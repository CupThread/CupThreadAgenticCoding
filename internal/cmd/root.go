// Package cmd wires up the cupthread command tree.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
	"github.com/spf13/cobra"
)

// Version is the CLI version. It is a var so release builds can inject the
// git tag at link time:
//
//	go build -ldflags "-X github.com/CupThread/CupThreadAgenticCoding/internal/cmd.Version=0.3.0" ./cmd/cupthread
//
// The tag is then the single source of truth; builds without the flag report
// "dev" instead of a stale-looking fake version.
var Version = "dev"

var (
	flagJSON      bool
	flagOutput    string
	flagBaseURL   string
	flagConfig    string
	flagWorkspace string
	flagApp       string
	flagNoRetry   bool
)

// app carries shared state to all commands, built once per execution.
type app struct {
	cfg     *config.Config
	cfgPath string
	out     *output.Writer

	// cfgBaseline snapshots cfg as of the config load (see Snapshot), so
	// saveConfig can persist only the fields this invocation actually
	// changed instead of a stale whole-file copy (issue #139).
	cfgBaseline *config.Config

	client     *api.Client
	clientOnce sync.Once
	refreshMu  sync.Mutex
}

var A *app

// oauthRefreshTimeout bounds the transparent token refresh even when the
// caller's context is unbounded (cobra's context.Background), so a stalled
// token endpoint cannot hang an authenticated command; the auth package's
// HTTP client has its own timeout on top.
var oauthRefreshTimeout = 30 * time.Second

// Execute builds the command tree and runs it on a signal-aware context so
// Ctrl-C cancels in-flight HTTP requests cleanly instead of relying on the
// default process kill.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := newRootCmd()
	return root.ExecuteContext(ctx)
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "cupthread",
		Short:         "Manage your CupThread projects from the command line",
		Long: `cupthread — the official CupThread CLI.

Manage the projects you created on cupthread.com (workspaces, apps, inbox,
feature requests, roadmap columns, versions, changelog, imports, integrations,
notifications, billing) without leaving the terminal — nearly everything the
web Console can do. A few high-impact actions (member management, billing
changes, integration connect/disconnect, changelog publishing) are
Console-web-only: no CLI credential can perform them.

Log in with 'cupthread auth login' (OAuth via browser) or
'cupthread auth login --token cpt_...' (personal access token).`,
		Version:               Version,
		SilenceUsage:          true,
		SilenceErrors:         false,
		DisableFlagsInUseLine: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			format := output.FormatTable
			if flagOutput != "" {
				parsed, err := output.ParseFormat(flagOutput)
				if err != nil {
					return err
				}
				format = parsed
			} else if flagJSON {
				format = output.FormatJSON
			}
			A = &app{out: output.New(os.Stdout, format)}
			path := flagConfig
			if path == "" {
				var err error
				if path, err = config.Path(); err != nil {
					return err
				}
			}
			A.cfgPath = path
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			A.cfg = cfg
			A.cfgBaseline = cfg.Snapshot()
			A.warnOAuthIssuerDivergence()
			A.client = A.buildClient()
			return nil
		},
	}

	pf := root.PersistentFlags()
	pf.StringVarP(&flagOutput, "output", "o", "", "Output format for results: table, json or yaml (default table)")
	pf.BoolVar(&flagJSON, "json", false, "Shorthand for --output json")
	pf.StringVar(&flagBaseURL, "base-url", "", "API base URL (default $CUPTHREAD_BASE_URL, then https://api.cupthread.com); OAuth token refresh/revocation always target the credential's issuing server")
	pf.StringVar(&flagConfig, "config", "", "Config file path (default $CUPTHREAD_CONFIG, then ~/.config/cupthread/config.json)")
	pf.StringVarP(&flagWorkspace, "workspace", "w", "", "Workspace ID (default: saved default from 'workspaces use')")
	pf.StringVarP(&flagApp, "app", "a", "", "App ID (default: saved default from 'apps use')")
	pf.BoolVar(&flagNoRetry, "no-retry", false, "Disable automatic retry/backoff on transient failures (429/502/503/504 on idempotent requests); also $CUPTHREAD_NO_RETRY=1")

	root.AddCommand(
		newAuthCmd(),
		newMeCmd(),
		newWorkspacesCmd(),
		newAppsCmd(),
		newInboxCmd(),
		newFeaturesCmd(),
		newCommentsCmd(),
		newColumnsCmd(),
		newVersionsCmd(),
		newChangelogCmd(),
		newImportsCmd(),
		newIntegrationsCmd(),
		newNotificationsCmd(),
		newBillingCmd(),
		newSearchCmd(),
		newUsersCmd(),
		newAPICmd(),
		newStatusCmd(),
		newSkillsCmd(),
	)
	return root
}

// noRetryEnv reports whether $CUPTHREAD_NO_RETRY opts out of the
// transient-failure retry loop; any non-empty value except 0/false enables
// the opt-out.
func noRetryEnv() bool {
	switch os.Getenv("CUPTHREAD_NO_RETRY") {
	case "", "0", "false":
		return false
	default:
		return true
	}
}

// structured reports whether output should be machine-formatted (json/yaml)
// rather than the default human table.
func (a *app) structured() bool {
	return a.out.Format != output.FormatTable
}

// baseURL resolves the API origin from flags, env, config, then the default.
func (a *app) baseURL() string {
	if flagBaseURL != "" {
		return strings.TrimRight(flagBaseURL, "/")
	}
	if env := os.Getenv("CUPTHREAD_BASE_URL"); env != "" {
		return strings.TrimRight(env, "/")
	}
	if a.cfg.BaseURL != "" {
		return strings.TrimRight(a.cfg.BaseURL, "/")
	}
	return config.DefaultBaseURL
}

// warnOAuthIssuerDivergence surfaces a deliberate base-URL override that
// disagrees with the server that issued the stored OAuth credential: ordinary
// API calls follow the override, while token refresh and revocation stay
// pinned to the issuer (issue #191). One stderr warning per invocation, in
// every output mode, so neither the human nor a parsing agent mistakes a
// retargeted run for one talking to the credential's own server. It stays
// quiet without an explicit --base-url/$CUPTHREAD_BASE_URL override (the
// effective URL then matches the issuer by construction) and while
// $CUPTHREAD_TOKEN keeps the stored credential inactive.
func (a *app) warnOAuthIssuerDivergence() {
	if flagBaseURL == "" && os.Getenv("CUPTHREAD_BASE_URL") == "" {
		return
	}
	if config.EnvToken() != "" {
		return
	}
	authState := a.cfg.Auth
	if authState == nil || authState.Method != "oauth" {
		return
	}
	effective, issuer := a.baseURL(), authState.IssuerBaseURL(a.cfg.BaseURL)
	if effective == issuer {
		return
	}
	fmt.Fprintf(os.Stderr, "warning: API requests target %s, but the stored OAuth credential was issued by %s — API calls follow the override while token refresh and revocation stay pinned to the issuing server\n", effective, issuer)
}

// buildClient wires the API client with a token provider that transparently
// refreshes expired OAuth tokens and persists the rotated pair. The refresh
// is serialized across processes via a config lock and a disk re-read, so
// two concurrent invocations never replay the same single-use refresh token
// (issue #62).
func (a *app) buildClient() *api.Client {
	client := api.New(a.baseURL())
	client.WorkspaceID = flagWorkspace
	client.NoRetry = flagNoRetry || noRetryEnv()
	// Retry notices go to stderr and only in human mode, so stdout and the
	// machine-readable stream stay parse-clean.
	if !a.structured() {
		client.Stderr = os.Stderr
	}
	client.Token = func(ctx context.Context) (string, error) {
		if env := config.EnvToken(); env != "" {
			if err := config.ValidateToken(env); err != nil {
				return "", fmt.Errorf("invalid $CUPTHREAD_TOKEN: %w", err)
			}
			return env, nil
		}
		authState := a.cfg.Auth
		if authState == nil || authState.AccessToken == "" {
			return "", errors.New("not logged in: run 'cupthread auth login' or set $CUPTHREAD_TOKEN")
		}
		if authState.Method != "oauth" || authState.RefreshToken == "" || authState.ExpiresAt == "" {
			return authState.AccessToken, nil
		}
		expiresAt, err := time.Parse(time.RFC3339, authState.ExpiresAt)
		if err != nil || time.Until(expiresAt) > time.Minute {
			return authState.AccessToken, nil
		}
		// Snapshot the pair values: another goroutine can mutate a.cfg.Auth
		// in place while we wait, so comparing through the pointer would
		// never observe its rotation.
		snap := *authState
		a.refreshMu.Lock()
		defer a.refreshMu.Unlock()
		// Another request may have refreshed while we waited on the lock.
		if cur := a.cfg.Auth; cur != nil &&
			(cur.RefreshToken != snap.RefreshToken || cur.ExpiresAt != snap.ExpiresAt) {
			return cur.AccessToken, nil
		}
		return a.refreshAcrossProcesses(ctx, &snap)
	}
	return client
}

// unauthenticatedClient returns a credential-free client with the same
// retry/notice wiring as buildClient's, for the public (pre-login) endpoints
// and the auth login --token session probe. Hand-rolled api.New(...) clients
// at those call sites silently dropped --no-retry/$CUPTHREAD_NO_RETRY and the
// human-mode stderr retry notices (issue #202).
func (a *app) unauthenticatedClient() *api.Client {
	client := api.New(a.baseURL())
	client.NoRetry = flagNoRetry || noRetryEnv()
	// Retry notices go to stderr and only in human mode, so stdout and the
	// machine-readable stream stay parse-clean.
	if !a.structured() {
		client.Stderr = os.Stderr
	}
	return client
}

// refreshAcrossProcesses rotates the stored OAuth pair without the
// cross-process race in which one concurrent CLI invocation replays an
// already-rotated refresh token; the server treats the replay as theft and
// revokes the entire descendant rotation chain, permanently bricking every
// future invocation (issue #62). It holds an exclusive lock on
// <config>.lock, re-reads the on-disk pair under the lock so a rotation
// another process already committed is adopted instead of replayed, and
// persists the rotated pair merged onto the latest on-disk config. The token
// endpoint is resolved from the credential's issuing server, never from the
// effective --base-url override: the refresh token must only ever travel to
// the server that minted it (issue #191).
func (a *app) refreshAcrossProcesses(ctx context.Context, snap *config.Auth) (string, error) {
	lock, err := config.LockConfig(a.cfgPath)
	if err != nil {
		return "", fmt.Errorf("lock config for token refresh: %w", err)
	}
	defer func() { _ = lock.Close() }()

	// Cross-process double-check: another CLI process may have rotated the
	// pair between this process's start and now. Adopting the disk pair
	// skips the token endpoint entirely — the disk-level analogue of the
	// in-process double-check above.
	disk, err := config.Load(a.cfgPath)
	if err != nil {
		return "", fmt.Errorf("re-read config before token refresh: %w", err)
	}
	refreshToken := snap.RefreshToken
	if d := disk.Auth; d != nil && d.RefreshToken != "" && d.RefreshToken != snap.RefreshToken {
		if d.AccessToken == "" {
			return "", errors.New("stored OAuth credential has a refresh token but no access token; run 'cupthread auth login' again")
		}
		a.cfg.Auth = d
		return d.AccessToken, nil
	}
	if d := disk.Auth; d != nil && d.RefreshToken != "" {
		refreshToken = d.RefreshToken
	}

	_, tokenURL, _, _ := auth.Endpoints(snap.IssuerBaseURL(a.cfg.BaseURL))
	refreshCtx, cancel := context.WithTimeout(ctx, oauthRefreshTimeout)
	defer cancel()
	set, err := auth.Refresh(refreshCtx, tokenURL, snap.ClientID, refreshToken)
	if err != nil {
		// invalid_grant after the re-read means a winner's rotation landed in
		// the window between our disk read and the server processing our
		// refresh. The winner persisted its still-valid pair before we
		// failed, so re-read once and adopt it instead of surfacing the
		// (now misleading) re-login demand.
		if disk2, lerr := config.Load(a.cfgPath); lerr == nil {
			if d := disk2.Auth; d != nil && d.AccessToken != "" && d.RefreshToken != "" &&
				d.RefreshToken != refreshToken {
				a.cfg.Auth = d
				return d.AccessToken, nil
			}
		}
		var apiErr *auth.APIError
		if errors.As(err, &apiErr) && apiErr.Code == "invalid_grant" {
			return "", fmt.Errorf("refresh OAuth token: the OAuth credential chain was revoked server-side (likely a concurrent refresh race or replay detection); run 'cupthread auth login' again: %w", err)
		}
		return "", fmt.Errorf("refresh OAuth token (run 'cupthread auth login' again): %w", err)
	}
	a.applyTokenSet(set)
	// Merge-save so the refresh only claims the auth section and never
	// reverts defaults another process wrote meanwhile.
	if err := a.saveAuthUnderLock(); err != nil {
		return set.AccessToken, fmt.Errorf("save refreshed tokens: %w", err)
	}
	return set.AccessToken, nil
}

// saveAuthUnderLock persists the in-memory auth pair onto a fresh read of
// the on-disk config, leaving every other field exactly as the disk has it.
// Callers must hold the config lock; the merge means a concurrent
// `workspaces use` or `apps use` survives the refresh. A PAT stored by a
// concurrent `auth login --token` wins and the save is skipped — the newer
// credential must not be clobbered. Either outcome acknowledges the
// in-memory auth section in the baseline, so a later saveConfig in this
// process never re-applies the skipped or already-persisted pair over newer
// disk state.
func (a *app) saveAuthUnderLock() error {
	if a.cfg.Auth == nil {
		return nil
	}
	disk, err := config.Load(a.cfgPath)
	if err != nil {
		return err
	}
	if disk.Auth != nil && disk.Auth.Method == "token" {
		a.acknowledgeAuth()
		return nil
	}
	disk.Auth = a.cfg.Auth
	if err := disk.Save(a.cfgPath); err != nil {
		return err
	}
	a.cfg = disk
	a.acknowledgeAuth()
	return nil
}

// acknowledgeAuth marks the in-memory auth section as the state a future
// saveConfig should diff against, so an auth section this process already
// persisted (or deliberately declined to persist) is not treated as a
// pending change and re-applied over a concurrently stored credential.
func (a *app) acknowledgeAuth() {
	if a.cfgBaseline == nil {
		a.cfgBaseline = a.cfg.Snapshot()
		return
	}
	if a.cfg.Auth == nil {
		a.cfgBaseline.Auth = nil
		return
	}
	authCopy := *a.cfg.Auth
	a.cfgBaseline.Auth = &authCopy
}

// applyTokenSet stores a fresh OAuth token pair on the config. It deliberately
// leaves Auth.IssuedBaseURL untouched: the only caller-driven stamping happens
// at login (rememberLoginBaseURL), because during a transparent refresh the
// effective base URL may be a deliberate override while the credential's
// issuer never changes — re-stamping here would aim the next refresh at the
// override host (issue #191).
func (a *app) applyTokenSet(set *auth.TokenSet) {
	method := "oauth"
	prefix := ""
	if n := len(set.AccessToken); n > 12 {
		prefix = set.AccessToken[:12]
	} else {
		prefix = set.AccessToken
	}
	if a.cfg.Auth == nil {
		a.cfg.Auth = &config.Auth{}
	}
	a.cfg.Auth.Method = method
	a.cfg.Auth.AccessToken = set.AccessToken
	a.cfg.Auth.RefreshToken = set.RefreshToken
	a.cfg.Auth.ClientID = auth.FirstPartyClientID
	a.cfg.Auth.TokenPrefix = prefix
	if set.ExpiresIn > 0 {
		a.cfg.Auth.ExpiresAt = time.Now().Add(time.Duration(set.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	} else {
		a.cfg.Auth.ExpiresAt = ""
	}
}

// saveConfig persists this invocation's config changes under the
// cross-process config lock, merged onto a fresh read of the on-disk file:
// only the fields this invocation changed since it loaded the config (the
// cfgBaseline snapshot) are applied to the latest disk snapshot, so
// concurrent CLI invocations — a 'workspaces use' racing an 'apps use', or
// either racing a credential write — never lose each other's updates
// (issue #139). The in-memory config and baseline are adopted from the
// merged result so a follow-up save in the same process carries only newer
// changes.
func (a *app) saveConfig() error {
	baseline := a.cfgBaseline
	if baseline == nil {
		// Hand-assembled app without a load-time snapshot: diff against an
		// empty config, so every in-memory field counts as this
		// invocation's change — the whole-file-save behavior such callers
		// always had. (Snapping a.cfg here instead would bless its pending
		// changes as already-persisted and drop them from the merge.)
		baseline = &config.Config{}
	}
	merged, err := config.Update(a.cfgPath, baseline, a.cfg)
	if err != nil {
		return err
	}
	a.cfg = merged
	a.cfgBaseline = merged.Snapshot()
	return nil
}

// workspaceID resolves the workspace for workspace-scoped commands.
func (a *app) workspaceID() (string, error) {
	if flagWorkspace != "" {
		return flagWorkspace, nil
	}
	if a.cfg.DefaultWorkspace != "" {
		return a.cfg.DefaultWorkspace, nil
	}
	return "", errors.New("no workspace selected: pass --workspace <id> or run 'cupthread workspaces use <id>'")
}

// requireAppID resolves the app for app-scoped commands.
func (a *app) requireAppID() (string, error) {
	if flagApp != "" {
		return flagApp, nil
	}
	ws, err := a.workspaceID()
	if err != nil {
		return "", err
	}
	if prefs, ok := a.cfg.Workspaces[ws]; ok && prefs.DefaultApp != "" {
		return prefs.DefaultApp, nil
	}
	return "", errors.New("no app selected: pass --app <id> or run 'cupthread apps use <id>'")
}

// optionalAppID resolves the app like requireAppID but returns "" instead of
// an error when neither the --app flag nor a saved default app is set, for
// commands that fall back to workspace-wide scoping.
func (a *app) optionalAppID() string {
	if flagApp != "" {
		return flagApp
	}
	ws, err := a.workspaceID()
	if err != nil {
		return ""
	}
	if prefs, ok := a.cfg.Workspaces[ws]; ok {
		return prefs.DefaultApp
	}
	return ""
}

// lookupApp lists the workspace's apps and matches id, slug or name. Ids and
// slugs are unique server-side and short-circuit; name matches are collected
// exhaustively because display names are not unique (the API only dedupes
// slugs), so an ambiguous name must fail loudly instead of silently picking
// the first list entry.
func (a *app) lookupApp(ctx context.Context, ref string) (*api.AppRecord, error) {
	ws, err := a.workspaceID()
	if err != nil {
		return nil, err
	}
	client := a.client
	client.WorkspaceID = ws
	var resp api.ListAppsResponse
	if err := client.Do(ctx, "GET", "/api/v1/console/workspaces/"+ws+"/apps", nil, nil, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Apps {
		appRec := &resp.Apps[i]
		if appRec.AppID == ref || appRec.Slug == ref {
			return appRec, nil
		}
	}
	var byName []*api.AppRecord
	for i := range resp.Apps {
		appRec := &resp.Apps[i]
		if appRec.Name == ref {
			byName = append(byName, appRec)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return nil, fmt.Errorf("app %q not found in workspace (see 'cupthread apps list')", ref)
	default:
		candidates := make([]string, len(byName))
		for i, appRec := range byName {
			candidates[i] = fmt.Sprintf("%s (%s, slug %s)", appRec.Name, appRec.AppID, appRec.Slug)
		}
		return nil, fmt.Errorf("app %q is ambiguous; it matches %d apps in this workspace:\n  - %s\nretry with the app id or slug instead (see 'cupthread apps list')",
			ref, len(byName), strings.Join(candidates, "\n  - "))
	}
}

// lookupWorkspace resolves a workspace reference (id, slug or name) via
// /console/me, with the same unique-id/slug short-circuit and exhaustive,
// ambiguity-checked name pass as lookupApp.
func (a *app) lookupWorkspace(ctx context.Context, ref string) (*api.Workspace, error) {
	var me api.MeResponse
	if err := a.client.Do(ctx, "GET", "/api/v1/console/me", nil, nil, &me); err != nil {
		return nil, err
	}
	for i := range me.Workspaces {
		entry := &me.Workspaces[i]
		if entry.Workspace.ID == ref || entry.Workspace.Slug == ref {
			return &entry.Workspace, nil
		}
	}
	var byName []*api.Workspace
	for i := range me.Workspaces {
		entry := &me.Workspaces[i]
		if entry.Workspace.Name == ref {
			byName = append(byName, &entry.Workspace)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return nil, fmt.Errorf("workspace %q not found (see 'cupthread workspaces list')", ref)
	default:
		candidates := make([]string, len(byName))
		for i, ws := range byName {
			candidates[i] = fmt.Sprintf("%s (%s, slug %s)", ws.Name, ws.ID, ws.Slug)
		}
		return nil, fmt.Errorf("workspace %q is ambiguous; it matches %d of your workspaces:\n  - %s\nretry with the workspace id or slug instead (see 'cupthread workspaces list')",
			ref, len(byName), strings.Join(candidates, "\n  - "))
	}
}
