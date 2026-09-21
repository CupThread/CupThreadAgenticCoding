package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

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
	list := &cobra.Command{
		Use:   "list",
		Short: "List feature requests",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			// The server clamps the page size to 200 silently; clamp here
			// too so --limit 500 does not imply it did anything more.
			if limit < 1 {
				limit = 1
			} else if limit > featureRequestPageSize {
				limit = featureRequestPageSize
			}
			appID := flagApp
			resp, err := listFeatureRequests(cmd.Context(), ws, appID, limit, offset, sort, payerOnly)
			if err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Table([]string{"ID", "Title", "Column", "Version", "Votes", "Approved", "Revenue", "Created"}, featureRows(resp.Requests))
			A.out.Printf("(%d shown, %d total)", len(resp.Requests), resp.Total)
			// Always printed (even at 0) so the line's presence is a stable
			// part of the output shape; it is the Console triage backlog
			// badge's count (QUAL-03).
			A.out.Printf("(%d of %d unassigned)", resp.UnassignedTotal, resp.Total)
			return nil
		},
	}
	list.Flags().IntVar(&limit, "limit", 50, "Maximum requests to list (1-200, server page cap 200)")
	list.Flags().IntVar(&offset, "offset", 0, "Offset for pagination")
	list.Flags().StringVar(&sort, "sort", "newest", "Sort order: newest or revenue (revenue is Pro-gated)")
	list.Flags().BoolVar(&payerOnly, "payer-only", false, "Only requests from paying users (Pro-gated)")
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
// addressable, not just the newest page. An exact ID match returns
// immediately; prefix matches accumulate across every page before the
// ambiguity decision, so a prefix that looks unique on the first page still
// errors once a later page adds a second match.
//
// The lookup is scoped to the resolved app (--app flag, else the saved
// default from 'apps use') so ID resolution cannot cross into another app's
// requests; with no app resolved it stays workspace-wide.
func fetchOneFeatureRequest(ctx context.Context, ref string) (*api.AdminFeatureRequest, error) {
	ws, err := workspaceClient(ctx)
	if err != nil {
		return nil, err
	}
	appID := A.optionalAppID()
	var matches []api.AdminFeatureRequest
	scanned := 0
	exhausted := false
	for page := 0; page < maxFeatureRequestPages && !exhausted; page++ {
		offset := page * featureRequestPageSize
		resp, err := listFeatureRequests(ctx, ws, appID, featureRequestPageSize, offset, "", false)
		if err != nil {
			return nil, err
		}
		for i := range resp.Requests {
			if resp.Requests[i].ID == ref {
				return &resp.Requests[i], nil
			}
			if strings.HasPrefix(resp.Requests[i].ID, ref) {
				matches = append(matches, resp.Requests[i])
			}
		}
		scanned += len(resp.Requests)
		if len(resp.Requests) < featureRequestPageSize || offset+len(resp.Requests) >= resp.Total {
			exhausted = true
		}
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous request prefix %q matches %d requests; use a longer prefix", ref, len(matches))
	}
	if !exhausted {
		return nil, fmt.Errorf("feature request %q not found in the first %d requests (resolution scans at most %d pages); narrow the search with --app", ref, scanned, maxFeatureRequestPages)
	}
	return nil, fmt.Errorf("feature request %q not found (scanned %d requests in workspace %s)", ref, scanned, ws)
}

func newFeaturesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <request-id>",
		Short: "Show one feature request",
		Args:  cobra.ExactArgs(1),
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

func newFeaturesCreateCmd() *cobra.Command {
	var title, description, columnSlug, versionID string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a feature request on behalf of a user",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" || description == "" {
				return errors.New("--title and --description are required")
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
			A.out.Printf("✓ Created feature request %s", resp.ID)
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if !A.structured() {
				A.out.Printf("✓ Updated feature request %s", r.ID)
			}
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
		Args:  cobra.ExactArgs(1),
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
			if !A.structured() {
				A.out.Printf("✓ Approved %s", r.ID)
			}
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
		Args: cobra.ExactArgs(1),
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
			if !A.structured() {
				A.out.Printf("✓ Deleted %s", r.ID)
			}
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if target != "discussion" && target != "issue" {
				return fmt.Errorf("invalid --target %q: use discussion or issue", target)
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
			body := map[string]any{"targetType": target, "labels": labels}
			if owner != "" {
				body["owner"] = owner
			}
			if repo != "" {
				body["repo"] = repo
			}
			if categoryID != "" {
				body["categoryId"] = categoryID
			}
			path := fmt.Sprintf("%s/apps/%s/features/%s/github/forward", wsPath(ws, ""), appID, r.ID)
			var resp api.ForwardToGitHubResponse
			if err := A.client.Do(cmd.Context(), "POST", path, nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			if resp.Success {
				A.out.Printf("✓ Forwarded to GitHub %s: %s", resp.TargetType, resp.URL)
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
