// Package cmd wires up the cupthread command tree.
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
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
		Use:   "cupthread",
		Short: "Manage your CupThread projects from the command line",
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
			A.warnUnsafeConfigPermissions()
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

// repairConfigPermissions is the seam warnUnsafeConfigPermissions tightens
// the credential file through; a var so tests can force the repair-failure
// path that a read-only directory or foreign ownership produces in production.
var repairConfigPermissions = config.RepairPermissions

// warnUnsafeConfigPermissions re-checks the credential file's hygiene at load
// time (issue #186): Save always writes 0600, but a backup restore, a
// permissive-umask copy or a dotfiles sync can loosen the mode afterwards,
// and nothing flagged the stored tokens sitting readable by every local
// account. A group/world-readable file that StoresCredentials gets exactly
// one warning on stderr in every output mode — direct like the sibling
// warnOAuthIssuerDivergence, because warnf would print on stdout in table
// mode — and an immediate chmod 0600 repair, so the exposure window closes
// without user action while the warning still says the credential may have
// been readable. A failed repair degrades to the warning-only path (hinting
// at the manual chmod) and never blocks the command, and platforms without
// meaningful mode bits skip both halves via the no-op UnsafePermissions.
func (a *app) warnUnsafeConfigPermissions() {
	mode, unsafe := config.UnsafePermissions(a.cfgPath)
	if !unsafe || !a.cfg.StoresCredentials() {
		return
	}
	if err := repairConfigPermissions(a.cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: config file %s is group/world-readable (%04o) and stores your cupthread credentials — tighten it with 'chmod 600 %s' (automatic repair failed: %v)\n", a.cfgPath, mode, a.cfgPath, err)
		return
	}
	fmt.Fprintf(os.Stderr, "warning: config file %s was group/world-readable (%04o) and stores your cupthread credentials; tightened the mode to 0600 — run 'cupthread auth login' to rotate the credential if it may have been read meanwhile\n", a.cfgPath, mode)
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
// <config>.lock for the whole rotation — including the persistence of the
// rotated pair and the recovery paths below — and resolves the token
// endpoint from the credential's issuing server, never from the effective
// --base-url override: the refresh token must only ever travel to the
// server that minted it (issue #191).
func (a *app) refreshAcrossProcesses(ctx context.Context, snap *config.Auth) (string, error) {
	lock, err := config.LockConfig(a.cfgPath)
	if err != nil {
		return "", fmt.Errorf("lock config for token refresh: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if !config.LockSupported {
		// The platform has no real config lock (issue #193): this refresh
		// runs without cross-process serialization. Mirrors the retry-notice
		// convention — one line on stderr, human mode only, so stdout and
		// the machine-readable stream stay parse-clean.
		if !a.structured() {
			fmt.Fprintf(os.Stderr, "warning: this platform has no cross-process config lock; concurrent cupthread invocations could race a token refresh — avoid running them simultaneously\n")
		}
	}
	return a.refreshLocked(ctx, snap)
}

// refreshLocked is the refresh body proper; the config lock is already held
// by refreshAcrossProcesses (or the recovery-adoption recursion below, which
// must not re-lock a lock it holds — flock on a second descriptor of the
// same file would deadlock against itself).
func (a *app) refreshLocked(ctx context.Context, snap *config.Auth) (string, error) {
	// Cross-process double-check: another CLI process may have rotated the
	// pair between this process's start and now. Adopting the disk pair
	// skips the token endpoint entirely — the disk-level analogue of the
	// in-process double-check above.
	disk, err := config.Load(a.cfgPath)
	if err != nil {
		return "", fmt.Errorf("re-read config before token refresh: %w", err)
	}
	refreshToken := snap.RefreshToken
	if d := disk.Auth; d != nil && d.RefreshToken != "" {
		switch {
		case d.RefreshToken == snap.RefreshToken:
			refreshToken = d.RefreshToken
		case diskAuthAtLeastAsFresh(d, snap):
			if d.AccessToken == "" {
				return "", errors.New("stored OAuth credential has a refresh token but no access token; run 'cupthread auth login' again")
			}
			a.cfg.Auth = d
			// Acknowledge the adopted pair (issue #285): with the baseline left
			// at the load-time pair, the next saveConfig in this process would
			// diff adopted-vs-baseline as a pending change and re-apply the
			// possibly-stale pair over whatever newer credential another process
			// persisted in between — replaying a consumed single-use refresh
			// token (revoked chain) or clobbering a concurrent PAT, exactly what
			// acknowledgeAuth exists to prevent.
			a.acknowledgeAuth()
			return d.AccessToken, nil
		default:
			// The on-disk pair is staler than the in-memory snapshot — the
			// shape a recovered rotation leaves behind (issue #299): the
			// recovered pair lives in this process (and in its recovery
			// file) while the disk still holds the consumed predecessor.
			// Adopting the stale disk pair would resurrect a dead access
			// token, so the refresh below uses the snapshot's token.
		}
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
				d.RefreshToken != refreshToken && diskAuthAtLeastAsFresh(d, snap) {
				a.cfg.Auth = d
				// Same baseline acknowledgment as the disk-pair adoption
				// above (issue #285): the recovered pair must not be
				// re-applied by a later saveConfig over a newer credential.
				a.acknowledgeAuth()
				return d.AccessToken, nil
			}
		}
		var apiErr *auth.APIError
		if errors.As(err, &apiErr) && apiErr.Code == "invalid_grant" {
			if adopted, recPath := newestAdoptableRecovery(a.cfgPath, refreshToken); adopted != nil {
				a.cfg.Auth = adopted
				// Same baseline acknowledgment as the disk adoptions above
				// (issue #285): the adopted pair must never be re-applied by
				// a later saveConfig over a newer concurrent credential.
				a.acknowledgeAuth()
				// A recovered pair whose access token has already expired
				// cannot serve this request; rotate it right now under the
				// still-held lock (issue #299). The recursion is bounded:
				// each level either succeeds, adopts a strictly newer
				// recovery pair (the one just tried is excluded), or falls
				// through to the revoked-chain error.
				if !authAccessUsable(adopted, time.Now()) {
					return a.refreshLocked(ctx, adopted)
				}
				if serr := a.saveAuthWithRetry(); serr != nil {
					a.removeRecoveryFiles(recPath)
					warnStderrLine("warning: recovered the OAuth credential pair from %s, but it could not be saved to %s (%v) — the pair stays in the recovery file until a save succeeds", recPath, a.cfgPath, serr)
					return adopted.AccessToken, nil
				}
				a.removeRecoveryFiles("")
				return adopted.AccessToken, nil
			}
			return "", fmt.Errorf("refresh OAuth token: the OAuth credential chain was revoked server-side (likely a concurrent refresh race or replay detection); run 'cupthread auth login' again: %w", err)
		}
		return "", fmt.Errorf("refresh OAuth token (run 'cupthread auth login' again): %w", err)
	}
	a.applyTokenSet(set)
	if err := a.persistRotatedPair(); err != nil {
		return "", err
	}
	return set.AccessToken, nil
}

// authSaveRetryDelays bounds the merge-save retry: transient local I/O
// failures (EIO, ENOSPC pressure easing, a Windows rename budget) usually
// clear in milliseconds, and retrying under the still-held config lock
// cannot collide with another writer. A var so tests run without the sleeps.
var authSaveRetryDelays = []time.Duration{50 * time.Millisecond, 150 * time.Millisecond}

// persistAuthPair is the seam persistRotatedPair saves through; a var so
// tests can force deterministic persistence failures without chmod games.
var persistAuthPair = (*app).saveAuthUnderLock

// persistRotatedPair persists the just-rotated pair (issue #299). The
// rotation is single-use server-side — the moment auth.Refresh succeeded,
// the on-disk refresh token was invalidated — so persistence is the only
// thing standing between a working credential chain and a bricked one, and
// a transient local disk error must not be fatal: the merge-save is retried
// under the still-held lock, and if the disk genuinely cannot take the
// write, the pair is recorded in a 0600 recovery file next to the config
// for the next invocation to adopt. The command itself still completes with
// the minted access token — a usable token and a warning, never the
// incoherent half-state of handing the caller a token behind an error.
func (a *app) persistRotatedPair() error {
	saveErr := a.saveAuthWithRetry()
	if saveErr == nil {
		// Persisted: every recovery copy predates this rotation and holds a
		// strictly staler pair of the chain — dead weight to clean up.
		a.removeRecoveryFiles("")
		return nil
	}
	// Acknowledge the rotated pair (issue #285): the baseline must not treat
	// it as pending, or a later saveConfig in this process would re-apply it
	// over a concurrent credential — the recovery file is the record now.
	a.acknowledgeAuth()
	recPath, rerr := writeRecoveryCredential(a.cfgPath, a.cfg.Auth)
	if rerr != nil {
		return fmt.Errorf("save refreshed tokens: %w; recovery copy failed too (%v) — the rotated pair is lost and 'cupthread auth login' will be needed", saveErr, rerr)
	}
	a.removeRecoveryFiles(recPath)
	warnStderrLine("warning: the refreshed OAuth credentials could not be saved to %s (%v); a recovery copy carrying the rotated pair was written to %s — the next command adopts it automatically, and it can be deleted once the config saves cleanly", a.cfgPath, saveErr, recPath)
	return nil
}

// saveAuthWithRetry persists the auth pair with a bounded retry (issue
// #299); the config lock stays held across every attempt, so no other
// writer can slip in between them.
func (a *app) saveAuthWithRetry() error {
	var err error
	for attempt := 0; ; attempt++ {
		if err = persistAuthPair(a); err == nil {
			return nil
		}
		if attempt >= len(authSaveRetryDelays) {
			return err
		}
		time.Sleep(authSaveRetryDelays[attempt])
	}
}

// diskAuthAtLeastAsFresh reports whether the on-disk pair is at least as new
// as the in-memory snapshot, compared on ExpiresAt (a later mint means a
// later expiry). The disk-adoption branches exist to pick up a rotation
// another process persisted, whose pair is strictly newer; since recovery
// adoption (issue #299) the in-memory pair can itself be newer than a stale
// on-disk one, and adopting the stale pair would resurrect a consumed
// access token. Same-second rotations degenerate to equal timestamps, which
// still adopt — the honest tie-break for the cross-process race, at the
// cost of a one-second ambiguity for a same-second recovery pair.
// Unparseable timestamps fall back to adopting, preserving the pre-recovery
// behavior for hand-edited configs.
func diskAuthAtLeastAsFresh(d, snap *config.Auth) bool {
	dAt, derr := time.Parse(time.RFC3339, d.ExpiresAt)
	sAt, serr := time.Parse(time.RFC3339, snap.ExpiresAt)
	if derr != nil || serr != nil {
		return true
	}
	return !dAt.Before(sAt)
}

// authAccessUsable reports whether an access token is still worth handing to
// the current request: it parses ExpiresAt and finds more than the
// provider's own refresh margin (one minute, mirroring buildClient) left.
// An empty or unparseable expiry counts as usable — the server rejects a
// dead token with a plain 401, and there is nothing local to decide from.
func authAccessUsable(authState *config.Auth, now time.Time) bool {
	if authState == nil || authState.AccessToken == "" {
		return false
	}
	if authState.ExpiresAt == "" {
		return true
	}
	exp, err := time.Parse(time.RFC3339, authState.ExpiresAt)
	if err != nil {
		return true
	}
	return exp.Sub(now) > time.Minute
}

// writeRecoveryCredential records a rotated pair that the config file could
// not take, in <cfgPath>.recovery-<unix-nano> next to the config (issue
// #299). The file is created O_EXCL with 0600 — the same hygiene Save
// applies, since it holds a full bearer+refresh pair — and its JSON payload
// is the config.Auth struct verbatim, so adoption restores every field the
// credential protocol needs (client id, pinned issuer, expiry).
func writeRecoveryCredential(cfgPath string, authState *config.Auth) (string, error) {
	if authState == nil {
		return "", errors.New("no in-memory credential to recover")
	}
	data, err := json.Marshal(authState)
	if err != nil {
		return "", fmt.Errorf("encode recovery credential: %w", err)
	}
	path := fmt.Sprintf("%s.recovery-%d", cfgPath, time.Now().UnixNano())
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create recovery credential file %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write recovery credential %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("sync recovery credential %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close recovery credential %s: %w", path, err)
	}
	return path, nil
}

// newestAdoptableRecovery returns the newest recovery file next to cfgPath
// that holds a plausible pair to adopt: parseable JSON, an OAuth method with
// both tokens present, and a refresh token different from triedRefresh (the
// token whose refresh just failed with invalid_grant — a recovery file
// carrying it is the stale record of an older rotation). Files with an
// unparseable timestamp suffix or unreadable/invalid content are skipped;
// the newest surviving candidate wins, because every older recovery file
// describes an earlier, already-superseded rotation of the same chain.
func newestAdoptableRecovery(cfgPath, triedRefresh string) (*config.Auth, string) {
	matches, _ := filepath.Glob(cfgPath + ".recovery-*")
	prefix := filepath.Base(cfgPath) + ".recovery-"
	type candidate struct {
		ts   int64
		path string
	}
	var cands []candidate
	for _, m := range matches {
		ts, err := strconv.ParseInt(strings.TrimPrefix(filepath.Base(m), prefix), 10, 64)
		if err != nil {
			continue
		}
		cands = append(cands, candidate{ts: ts, path: m})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].ts > cands[j].ts })
	for _, c := range cands {
		data, err := os.ReadFile(c.path)
		if err != nil {
			continue
		}
		var authState config.Auth
		if json.Unmarshal(data, &authState) != nil {
			continue
		}
		if authState.Method != "oauth" || authState.AccessToken == "" ||
			authState.RefreshToken == "" || authState.RefreshToken == triedRefresh {
			continue
		}
		return &authState, c.path
	}
	return nil, ""
}

// removeRecoveryFiles deletes the recovery files next to the config, except
// keep when non-empty (the file whose pair is still the only record of a
// live rotation). Best-effort: a file that survives is harmless stale
// hygiene, never a correctness problem, because adoption only ever runs
// under the config lock and validates the pair's content.
func (a *app) removeRecoveryFiles(keep string) {
	matches, _ := filepath.Glob(a.cfgPath + ".recovery-*")
	for _, m := range matches {
		if m == keep {
			continue
		}
		_ = os.Remove(m)
	}
}

// warnStderrLine writes one sanitized warning line to stderr in every output
// mode. It sits beside app.warnf for warnings emitted from the token
// provider mid-request: warnf's table-mode branch prints on stdout, which
// would split the human stream around a table and break the
// single-stdout-document contract, so these warnings go straight to stderr
// through the same strict stripper (issue #286).
func warnStderrLine(format string, args ...any) {
	fmt.Fprintln(os.Stderr, output.StripTerminalControls(fmt.Sprintf(format, args...)))
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
