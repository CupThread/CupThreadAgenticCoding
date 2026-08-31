package cmd

import (
	"errors"
	"fmt"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

var importProviders = []string{"linear", "notion", "slack"}

func newIntegrationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integrations",
		Short: "Manage workspace integrations (GitHub, Linear, Notion, Slack)",
	}
	cmd.AddCommand(newIntegrationsStatusCmd(), newIntegrationsGitHubCmd())
	for _, p := range importProviders {
		cmd.AddCommand(newIntegrationsProviderCmd(p))
	}
	return cmd
}

func newIntegrationsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show connection status of all integrations",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if flagJSON {
				out := map[string]any{}
				var gh api.GitHubIntegrationResponse
				if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/integrations/github"), nil, nil, &gh); err != nil {
					return err
				}
				out["github"] = gh.Integration
				for _, p := range importProviders {
					var resp api.ImportIntegrationResponse
					if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/integrations/"+p), nil, nil, &resp); err != nil {
						return err
					}
					out[p] = resp.Integration
					out[p+"_oauthConfigured"] = resp.OAuthConfigured
				}
				return A.out.JSON(out)
			}
			rows := [][]string{}
			var gh api.GitHubIntegrationResponse
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/integrations/github"), nil, nil, &gh); err != nil {
				return err
			}
			rows = append(rows, integrationRow("github", gh.Integration != nil, orDash(deref(gh.Integration.AccountLogin))))
			for _, p := range importProviders {
				var resp api.ImportIntegrationResponse
				if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/integrations/"+p), nil, nil, &resp); err != nil {
					return err
				}
				account := "—"
				if resp.Integration != nil {
					account = orDash(deref(resp.Integration.AccountLogin))
				}
				extra := ""
				if !resp.OAuthConfigured {
					extra = " (OAuth not configured: use manual token)"
				}
				rows = append(rows, integrationRow(p, resp.Integration != nil, account+extra))
			}
			A.out.Table([]string{"Provider", "Connected", "Account"}, rows)
			return nil
		},
	}
}

func integrationRow(provider string, connected bool, account string) []string {
	state := "no"
	if connected {
		state = "yes"
	}
	return []string{provider, state, account}
}

func newIntegrationsGitHubCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "github", Short: "GitHub integration (OAuth, repos, per-app config, sync)"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "auth-url",
			Short: "Print the GitHub OAuth authorize URL to open",
			DisableFlagsInUseLine: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				ws, err := workspaceClient(cmd.Context())
				if err != nil {
					return err
				}
				var resp api.AuthorizeURLResponse
				if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/integrations/github/authorize"), nil, nil, &resp); err != nil {
					return err
				}
				if flagJSON {
					return A.out.JSON(resp)
				}
				A.out.Printf("%s", resp.URL)
				return nil
			},
		},
		newGitHubConnectCmd(),
		&cobra.Command{
			Use:   "disconnect",
			Short: "Disconnect the GitHub integration",
			RunE: func(cmd *cobra.Command, args []string) error {
				ws, err := workspaceClient(cmd.Context())
				if err != nil {
					return err
				}
				if err := A.client.Do(cmd.Context(), "DELETE", wsPath(ws, "/integrations/github"), nil, nil, nil); err != nil {
					return err
				}
				if !flagJSON {
					A.out.Printf("✓ Disconnected GitHub")
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "repos",
			Short: "List GitHub repositories accessible to the integration",
			DisableFlagsInUseLine: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				ws, err := workspaceClient(cmd.Context())
				if err != nil {
					return err
				}
				var resp api.GitHubReposResponse
				if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/integrations/github/repos"), nil, nil, &resp); err != nil {
					return err
				}
				if flagJSON {
					return A.out.JSON(resp)
				}
				rows := make([][]string, 0, len(resp.Repos))
				for _, r := range resp.Repos {
					rows = append(rows, []string{r.FullName, boolYesNo(r.IsPrivate), boolYesNo(r.HasDiscussions), boolYesNo(r.HasIssues), orDash(r.HTMLURL)})
				}
				A.out.Table([]string{"Repository", "Private", "Discussions", "Issues", "URL"}, rows)
				return nil
			},
		},
		newGitHubCategoriesCmd(),
		newGitHubConfigCmd(),
		newGitHubSyncCmd(),
	)
	return cmd
}

func newGitHubConnectCmd() *cobra.Command {
	var token string
	connect := &cobra.Command{
		Use:   "connect --token <github-pat>",
		Short: "Connect GitHub with a personal access token",
		Long: `Connect the GitHub integration with a manual personal access token.

Alternatively run 'cupthread integrations github auth-url', open the printed
URL in a browser and finish the OAuth flow in the Console.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				return errors.New("--token is required (or use auth-url for the OAuth flow)")
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]string{"token": token}
			var resp struct {
				Integration api.WorkspaceIntegration `json:"integration"`
			}
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/integrations/github/manual-token"), nil, body, &resp); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(resp.Integration)
			}
			A.out.Printf("✓ Connected GitHub as %s", orDash(deref(resp.Integration.AccountLogin)))
			return nil
		},
	}
	connect.Flags().StringVar(&token, "token", "", "GitHub personal access token (required)")
	return connect
}

func newGitHubCategoriesCmd() *cobra.Command {
	var owner, repo string
	categories := &cobra.Command{
		Use:   "categories",
		Short: "List Discussion categories of a GitHub repository",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if owner == "" || repo == "" {
				return errors.New("--owner and --repo are required")
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			path := fmt.Sprintf("%s/integrations/github/repos/%s/%s/categories", wsPath(ws, ""), owner, repo)
			var resp api.GitHubCategoriesResponse
			if err := A.client.Do(cmd.Context(), "GET", path, nil, nil, &resp); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(resp)
			}
			rows := make([][]string, 0, len(resp.Categories))
			for _, c := range resp.Categories {
				rows = append(rows, []string{c.ID, c.Name, c.Slug})
			}
			A.out.Table([]string{"Category ID", "Name", "Slug"}, rows)
			A.out.Printf("repositoryId: %s", resp.RepositoryID)
			return nil
		},
	}
	categories.Flags().StringVar(&owner, "owner", "", "Repository owner (required)")
	categories.Flags().StringVar(&repo, "repo", "", "Repository name (required)")
	return categories
}

func newGitHubConfigCmd() *cobra.Command {
	var owner, repo, repositoryID, categoryID, categoryName, categorySlug, webhookSecret string
	var syncEnabled, statusSync, commentsSync bool
	config_ := &cobra.Command{
		Use:   "config <app-id-or-slug>",
		Short: "Set the per-app GitHub repository and sync options",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			appRec, err := A.lookupApp(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("owner") {
				body["githubOwner"] = nilIfEmpty(owner)
			}
			if cmd.Flags().Changed("repo") {
				body["githubRepo"] = nilIfEmpty(repo)
			}
			if cmd.Flags().Changed("repository-id") {
				body["githubRepositoryId"] = nilIfEmpty(repositoryID)
			}
			if cmd.Flags().Changed("category-id") {
				body["githubDiscussionCategoryId"] = nilIfEmpty(categoryID)
			}
			if cmd.Flags().Changed("category-name") {
				body["githubDiscussionCategoryName"] = nilIfEmpty(categoryName)
			}
			if cmd.Flags().Changed("category-slug") {
				body["githubDiscussionCategorySlug"] = nilIfEmpty(categorySlug)
			}
			if cmd.Flags().Changed("webhook-secret") {
				body["githubWebhookSecret"] = nilIfEmpty(webhookSecret)
			}
			if cmd.Flags().Changed("sync-enabled") {
				body["githubSyncEnabled"] = syncEnabled
			}
			if cmd.Flags().Changed("status-sync") {
				body["githubSyncStatusEnabled"] = statusSync
			}
			if cmd.Flags().Changed("comments-sync") {
				body["githubSyncCommentsEnabled"] = commentsSync
			}
			if len(body) == 0 {
				return errors.New("nothing to update: pass at least one flag")
			}
			path := fmt.Sprintf("%s/apps/%s/github", wsPath(ws, ""), appRec.AppID)
			if err := A.client.Do(cmd.Context(), "PATCH", path, nil, body, nil); err != nil {
				return err
			}
			if !flagJSON {
				A.out.Printf("✓ GitHub config updated for %s", appRec.AppID)
			}
			return nil
		},
	}
	config_.Flags().StringVar(&owner, "owner", "", "Repository owner")
	config_.Flags().StringVar(&repo, "repo", "", "Repository name")
	config_.Flags().StringVar(&repositoryID, "repository-id", "", "GitHub repository node ID (see integrations github repos)")
	config_.Flags().StringVar(&categoryID, "category-id", "", "Discussion category ID (see integrations github categories)")
	config_.Flags().StringVar(&categoryName, "category-name", "", "Discussion category name")
	config_.Flags().StringVar(&categorySlug, "category-slug", "", "Discussion category slug")
	config_.Flags().StringVar(&webhookSecret, "webhook-secret", "", "GitHub webhook secret")
	config_.Flags().BoolVar(&syncEnabled, "sync-enabled", true, "Enable GitHub sync for this app")
	config_.Flags().BoolVar(&statusSync, "status-sync", true, "Sync status changes to GitHub")
	config_.Flags().BoolVar(&commentsSync, "comments-sync", false, "Sync comments to GitHub")
	return config_
}

func newGitHubSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync <app-id-or-slug>",
		Short: "Run a full bi-directional GitHub sync for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			appRec, err := A.lookupApp(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			path := fmt.Sprintf("%s/apps/%s/github/sync", wsPath(ws, ""), appRec.AppID)
			var resp api.GitHubSyncResponse
			if err := A.client.Do(cmd.Context(), "POST", path, nil, map[string]any{}, &resp); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(resp)
			}
			A.out.Printf("✓ Synced %d requests", resp.SyncedCount)
			for _, e := range resp.Errors {
				A.out.Printf("  error: %s", e)
			}
			return nil
		},
	}
}

// newIntegrationsProviderCmd builds the identical auth-url/connect/disconnect/
// status surface for one of linear/notion/slack.
func newIntegrationsProviderCmd(prov string) *cobra.Command {
	sub := &cobra.Command{Use: prov, Short: fmt.Sprintf("%s import integration", prov)}
	sub.AddCommand(
			&cobra.Command{
				Use:   "auth-url",
				Short: fmt.Sprintf("Print the %s OAuth authorize URL to open", prov),
				DisableFlagsInUseLine: true,
				RunE: func(cmd *cobra.Command, args []string) error {
					ws, err := workspaceClient(cmd.Context())
					if err != nil {
						return err
					}
					var resp struct {
						URL string `json:"url"`
					}
					if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/integrations/"+prov+"/authorize"), nil, nil, &resp); err != nil {
						return err
					}
					if flagJSON {
						return A.out.JSON(resp)
					}
					A.out.Printf("%s", resp.URL)
					return nil
				},
			},
			newProviderConnectCmd(prov),
			&cobra.Command{
				Use:   "disconnect",
				Short: fmt.Sprintf("Disconnect %s", prov),
				RunE: func(cmd *cobra.Command, args []string) error {
					ws, err := workspaceClient(cmd.Context())
					if err != nil {
						return err
					}
					if err := A.client.Do(cmd.Context(), "DELETE", wsPath(ws, "/integrations/"+prov), nil, nil, nil); err != nil {
						return err
					}
					if !flagJSON {
						A.out.Printf("✓ Disconnected %s", prov)
					}
					return nil
				},
			},
			&cobra.Command{
				Use:   "status",
				Short: fmt.Sprintf("Show %s connection status", prov),
				DisableFlagsInUseLine: true,
				RunE: func(cmd *cobra.Command, args []string) error {
					ws, err := workspaceClient(cmd.Context())
					if err != nil {
						return err
					}
					var resp api.ImportIntegrationResponse
					if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/integrations/"+prov), nil, nil, &resp); err != nil {
						return err
					}
					if flagJSON {
						return A.out.JSON(resp)
					}
					A.out.Printf("Connected: %s", boolYesNo(resp.Integration != nil))
					if resp.Integration != nil {
						A.out.Printf("Account:   %s", orDash(deref(resp.Integration.AccountLogin)))
					}
					A.out.Printf("OAuth configured: %s", boolYesNo(resp.OAuthConfigured))
					return nil
				},
			},
		)
	return sub
}

func newProviderConnectCmd(prov string) *cobra.Command {
	var token string
	connect := &cobra.Command{
		Use:   "connect --token <api-token>",
		Short: fmt.Sprintf("Connect %s with a manual API token", prov),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				return fmt.Errorf("--token is required (or use auth-url for the OAuth flow)")
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]string{"token": token}
			var resp struct {
				Integration api.WorkspaceIntegration `json:"integration"`
			}
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/integrations/"+prov+"/manual-token"), nil, body, &resp); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(resp.Integration)
			}
			A.out.Printf("✓ Connected %s as %s", prov, orDash(deref(resp.Integration.AccountLogin)))
			return nil
		},
	}
	connect.Flags().StringVar(&token, "token", "", fmt.Sprintf("%s API token (required)", prov))
	return connect
}
