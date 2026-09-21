package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
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
		newAppsPublicFeatureRequestsCmd(),
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

// newAppsPublicFeatureRequestsCmd pages the unauthenticated public
// feature-request feed (DATA-01). Keyset cursor paging supersedes offset
// paging: when --cursor is set the offset is not sent at all, so deep-page
// walks stay cheap server-side.
func newAppsPublicFeatureRequestsCmd() *cobra.Command {
	var limit, offset int
	var cursor, query string
	publicRequests := &cobra.Command{
		Use:   "public-feature-requests <app-key>",
		Short: "Fetch an app's public feature-request feed (no login required)",
		Long: `Fetch the feature-request feed served to the public web portal and SDKs.

Keyset-cursor-paginated: pass --cursor with the nextCursor value from the
previous page until the response reports hasMore=false. Requests are ordered
newest-first and --offset is ignored whenever --cursor is set:
  cupthread apps public-feature-requests <app-key>
  cupthread apps public-feature-requests <app-key> --limit 100
  cupthread apps public-feature-requests <app-key> --cursor <nextCursor>
  cupthread apps public-feature-requests <app-key> --q "dark mode"

Boards that require sign-in (anonymous roadmap view disabled) reject the
unauthenticated feed with 401.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{"appKey": {args[0]}, "limit": {strconv.Itoa(limit)}}
			if cursor != "" {
				q.Set("cursor", cursor)
			} else if offset > 0 {
				q.Set("offset", strconv.Itoa(offset))
			}
			if query != "" {
				q.Set("q", query)
			}
			var resp api.ListPublicFeatureRequestsResponse
			if err := api.New(A.baseURL()).Do(cmd.Context(), "GET", "/api/v1/feature-requests", q, nil, &resp); err != nil {
				var apiErr *api.APIError
				if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
					return fmt.Errorf("this board requires sign-in; the CLI reads the public feed unauthenticated: %w", err)
				}
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			rows := make([][]string, 0, len(resp.Requests))
			for _, r := range resp.Requests {
				rows = append(rows, []string{
					shortID(r.ID), truncate(r.Title, 44), r.Status, orDash(deref(r.ColumnName)),
					strconv.Itoa(r.VoteCount), strconv.Itoa(r.CommentCount), cutDate(r.CreatedAt),
				})
			}
			A.out.Table([]string{"ID", "Title", "Status", "Column", "Votes", "Comments", "Created"}, rows)
			A.out.Printf("(%d shown, %d total)", len(resp.Requests), resp.Total)
			if resp.NextCursor != nil {
				A.out.Printf("More requests available. Next page: --cursor %s", *resp.NextCursor)
			}
			return nil
		},
	}
	publicRequests.Flags().IntVar(&limit, "limit", 50, "Requests per page (1-200, server default 50)")
	publicRequests.Flags().IntVar(&offset, "offset", 0, "Legacy offset page start (ignored when --cursor is set)")
	publicRequests.Flags().StringVar(&cursor, "cursor", "", "Opaque keyset cursor from the previous page's nextCursor")
	publicRequests.Flags().StringVar(&query, "q", "", "Search titles, descriptions, columns, and versions")
	return publicRequests
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
			if A.structured() {
				return A.out.Structured(appUseResult{
					Workspace:  ws,
					DefaultApp: appUseAppRef{AppID: appRec.AppID, Name: appRec.Name, Slug: appRec.Slug},
				})
			}
			A.out.Printf("✓ Default app for %s: %s (%s)", ws, appRec.Name, appRec.AppID)
			return nil
		},
	}
}

// appUseResult is the machine-readable payload of 'apps use', carrying the
// resolved record so a caller that passed a slug learns its app ID.
type appUseResult struct {
	Workspace  string       `json:"workspace"`
	DefaultApp appUseAppRef `json:"defaultApp"`
}

// appUseAppRef names the resolved app without its profile fields.
type appUseAppRef struct {
	AppID string `json:"appId"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
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
		Long: `Update an app's profile with the given flags.

Field flags (--name, --slug, --store-url, --app-store-url, --google-play-url,
--public, --platforms) are validated locally against the server's rules and
applied with one PUT before the icon is uploaded, so a rejected field update
commits nothing. The icon file itself is checked before any request is sent:
a missing path or a file over the 10 MB server-side image cap fails the
command up front instead of after the metadata PUT has applied. If the
metadata PUT succeeds but the icon upload still fails, the command reports
"partially applied" — in --json/--yaml mode the error payload carries
"applied" and "failed" lists so scripts can tell what went live.
Clear a URL or the icon by passing an empty value (e.g. --icon "").`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Issue #91: mirror the server's field rules locally so bad
			// flag values fail before any request — the app lookup
			// included — can commit anything.
			if err := validateAppsUpdateFlags(cmd, name, slug, storeURL, appStoreURL, googlePlayURL, platforms, iconPath); err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			appRec, err := A.lookupApp(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			iconUpload := cmd.Flags().Changed("icon") && iconPath != ""
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
			if cmd.Flags().Changed("icon") && iconPath == "" {
				body["iconUrl"] = nil
			}
			if len(body) == 0 && !iconUpload {
				return errors.New("nothing to update: pass at least one flag")
			}

			// Issue #91: the metadata PUT runs before the icon upload. A
			// rejected field update then commits nothing (the icon was
			// never sent), so the only possible partial state is "fields
			// applied, icon failed" — disclosed by reportPartialUpdate.
			var updated *api.AppRecord
			var applied []string
			if len(body) > 0 {
				var rec api.AppRecord
				if err := A.client.Do(cmd.Context(), "PUT",
					fmt.Sprintf("%s/apps/%s", wsPath(ws, ""), appRec.AppID), nil, body, &rec); err != nil {
					return err
				}
				updated = &rec
				applied = append(applied, "metadata")
			}
			var iconRec *api.AppRecord
			if iconUpload {
				// The console icon endpoint validates the image and updates
				// the app record in the same request, so no iconUrl PUT here.
				iconRec, err = uploadIcon(cmd.Context(), ws, appRec.AppID, iconPath)
				if err != nil {
					if updated != nil {
						return A.reportPartialUpdate(applied, "icon", err)
					}
					return err
				}
				applied = append(applied, "icon")
				if updated == nil {
					updated = iconRec
				}
			}
			if A.structured() {
				return A.out.Structured(*updated)
			}
			A.out.Printf("✓ Updated app %s", updated.AppID)
			if iconRec != nil {
				A.out.Printf("  Icon: %s", deref(iconRec.IconURL))
			}
			return nil
		},
	}
	update.Flags().StringVar(&name, "name", "", "New app name")
	update.Flags().StringVar(&slug, "slug", "", "New slug ([a-z0-9-])")
	update.Flags().StringVar(&storeURL, "store-url", "", "Legacy store URL (\"\" clears it)")
	update.Flags().StringVar(&appStoreURL, "app-store-url", "", "App Store URL (\"\" clears it)")
	update.Flags().StringVar(&googlePlayURL, "google-play-url", "", "Google Play URL (\"\" clears it)")
	update.Flags().StringVar(&iconPath, "icon", "", "Path to an image file to upload as the app icon (PNG, JPEG, WebP, GIF, or screened SVG; a declared type that does not match the file content fails with 415; files over 10 MB are rejected before upload)")
	update.Flags().BoolVar(&public, "public", false, "Show the app on the public showcase (Pro feature)")
	update.Flags().StringSliceVar(&platforms, "platforms", nil, "Allowed platforms: ios,macos,android,universal")
	return update
}

// appSlugRE mirrors the server's slug rule for apps (UpdateWorkspaceAppInputSchema).
var appSlugRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// appPlatformSet mirrors the server's PlatformSchema enum.
var appPlatformSet = map[string]bool{"ios": true, "macos": true, "android": true, "universal": true}

// validateAppsUpdateFlags checks flag values against the server's zod rules
// (UpdateWorkspaceAppInputSchema: name 2-120, slug 2-64 [a-z0-9-]+, absolute
// URLs, 1-4 platforms) so a rejected update fails before any request is sent.
// Clearing a URL with "" is allowed (sent as null); only values meant to be
// set are checked.
// maxIconBytes mirrors the server-side app-icon cap (SEC-36): the console
// icon route reads the multipart body through an 11 MB bounded reader and
// Cloudflare Images stores at most 10 MB per image, so a larger file can
// never succeed and is rejected before any request is sent.
const maxIconBytes = 10 << 20

func validateAppsUpdateFlags(cmd *cobra.Command, name, slug, storeURL, appStoreURL, googlePlayURL string, platforms []string, iconPath string) error {
	if cmd.Flags().Changed("name") && (len(name) < 2 || len(name) > 120) {
		return fmt.Errorf("invalid --name: must be 2-120 characters, got %d", len(name))
	}
	if cmd.Flags().Changed("slug") {
		if len(slug) < 2 || len(slug) > 64 {
			return fmt.Errorf("invalid --slug: must be 2-64 characters, got %d", len(slug))
		}
		if !appSlugRE.MatchString(slug) {
			return fmt.Errorf("invalid --slug %q: may only contain a-z, 0-9 and -", slug)
		}
	}
	for _, f := range []struct{ flag, val string }{
		{"store-url", storeURL},
		{"app-store-url", appStoreURL},
		{"google-play-url", googlePlayURL},
	} {
		if cmd.Flags().Changed(f.flag) && f.val != "" && !validAbsoluteURL(f.val) {
			return fmt.Errorf("invalid --%s %q: must be an absolute URL (e.g. https://example.com/app)", f.flag, f.val)
		}
	}
	if cmd.Flags().Changed("platforms") {
		if len(platforms) < 1 || len(platforms) > 4 {
			return fmt.Errorf("invalid --platforms: pass 1-4 of ios, macos, android, universal")
		}
		for _, p := range platforms {
			if !appPlatformSet[p] {
				return fmt.Errorf("invalid --platforms value %q: must be one of ios, macos, android, universal", p)
			}
		}
	}
	if cmd.Flags().Changed("icon") && iconPath != "" {
		info, err := os.Stat(iconPath)
		if err != nil {
			return fmt.Errorf("invalid --icon: %w", err)
		}
		if info.IsDir() {
			return fmt.Errorf("invalid --icon %q: is a directory", iconPath)
		}
		if info.Size() > maxIconBytes {
			return fmt.Errorf("invalid --icon %q: is %d bytes; the app-icon limit is %d MB (the server rejects larger uploads with 413 payload_too_large)", iconPath, info.Size(), maxIconBytes>>20)
		}
	}
	return nil
}

// validAbsoluteURL reports whether v parses as an absolute URL with a host,
// the client-side approximation of zod's z.string().url() for store URLs.
func validAbsoluteURL(v string) bool {
	u, err := url.Parse(v)
	return err == nil && u.Scheme != "" && u.Host != ""
}

// partialUpdateError reports an apps update where earlier steps already
// committed server-side while a later step failed (issue #91).
type partialUpdateError struct {
	applied []string
	failed  string
	cause   error
}

func (e *partialUpdateError) Error() string {
	return fmt.Sprintf("apps update partially applied: %s already applied, but %s failed: %v",
		strings.Join(e.applied, " and "), e.failed, e.cause)
}

func (e *partialUpdateError) Unwrap() error { return e.cause }

// reportPartialUpdate fails the command while disclosing what already went
// live: the returned error names the applied steps (human mode), and in
// --json/--yaml mode a structured payload with applied/failed is printed so
// scripts and agents can react programmatically.
func (a *app) reportPartialUpdate(applied []string, failed string, cause error) error {
	err := &partialUpdateError{applied: applied, failed: failed, cause: cause}
	if a.structured() {
		_ = a.out.Structured(map[string]any{
			"error":   err.Error(),
			"applied": applied,
			"failed":  failed,
		})
	}
	return err
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

			// json.RawMessage values keep every number in the --input body
			// byte-identical on the wire (issue #75); a map[string]any decode
			// would round-trip them through float64 and silently rewrite
			// integers a float64 cannot represent exactly.
			body := map[string]json.RawMessage{}
			if inputPath != "" {
				data, err := readInputFile(inputPath)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(data, &body); err != nil {
					return fmt.Errorf("parse settings JSON: %w", err)
				}
			}

			for flag, key := range map[string]string{
				"anon-roadmap":   "allowAnonymousRoadmap",
				"anon-vote":      "allowAnonymousVote",
				"anon-feedback":  "allowAnonymousFeedback",
				"anon-changelog": "allowAnonymousChangelog",
			} {
				if cmd.Flags().Changed(flag) {
					v, _ := cmd.Flags().GetBool(flag)
					encoded, err := json.Marshal(v)
					if err != nil {
						return err
					}
					body[key] = encoded
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
	set.Flags().StringVar(&inputPath, "input", "", "JSON file (or @- for stdin) with the raw update body, sent verbatim (numbers keep full precision), e.g. {\"sdk\":{\"theme\":\"dark\"}}")
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
