package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

func newAppsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "Manage the apps in a workspace",
	}
	cmd.AddCommand(
		newAppsListCmd(),
		newAppsCreateCmd(),
		newAppsGetCmd(),
		newAppsUpdateCmd(),
		newAppsUseCmd(),
		newAppSettingsCmd(),
		newAppsPublicConfigCmd(),
		newAppsPublicChangelogCmd(),
	)
	return cmd
}

// newAppsPublicChangelogCmd fetches the public changelog feed served to the
// web portal and SDKs. The endpoint is unauthenticated and cursor-paginated
// (PROD-28): pass the previous page's nextCursor back via --cursor until
// hasMore is false.
func newAppsPublicChangelogCmd() *cobra.Command {
	var limit int
	var cursor string
	publicChangelog := &cobra.Command{
		Use:   "public-changelog <app-key>",
		Short: "Fetch an app's public changelog feed (no login required)",
		Long: `Fetch the published changelog feed served to the public web portal and SDKs.

Cursor-paginated: pass --cursor with the nextCursor value from the previous
page until the response reports hasMore=false:
  cupthread apps public-changelog <app-key>
  cupthread apps public-changelog <app-key> --limit 50
  cupthread apps public-changelog <app-key> --cursor <nextCursor>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{"limit": {strconv.Itoa(limit)}}
			if cursor != "" {
				q.Set("cursor", cursor)
			}
			path := "/api/v1/public/apps/" + url.PathEscape(args[0]) + "/changelog"
			var resp api.ListPublicChangelogResponse
			if err := api.New(A.baseURL()).Do(cmd.Context(), "GET", path, q, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			rows := make([][]string, 0, len(resp.Entries))
			for _, e := range resp.Entries {
				rows = append(rows, []string{
					shortID(e.ID), truncate(e.Title, 44), orDash(deref(e.VersionLabel)), cutDate(e.PublishedAt),
				})
			}
			A.out.Table([]string{"ID", "Title", "Version", "Published"}, rows)
			if resp.NextCursor != nil {
				A.out.Printf("More entries available. Next page: --cursor %s", *resp.NextCursor)
			}
			return nil
		},
	}
	publicChangelog.Flags().IntVar(&limit, "limit", 100, "Entries per page (1-100, server default 100)")
	publicChangelog.Flags().StringVar(&cursor, "cursor", "", "Opaque keyset cursor from the previous page's nextCursor")
	return publicChangelog
}

// newAppsPublicConfigCmd shows the PublicAppConfig served to the public web
// portal and SDKs. The endpoints are unauthenticated, so this works before
// 'auth login'. Private apps fail closed with the same 404 as unknown app
// keys (SEC-37), so a 404 here means "not found or not public".
func newAppsPublicConfigCmd() *cobra.Command {
	var workspaceSlug, appSlug string
	publicConfig := &cobra.Command{
		Use:   "public-config [app-key]",
		Short: "Show an app's public portal config (no login required)",
		Long: `Show the PublicAppConfig served to the public web portal and SDKs.

Resolve the app by its public app key, or by workspace and app slugs:
  cupthread apps public-config <app-key>
  cupthread apps public-config --workspace-slug <slug> --app-slug <slug>

Private apps fail closed: both routes return 404 {"error": "App not found"},
identical to an unknown app key. Only public apps return a 200 body.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			switch {
			case len(args) == 1:
				if workspaceSlug != "" || appSlug != "" {
					return errors.New("pass either an app key or --workspace-slug/--app-slug, not both")
				}
				path = "/api/v1/public/config/" + url.PathEscape(args[0])
			case workspaceSlug != "" && appSlug != "":
				path = fmt.Sprintf("/api/v1/public/workspaces/%s/apps/%s/config",
					url.PathEscape(workspaceSlug), url.PathEscape(appSlug))
			default:
				return errors.New("pass an app key, or both --workspace-slug and --app-slug")
			}
			var config api.PublicAppConfig
			if err := api.New(A.baseURL()).Do(cmd.Context(), "GET", path, nil, nil, &config); err != nil {
				var apiErr *api.APIError
				if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
					return fmt.Errorf("app not found or not public: %w", err)
				}
				return err
			}
			if A.structured() {
				return A.out.Structured(config)
			}
			A.out.Table([]string{"Field", "Value"}, [][]string{
				{"App ID", config.AppID},
				{"App key", config.AppKey},
				{"Workspace", config.WorkspaceSlug},
				{"Slug", config.Slug},
				{"Name", config.Name},
				{"Public", boolYesNo(config.AllowPublic)},
				{"Platforms", strings.Join(config.AllowedPlatforms, ", ")},
				{"Website URL", orDash(deref(config.WebsiteURL))},
				{"Hide site branding", boolYesNo(config.HideSiteBranding)},
				{"Store URL", orDash(deref(config.StoreURL))},
				{"App Store URL", orDash(deref(config.AppStoreURL))},
				{"Google Play URL", orDash(deref(config.GooglePlayURL))},
				{"Icon", orDash(deref(config.IconURL))},
				{"Max attachment bytes", strconv.Itoa(config.MaxAttachmentBytes)},
				{"Anonymous roadmap", boolYesNo(config.AllowAnonymousRoadmap)},
				{"Anonymous vote", boolYesNo(config.AllowAnonymousVote)},
				{"Anonymous feedback", boolYesNo(config.AllowAnonymousFeedback)},
				{"Anonymous changelog", boolYesNo(config.AllowAnonymousChangelog)},
			})
			return nil
		},
	}
	publicConfig.Flags().StringVar(&workspaceSlug, "workspace-slug", "", "Workspace slug (resolve the config by slugs)")
	publicConfig.Flags().StringVar(&appSlug, "app-slug", "", "App slug (requires --workspace-slug)")
	return publicConfig
}

func newAppsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List apps in the workspace",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.ListAppsResponse
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/apps"), nil, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			rows := make([][]string, 0, len(resp.Apps))
			for _, appRec := range resp.Apps {
				defaultMark := ""
				if prefs, ok := A.cfg.Workspaces[ws]; ok && prefs.DefaultApp == appRec.AppID {
					defaultMark = " *"
				}
				rows = append(rows, []string{
					appRec.AppID,
					appRec.Name + defaultMark,
					appRec.Slug,
					appRec.AppKey,
					boolYesNo(appRec.AllowPublic),
					strings.Join(appRec.AllowedPlatforms, ","),
				})
			}
			A.out.Table([]string{"ID", "Name", "Slug", "App Key", "Public", "Platforms"}, rows)
			return nil
		},
	}
}

func boolYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func newAppsCreateCmd() *cobra.Command {
	var name, storeURL string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a new app in the workspace",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return errors.New("--name is required")
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]any{"name": name, "workspaceId": ws}
			if storeURL != "" {
				body["storeUrl"] = storeURL
			}
			var appRec api.AppRecord
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/apps"), nil, body, &appRec); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(appRec)
			}
			A.out.Printf("✓ Created app %s (%s)", appRec.Name, appRec.AppID)
			A.out.Printf("  App key: %s", appRec.AppKey)
			A.out.Printf("  Make it the default with: cupthread apps use %s", appRec.AppID)
			return nil
		},
	}
	create.Flags().StringVar(&name, "name", "", "App name (required)")
	create.Flags().StringVar(&storeURL, "store-url", "", "App Store or Google Play URL (metadata/icon are fetched from it)")
	return create
}

func newAppsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <app-id-or-slug>",
		Short: "Show one app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appRec, err := A.lookupApp(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(appRec)
			}
			printApp(appRec)
			return nil
		},
	}
}

func printApp(appRec *api.AppRecord) {
	A.out.Table([]string{"Field", "Value"}, [][]string{
		{"ID", appRec.AppID},
		{"Name", appRec.Name},
		{"Slug", appRec.Slug},
		{"App key", appRec.AppKey},
		{"Public", boolYesNo(appRec.AllowPublic)},
		{"Platforms", strings.Join(appRec.AllowedPlatforms, ", ")},
		{"Store URL", orDash(deref(appRec.StoreURL))},
		{"App Store URL", orDash(deref(appRec.AppStoreURL))},
		{"Google Play URL", orDash(deref(appRec.GooglePlayURL))},
		{"Icon", orDash(deref(appRec.IconURL))},
		{"GitHub", fmt.Sprintf("%s/%s", orDash(deref(appRec.GithubOwner)), orDash(deref(appRec.GithubRepo)))},
		{"Created", appRec.CreatedAt},
		{"Updated", appRec.UpdatedAt},
	})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func newAppsUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <app-id-or-slug>",
		Short: "Set the default app for the current workspace",
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
			A.cfg.WorkspacePrefsFor(ws).DefaultApp = appRec.AppID
			if err := A.saveConfig(); err != nil {
				return err
			}
			A.out.Printf("✓ Default app for %s: %s (%s)", ws, appRec.Name, appRec.AppID)
			return nil
		},
	}
}

func newAppsUpdateCmd() *cobra.Command {
	var (
		name, slug           string
		storeURL, appStoreURL, googlePlayURL string
		iconPath             string
		public               bool
		platforms            []string
	)
	update := &cobra.Command{
		Use:   "update <app-id-or-slug>",
		Short: "Update an app's profile",
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

			var iconRec *api.AppRecord
			body := map[string]any{}
			if cmd.Flags().Changed("name") {
				body["name"] = name
			}
			if cmd.Flags().Changed("slug") {
				body["slug"] = slug
			}
			if cmd.Flags().Changed("store-url") {
				body["storeUrl"] = nilIfEmpty(storeURL)
			}
			if cmd.Flags().Changed("app-store-url") {
				body["appStoreUrl"] = nilIfEmpty(appStoreURL)
			}
			if cmd.Flags().Changed("google-play-url") {
				body["googlePlayUrl"] = nilIfEmpty(googlePlayURL)
			}
			if cmd.Flags().Changed("public") {
				body["allowPublic"] = public
			}
			if cmd.Flags().Changed("platforms") {
				body["allowedPlatforms"] = platforms
			}
			if cmd.Flags().Changed("icon") && iconPath != "" {
				// The console icon endpoint validates the image and updates
				// the app record in the same request, so no iconUrl PUT here.
				iconRec, err = uploadIcon(cmd.Context(), ws, appRec.AppID, iconPath)
				if err != nil {
					return err
				}
			}
			if cmd.Flags().Changed("icon") && iconPath == "" {
				body["iconUrl"] = nil
			}
			if len(body) == 0 && iconRec == nil {
				return errors.New("nothing to update: pass at least one flag")
			}

			updated := iconRec
			if len(body) > 0 {
				var rec api.AppRecord
				if err := A.client.Do(cmd.Context(), "PUT",
					fmt.Sprintf("%s/apps/%s", wsPath(ws, ""), appRec.AppID), nil, body, &rec); err != nil {
					return err
				}
				updated = &rec
			}
			if A.structured() {
				return A.out.Structured(*updated)
			}
			A.out.Printf("✓ Updated app %s", updated.AppID)
			if iconRec != nil {
				A.out.Printf("  Icon: %s", deref(updated.IconURL))
			}
			return nil
		},
	}
	update.Flags().StringVar(&name, "name", "", "New app name")
	update.Flags().StringVar(&slug, "slug", "", "New slug ([a-z0-9-])")
	update.Flags().StringVar(&storeURL, "store-url", "", "Legacy store URL (\"\" clears it)")
	update.Flags().StringVar(&appStoreURL, "app-store-url", "", "App Store URL (\"\" clears it)")
	update.Flags().StringVar(&googlePlayURL, "google-play-url", "", "Google Play URL (\"\" clears it)")
	update.Flags().StringVar(&iconPath, "icon", "", "Path to an image file to upload as the app icon (PNG, JPEG, WebP, GIF, or screened SVG; a declared type that does not match the file content fails with 415)")
	update.Flags().BoolVar(&public, "public", false, "Show the app on the public showcase (Pro feature)")
	update.Flags().StringSliceVar(&platforms, "platforms", nil, "Allowed platforms: ios,macos,android,universal")
	return update
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func uploadIcon(ctx context.Context, wsID, appID, path string) (*api.AppRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read icon file: %w", err)
	}
	return A.client.UploadAppIcon(ctx, wsID, appID, fileName(path), data)
}

func fileName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func newAppSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "settings", Short: "Show or update per-app settings (anonymous access, SDK appearance)"}

	var inputPath string
	show := &cobra.Command{
		Use:   "show [app-id-or-slug]",
		Short: "Show app settings",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var settings *api.AppSettings
			if len(args) == 1 {
				appRec, err := A.lookupApp(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				var resp api.WorkspaceSettingsResponse
				if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/settings"), nil, nil, &resp); err != nil {
					return err
				}
				for i := range resp.Apps {
					if resp.Apps[i].AppID == appRec.AppID {
						settings = resp.Apps[i].Settings
					}
				}
			} else {
				var resp api.WorkspaceSettingsResponse
				if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/settings"), nil, nil, &resp); err != nil {
					return err
				}
				if A.structured() {
					return A.out.Structured(resp)
				}
				for _, entry := range resp.Apps {
					A.out.Printf("App: %s (%s)", entry.Name, entry.AppID)
					printSettings(entry.Settings)
					A.out.Printf("")
				}
				return nil
			}
			if A.structured() {
				if settings == nil {
					return A.out.Structured(map[string]any{"settings": nil})
				}
				return A.out.Structured(settings)
			}
			printSettings(settings)
			return nil
		},
	}
	cmd.AddCommand(show)

	set := &cobra.Command{
		Use:   "set <app-id-or-slug>",
		Short: "Update app settings (flags, or --input for the raw JSON body)",
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

			var body map[string]any
			if inputPath != "" {
				data, err := readInputFile(inputPath)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(data, &body); err != nil {
					return fmt.Errorf("parse settings JSON: %w", err)
				}
			} else {
				body = map[string]any{}
			}

			for flag, key := range map[string]string{
				"anon-roadmap":   "allowAnonymousRoadmap",
				"anon-vote":      "allowAnonymousVote",
				"anon-feedback":  "allowAnonymousFeedback",
				"anon-changelog": "allowAnonymousChangelog",
			} {
				if cmd.Flags().Changed(flag) {
					v, _ := cmd.Flags().GetBool(flag)
					body[key] = v
				}
			}
			if len(body) == 0 {
				return errors.New("nothing to set: pass flags or --input @file")
			}

			var updated api.AppSettings
			if err := A.client.Do(cmd.Context(), "PUT",
				fmt.Sprintf("%s/apps/%s/settings", wsPath(ws, ""), appRec.AppID), nil, body, &updated); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(updated)
			}
			A.out.Printf("✓ Settings updated for %s", appRec.AppID)
			return nil
		},
	}
	set.Flags().Bool("anon-roadmap", true, "Allow anonymous roadmap viewing")
	set.Flags().Bool("anon-vote", true, "Allow anonymous voting")
	set.Flags().Bool("anon-feedback", true, "Allow anonymous feedback")
	set.Flags().Bool("anon-changelog", true, "Allow anonymous changelog viewing")
	set.Flags().StringVar(&inputPath, "input", "", "JSON file (or @- for stdin) with the raw update body, e.g. {\"sdk\":{\"theme\":\"dark\"}}")
	cmd.AddCommand(set)
	return cmd
}

func printSettings(s *api.AppSettings) {
	if s == nil {
		A.out.Printf("  Settings: none (defaults apply)")
		return
	}
	A.out.Table([]string{"Field", "Value"}, [][]string{
		{"Anonymous roadmap", boolYesNo(s.AllowAnonymousRoadmap)},
		{"Anonymous vote", boolYesNo(s.AllowAnonymousVote)},
		{"Anonymous feedback", boolYesNo(s.AllowAnonymousFeedback)},
		{"Anonymous changelog", boolYesNo(s.AllowAnonymousChangelog)},
		{"SDK appearance", string(s.SDK)},
	})
}
