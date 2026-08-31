package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	)
	return cmd
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
			if flagJSON {
				return A.out.JSON(resp)
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
			if flagJSON {
				return A.out.JSON(appRec)
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
			if flagJSON {
				return A.out.JSON(appRec)
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
			if cmd.Flags().Changed("icon") {
				if iconPath == "" {
					body["iconUrl"] = nil
				} else {
					iconURL, err := uploadIcon(cmd.Context(), appRec.AppKey, iconPath)
					if err != nil {
						return err
					}
					body["iconUrl"] = iconURL
				}
			}
			if len(body) == 0 {
				return errors.New("nothing to update: pass at least one flag")
			}

			var updated api.AppRecord
			if err := A.client.Do(cmd.Context(), "PUT",
				fmt.Sprintf("%s/apps/%s", wsPath(ws, ""), appRec.AppID), nil, body, &updated); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(updated)
			}
			A.out.Printf("✓ Updated app %s", updated.AppID)
			return nil
		},
	}
	update.Flags().StringVar(&name, "name", "", "New app name")
	update.Flags().StringVar(&slug, "slug", "", "New slug ([a-z0-9-])")
	update.Flags().StringVar(&storeURL, "store-url", "", "Legacy store URL (\"\" clears it)")
	update.Flags().StringVar(&appStoreURL, "app-store-url", "", "App Store URL (\"\" clears it)")
	update.Flags().StringVar(&googlePlayURL, "google-play-url", "", "Google Play URL (\"\" clears it)")
	update.Flags().StringVar(&iconPath, "icon", "", "Path to an image file to upload as the app icon")
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

func uploadIcon(ctx context.Context, appKey, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read icon file: %w", err)
	}
	return A.client.UploadAppIcon(ctx, appKey, fileName(path), data)
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
				if flagJSON {
					return A.out.JSON(resp)
				}
				for _, entry := range resp.Apps {
					A.out.Printf("App: %s (%s)", entry.Name, entry.AppID)
					printSettings(entry.Settings)
					A.out.Printf("")
				}
				return nil
			}
			if flagJSON {
				if settings == nil {
					return A.out.JSON(map[string]any{"settings": nil})
				}
				return A.out.JSON(settings)
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
			if flagJSON {
				return A.out.JSON(updated)
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
