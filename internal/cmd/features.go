package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

func newFeaturesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "features",
		Aliases: []string{"feature-requests"},
		Short:   "Manage feature requests",
	}
	cmd.AddCommand(
		newFeaturesListCmd(),
		newFeaturesGetCmd(),
		newFeaturesCreateCmd(),
		newFeaturesUpdateCmd(),
		newFeaturesApproveCmd(),
		newFeaturesDeleteCmd(),
		newFeaturesForwardCmd(),
	)
	return cmd
}

// listFeatureRequests fetches an admin feature request page for the app.
func listFeatureRequests(ctx context.Context, ws, appID string, limit, offset int, sort string, payerOnly bool) (*api.AdminListFeatureRequestsResponse, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	if appID != "" {
		q.Set("appId", appID)
	}
	if sort != "" && sort != "newest" {
		q.Set("sort", sort)
	}
	if payerOnly {
		q.Set("payer", "only")
	}
	var resp api.AdminListFeatureRequestsResponse
	if err := A.client.Do(ctx, "GET", wsPath(ws, "/feature-requests"), q, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func newFeaturesListCmd() *cobra.Command {
	var limit, offset int
	var sort string
	var payerOnly bool
	var allApps bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List feature requests",
		Long: `List feature requests.

The listing is scoped to the resolved app (--app flag, else the saved
default from 'apps use'), so every ID it shows resolves in the ID-taking
commands (get/update/approve/delete/forward). Pass --all-apps for the
workspace-wide cross-app view instead.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The server coerces any sort value other than the exact string
			// "revenue" to newest, so a typo like --sort revenuee would
			// silently return newest-ordered rows — and skip the Pro gate's
			// 402, which only fires for sort=revenue. Fail locally naming the
			// value instead (issue #405, matching the --triage-status guard).
			if sort != "newest" && sort != "revenue" {
				return fmt.Errorf("invalid --sort %q: use newest or revenue", sort)
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if allApps && flagApp != "" {
				return errors.New("the --all-apps flag cannot be combined with --app")
			}
			// The server clamps the page size to 200 silently; clamp here
			// too and announce the rewrite so an offset-stepping walk
			// advances by the page size the server actually serves
			// (issue #337, matching the sibling listings from #201).
			limit = A.clampListLimit(limit, featureRequestPageSize)
			// The console listing clamps the offset at 10,000 and still
			// answers 200; announce the rewrite so a deep walk notices it is
			// re-reading the final page (issue #178).
			offset = A.clampListOffset(offset)
			appID := ""
			if !allApps {
				// Scope the listing to the resolved app like the ID-taking
				// commands do, so list → get round-trips (issue #187).
				appID = A.optionalAppID()
			}
			resp, err := listFeatureRequests(cmd.Context(), ws, appID, limit, offset, sort, payerOnly)
			if err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Table([]string{"ID", "App", "Title", "Column", "Version", "Votes", "Approved", "Revenue", "Created"}, featureRows(resp.Requests))
			A.out.Printf("(%d shown, %d total)", len(resp.Requests), resp.Total)
			// Always printed (even at 0) so the line's presence is a stable
			// part of the output shape; it is the Console triage backlog
			// badge's count (QUAL-03).
			A.out.Printf("(%d of %d unassigned)", resp.UnassignedTotal, resp.Total)
			return nil
		},
	}
	list.Flags().IntVar(&limit, "limit", 50, "Maximum requests to list (1-200, server page cap 200)")
	list.Flags().IntVar(&offset, "offset", 0, "Offset for pagination (values above 10000 are clamped to 10000 by the server)")
	list.Flags().StringVar(&sort, "sort", "newest", "Sort order: newest or revenue (revenue is Pro-gated)")
	list.Flags().BoolVar(&payerOnly, "payer-only", false, "Only requests from paying users (Pro-gated)")
	list.Flags().BoolVar(&allApps, "all-apps", false, "List requests from every app in the workspace (ignore the resolved app)")
	_ = list.RegisterFlagCompletionFunc("sort", cobra.FixedCompletions([]string{"newest", "revenue"}, cobra.ShellCompDirectiveNoFileComp))
	return list
}

func featureRows(reqs []api.AdminFeatureRequest) [][]string {
	rows := make([][]string, 0, len(reqs))
	for _, r := range reqs {
		id := r.ID
		if len(id) > 12 {
			id = id[:12]
		}
		revenue := ""
		if r.RevenueTotal > 0 {
			revenue = fmt.Sprintf("%.0f", r.RevenueTotal)
		}
		rows = append(rows, []string{
			id,
			orDash(r.AppID),
			truncate(r.Title, 44),
			orDash(deref(r.ColumnName)),
			orDash(deref(r.VersionLabel)),
			strconv.Itoa(r.VoteCount),
			boolYesNo(r.Approved),
			revenue,
			cutDate(r.CreatedAt),
		})
	}
	return rows
}

// featureRequestPageSize is the page size used while resolving a single
// request ID; the console listing endpoint clamps limit to 200.
const featureRequestPageSize = 200

// maxFeatureRequestPages caps ID-resolution paging (50 pages ≈ 10k requests)
// so a server reporting a bogus total cannot keep the client paging forever.
const maxFeatureRequestPages = 50

// fetchOneFeatureRequest finds a request by exact ID or ID prefix.
//
// The console API has no single-item endpoint, so resolution pages through
// the workspace feature-request listing (newest first) until the reference
// resolves or the listing is exhausted — any request in the workspace is
// addressable, not just the newest page. Matching, ambiguity, and the
// page-cap guard live in the shared resolveIDRef (issue #413).
//
// The lookup is scoped to the resolved app (--app flag, else the saved
// default from 'apps use') so ID resolution cannot cross into another app's
// requests; with no app resolved it stays workspace-wide. The not-found
// error names the app the scan actually covered and points at --app as the
// escape hatch, so an ID copied from the wrong scope is diagnosable.
func fetchOneFeatureRequest(ctx context.Context, ref string) (*api.AdminFeatureRequest, error) {
	ws, err := workspaceClient(ctx)
	if err != nil {
		return nil, err
	}
	appID := A.optionalAppID()
	return resolveIDRef(ref, idRefNouns{what: "feature request", plural: "requests", listHint: "cupthread features list"}, maxFeatureRequestPages, true,
		func(page int) ([]api.AdminFeatureRequest, bool, error) {
			offset := page * featureRequestPageSize
			resp, err := listFeatureRequests(ctx, ws, appID, featureRequestPageSize, offset, "", false)
			if err != nil {
				return nil, false, err
			}
			return resp.Requests, len(resp.Requests) < featureRequestPageSize || offset+len(resp.Requests) >= resp.Total, nil
		},
		featureRequestNotFound(ws, appID),
		func(r *api.AdminFeatureRequest) string { return r.ID },
	)
}

// featureRequestNotFound keeps the #187 error contract for scans that find
// nothing: under a resolved app it names the app the scan actually covered
// and the --app escape hatch instead of mislabeling the scan workspace-wide.
func featureRequestNotFound(ws, appID string) func(ref string, scanned int, exhausted bool, prefixMatches int) error {
	return func(ref string, scanned int, exhausted bool, prefixMatches int) error {
		if appID != "" {
			if !exhausted {
				return fmt.Errorf("feature request %q not found in the first %d requests of app %s (resolution scans at most %d pages); pass --app <id> to search another app", ref, scanned, appID, maxFeatureRequestPages)
			}
			return fmt.Errorf("feature request %q not found among the %d requests of app %s in workspace %s (resolution is scoped to the resolved app: pass --app <id> to search another app, or run 'cupthread apps use' to change the default)", ref, scanned, appID, ws)
		}
		if !exhausted {
			return fmt.Errorf("feature request %q not found in the first %d requests (resolution scans at most %d pages); narrow the search with --app", ref, scanned, maxFeatureRequestPages)
		}
		return fmt.Errorf("feature request %q not found (scanned %d requests in workspace %s)", ref, scanned, ws)
	}
}

func newFeaturesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <request-id>",
		Short: "Show one feature request",
		Args:  exactNonBlankArg("feature request reference"),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := fetchOneFeatureRequest(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(r)
			}
			A.out.Table([]string{"Field", "Value"}, [][]string{
				{"ID", r.ID},
				{"Title", r.Title},
				{"Description", truncate(r.Description, 400)},
				{"Status", r.Status},
				{"Column", orDash(deref(r.ColumnName))},
				{"Version", orDash(deref(r.VersionLabel))},
				{"Approved", boolYesNo(r.Approved)},
				{"Votes", strconv.Itoa(r.VoteCount)},
				{"Requester", orDash(deref(r.RequesterName))},
				{"Email", orDash(deref(r.RequesterEmail))},
				{"Avatar", orDash(deref(r.RequesterAvatarUrl))},
				{"Clerk ID", orDash(deref(r.RequesterClerkId))},
				{"Revenue", fmt.Sprintf("%.0f", r.RevenueTotal)},
				{"Paying voters", strconv.Itoa(r.PayingVoters)},
				{"GitHub discussion", orDash(deref(r.GithubDiscussionURL))},
				{"Created", r.CreatedAt},
			})
			return nil
		},
	}
}

// validateFeaturesCreateFlags mirrors CreateFeatureRequestAdminInputSchema's
// zod intake bounds (title 3-160, description 5-5000, columnSlug 1-60 code
// points) so an out-of-bounds create fails locally naming the flag instead
// of as a guaranteed 400 Validation failed (issue #341, following
// validateAppsUpdateFlags from #336).
func validateFeaturesCreateFlags(title, description, columnSlug string) error {
	if n := utf8.RuneCountInString(title); n < 3 || n > 160 {
		return fmt.Errorf("invalid --title: must be 3-160 characters, got %d", n)
	}
	if n := utf8.RuneCountInString(description); n < 5 || n > 5000 {
		return fmt.Errorf("invalid --description: must be 5-5000 characters, got %d", n)
	}
	if columnSlug != "" {
		if n := utf8.RuneCountInString(columnSlug); n > 60 {
			return fmt.Errorf("invalid --column-slug: must be 1-60 characters, got %d", n)
		}
	}
	return nil
}

// validateFeaturesUpdateFlags mirrors UpdateFeatureRequestAdminInputSchema's
// zod intake bounds so a rejected update fails before the ID-resolution
// round trips. A Changed flag is checked even when empty: title/description
// below their minimum and columnSlug "" (the schema requires 1-60 characters
// and is not nullable, so there is no clear-the-column spelling) can never
// be accepted by the server and are rejected locally (issue #341).
func validateFeaturesUpdateFlags(cmd *cobra.Command, title, description, columnSlug string) error {
	if cmd.Flags().Changed("title") {
		if n := utf8.RuneCountInString(title); n < 3 || n > 160 {
			return fmt.Errorf("invalid --title: must be 3-160 characters, got %d", n)
		}
	}
	if cmd.Flags().Changed("description") {
		if n := utf8.RuneCountInString(description); n < 5 || n > 5000 {
			return fmt.Errorf("invalid --description: must be 5-5000 characters, got %d", n)
		}
	}
	if cmd.Flags().Changed("column-slug") {
		if columnSlug == "" {
			return errors.New(`invalid --column-slug "": the API has no clear-the-column form (the schema requires 1-60 characters and is not nullable) — omit the flag to keep the current column`)
		}
		if n := utf8.RuneCountInString(columnSlug); n > 60 {
			return fmt.Errorf("invalid --column-slug: must be 1-60 characters, got %d", n)
		}
	}
	return nil
}

func newFeaturesCreateCmd() *cobra.Command {
	var title, description, columnSlug, versionID string
	create := &cobra.Command{
		Use:                   "create",
		Short:                 "Create a feature request on behalf of a user",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" || description == "" {
				return errors.New("--title and --description are required")
			}
			if err := validateFeaturesCreateFlags(title, description, columnSlug); err != nil {
				return err
			}
			appID, err := A.requireAppID()
			if err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]any{"appId": appID, "title": title, "description": description}
			if columnSlug != "" {
				body["columnSlug"] = columnSlug
			}
			if versionID != "" {
				body["versionId"] = versionID
			}
			var resp api.AdminFeatureRequest
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/feature-requests"), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.PrintfSafe("✓ Created feature request %s", resp.ID)
			return nil
		},
	}
	create.Flags().StringVar(&title, "title", "", "Request title (required)")
	create.Flags().StringVar(&description, "description", "", "Request description (required)")
	create.Flags().StringVar(&columnSlug, "column-slug", "", "Target roadmap column slug")
	create.Flags().StringVar(&versionID, "version-id", "", "Target version ID")
	return create
}

func newFeaturesUpdateCmd() *cobra.Command {
	var title, description, columnSlug string
	var versionID string
	var approved bool
	update := &cobra.Command{
		Use:   "update <request-id>",
		Short: "Update a feature request",
		Args:  exactNonBlankArg("feature request reference"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate before the lookup: an out-of-bounds flag must fail
			// without burning the ID-resolution round trips (issue #341).
			if err := validateFeaturesUpdateFlags(cmd, title, description, columnSlug); err != nil {
				return err
			}
			r, err := fetchOneFeatureRequest(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("title") {
				body["title"] = title
			}
			if cmd.Flags().Changed("description") {
				body["description"] = description
			}
			if cmd.Flags().Changed("column-slug") {
				body["columnSlug"] = columnSlug
			}
			if cmd.Flags().Changed("version-id") {
				body["versionId"] = nilIfEmpty(versionID)
			}
			if cmd.Flags().Changed("approved") {
				body["approved"] = approved
			}
			if len(body) == 0 {
				return errors.New("nothing to update: pass at least one flag")
			}
			if err := A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/feature-requests/"+r.ID), nil, body, nil); err != nil {
				return err
			}
			if A.structured() {
				return A.emitMutationResult("updated", r.ID)
			}
			A.out.PrintfSafe("✓ Updated feature request %s", r.ID)
			return nil
		},
	}
	update.Flags().StringVar(&title, "title", "", "New title")
	update.Flags().StringVar(&description, "description", "", "New description")
	update.Flags().StringVar(&columnSlug, "column-slug", "", "Move to this roadmap column")
	update.Flags().StringVar(&versionID, "version-id", "", "Target version ID (\"\" clears)")
	update.Flags().BoolVar(&approved, "approved", false, "Approval state")
	return update
}

func newFeaturesApproveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "approve <request-id>",
		Short: "Approve a pending feature request",
		Args:  exactNonBlankArg("feature request reference"),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := fetchOneFeatureRequest(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/feature-requests/"+r.ID+"/approve"), nil, nil, nil); err != nil {
				return err
			}
			if A.structured() {
				return A.emitMutationResult("approved", r.ID)
			}
			A.out.PrintfSafe("✓ Approved %s", r.ID)
			return nil
		},
	}
}

func newFeaturesDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <request-id>",
		Aliases: []string{"rm"},
		Short:   "Delete a feature request",
		Long: `Delete a feature request permanently, including its votes and comments.

The server hard-deletes the request and it cannot be restored. On an
interactive terminal you are asked to confirm before anything is sent;
non-interactive callers (scripts, agents) must pass --yes.`,
		Args: exactNonBlankArg("feature request reference"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirmDestructive(cmd, fmt.Sprintf("permanently delete feature request %q", args[0])); err != nil {
				return err
			}
			r, err := fetchOneFeatureRequest(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "DELETE", wsPath(ws, "/feature-requests/"+r.ID), nil, nil, nil); err != nil {
				return err
			}
			if A.structured() {
				return A.emitMutationResult("deleted", r.ID)
			}
			A.out.PrintfSafe("✓ Deleted %s", r.ID)
			return nil
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	return cmd
}

func newFeaturesForwardCmd() *cobra.Command {
	var target, owner, repo, categoryID string
	var labels []string
	forward := &cobra.Command{
		Use:   "forward <request-id>",
		Short: "Forward a feature request to GitHub (discussion or issue)",
		Args:  exactNonBlankArg("feature request reference"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if target != "discussion" && target != "issue" {
				return fmt.Errorf("invalid --target %q: use discussion or issue", target)
			}
			// ForwardToGitHubInputSchema.labels is z.array(z.string().min(1)
			// .max(60)): an empty slice entry has no satisfiable wire form
			// and an over-length one is a guaranteed 400, so both fail
			// locally before the request lookup GET (issue #345).
			for _, label := range labels {
				if n := utf8.RuneCountInString(label); n < 1 || n > 60 {
					return fmt.Errorf("invalid --labels: each label must be 1-60 characters, got %d", n)
				}
			}
			appID, err := A.requireAppID()
			if err != nil {
				return err
			}
			r, err := fetchOneFeatureRequest(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			// labels must be omitted when unset: the server schema declares
			// z.array(...).default([]), and a zod default only applies to an
			// absent key — an explicit JSON null is rejected with 400.
			body := map[string]any{"targetType": target}
			if owner != "" {
				body["owner"] = owner
			}
			if repo != "" {
				body["repo"] = repo
			}
			if categoryID != "" {
				body["categoryId"] = categoryID
			}
			if len(labels) > 0 {
				body["labels"] = labels
			}
			// r.ID is server-derived (the lookup picked it); only the app
			// segment carries user input, so it alone needs pathID (issue #303).
			path := appPath(ws, appID, "/features/"+r.ID+"/github/forward")
			var resp api.ForwardToGitHubResponse
			if err := A.client.Do(cmd.Context(), "POST", path, nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			if resp.Success {
				A.out.PrintfSafe("✓ Forwarded to GitHub %s: %s", resp.TargetType, resp.URL)
			} else {
				return errors.New("forwarding failed (see API response with --json)")
			}
			return nil
		},
	}
	forward.Flags().StringVar(&target, "target", "discussion", "GitHub target: discussion or issue")
	forward.Flags().StringVar(&owner, "owner", "", "Repository owner (default: app's configured owner)")
	forward.Flags().StringVar(&repo, "repo", "", "Repository name (default: app's configured repo)")
	forward.Flags().StringVar(&categoryID, "category-id", "", "Discussion category ID (discussions only)")
	forward.Flags().StringSliceVar(&labels, "labels", nil, "Comma-separated issue labels (issues only)")
	return forward
}
