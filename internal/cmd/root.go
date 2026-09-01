// Package cmd wires up the cupthread command tree.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
	"github.com/spf13/cobra"
)

// Version is the CLI version.
const Version = "0.2.0"

var (
	flagJSON      bool
	flagOutput    string
	flagBaseURL   string
	flagConfig    string
	flagWorkspace string
	flagApp       string
)

// app carries shared state to all commands, built once per execution.
type app struct {
	cfg     *config.Config
	cfgPath string
	out     *output.Writer

	client     *api.Client
	clientOnce sync.Once
	refreshMu  sync.Mutex
}

var A *app

// Execute builds the command tree and runs it.
func Execute() error {
	root := newRootCmd()
	return root.Execute()
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "cupthread",
		Short:         "Manage your CupThread projects from the command line",
		Long: `cupthread — the official CupThread CLI.

Manage the projects you created on cupthread.com (workspaces, apps, inbox,
feature requests, roadmap columns, versions, changelog, imports, integrations,
notifications, billing) without leaving the terminal — everything the web
Console can do.

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
			A.client = A.buildClient()
			return nil
		},
	}

	pf := root.PersistentFlags()
	pf.StringVarP(&flagOutput, "output", "o", "", "Output format for results: table, json or yaml (default table)")
	pf.BoolVar(&flagJSON, "json", false, "Shorthand for --output json")
	pf.StringVar(&flagBaseURL, "base-url", "", "API base URL (default $CUPTHREAD_BASE_URL, then https://api.cupthread.com)")
	pf.StringVar(&flagConfig, "config", "", "Config file path (default $CUPTHREAD_CONFIG, then ~/.config/cupthread/config.json)")
	pf.StringVarP(&flagWorkspace, "workspace", "w", "", "Workspace ID (default: saved default from 'workspaces use')")
	pf.StringVarP(&flagApp, "app", "a", "", "App ID (default: saved default from 'apps use')")

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

// buildClient wires the API client with a token provider that transparently
// refreshes expired OAuth tokens and persists the rotated pair.
func (a *app) buildClient() *api.Client {
	client := api.New(a.baseURL())
	client.WorkspaceID = flagWorkspace
	client.Token = func(ctx context.Context) (string, error) {
		if env := config.EnvToken(); env != "" {
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
		a.refreshMu.Lock()
		defer a.refreshMu.Unlock()
		// Another request may have refreshed while we waited on the lock.
		if a.cfg.Auth.ExpiresAt != authState.ExpiresAt {
			return a.cfg.Auth.AccessToken, nil
		}
		_, tokenURL, _, _ := auth.Endpoints(a.baseURL())
		set, err := auth.Refresh(ctx, tokenURL, authState.ClientID, a.cfg.Auth.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("refresh OAuth token (run 'cupthread auth login' again): %w", err)
		}
		a.applyTokenSet(set)
		if err := a.cfg.Save(a.cfgPath); err != nil {
			return set.AccessToken, fmt.Errorf("save refreshed tokens: %w", err)
		}
		return set.AccessToken, nil
	}
	return client
}

// applyTokenSet stores a fresh OAuth token pair on the config.
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

// saveConfig persists the config to disk.
func (a *app) saveConfig() error {
	return a.cfg.Save(a.cfgPath)
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

// lookupApp lists the workspace's apps and matches id, slug or name.
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
		if appRec.AppID == ref || appRec.Slug == ref || appRec.Name == ref {
			return appRec, nil
		}
	}
	return nil, fmt.Errorf("app %q not found in workspace (see 'cupthread apps list')", ref)
}

// lookupWorkspace resolves a workspace reference (id or slug) via /console/me.
func (a *app) lookupWorkspace(ctx context.Context, ref string) (*api.Workspace, error) {
	var me api.MeResponse
	if err := a.client.Do(ctx, "GET", "/api/v1/console/me", nil, nil, &me); err != nil {
		return nil, err
	}
	for i := range me.Workspaces {
		entry := &me.Workspaces[i]
		if entry.Workspace.ID == ref || entry.Workspace.Slug == ref || entry.Workspace.Name == ref {
			return &entry.Workspace, nil
		}
	}
	return nil, fmt.Errorf("workspace %q not found (see 'cupthread workspaces list')", ref)
}
