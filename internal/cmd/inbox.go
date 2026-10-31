package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
	"github.com/spf13/cobra"
)

// triageStatuses mirrors the API's TriageStatus enum. The special value
// "active" (open + in_progress) is only valid as a list filter.
var triageStatuses = []string{"open", "in_progress", "resolved", "archived"}

func isValidTriageStatus(s string) bool {
	for _, v := range triageStatuses {
		if s == v {
			return true
		}
	}
	return false
}

func newInboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Triage incoming feedback submissions",
	}
	cmd.AddCommand(
		newInboxListCmd(),
		newInboxGetCmd(),
		newInboxPriorityCmd(),
		newInboxTriageCmd(),
		newInboxAssignCmd(),
		newInboxBulkTriageCmd(),
		newInboxRetryCmd(),
		newInboxDeliveriesCmd(),
	)
	return cmd
}

func newInboxListCmd() *cobra.Command {
	var limit, offset int
	var triageStatus, assignedTo, priority, appID, q string
	list := &cobra.Command{
		Use:   "list",
		Short: "List feedback submissions (newest first)",
		Long: `List feedback submissions (newest first).

Delivery status (Status column) is received/forwarded/forward_failed and is
separate from the triage lifecycle shown in the Triage column:
open/in_progress/resolved/archived.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if triageStatus != "" && triageStatus != "active" && !isValidTriageStatus(triageStatus) {
				return fmt.Errorf("invalid --triage-status %q: use open, in_progress, resolved, archived or active", triageStatus)
			}
			if priority != "" {
				switch priority {
				case "!!!", "!!", "!":
				default:
					return fmt.Errorf("invalid --priority %q: use \"!!!\", \"!!\" or \"!\"", priority)
				}
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			// The server clamps the page size to 200 silently; clamp here
			// too so an offset walk steps by the served size (issue #201).
			limit = A.clampListLimit(limit, inboxPageSize)
			// The console listing clamps the offset at 10,000 and still
			// answers 200; announce the rewrite so a deep walk notices it is
			// re-reading the final page (issue #178).
			offset = A.clampListOffset(offset)
			qv := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
			if triageStatus != "" {
				qv.Set("triage_status", triageStatus)
			}
			if assignedTo != "" {
				qv.Set("assigned_to", assignedTo)
			}
			if priority != "" {
				qv.Set("priority", priority)
			}
			if appID != "" {
				qv.Set("app_id", appID)
			}
			if q != "" {
				qv.Set("q", q)
			}
			var resp api.ListSubmissionsResponse
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/submissions"), qv, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			rows := make([][]string, 0, len(resp.Submissions))
			for _, s := range resp.Submissions {
				id := s.SubmissionID
				if len(id) > 12 {
					id = id[:12]
				}
				rows = append(rows, []string{
					id,
					truncate(s.Title, 40),
					s.Platform,
					s.Priority,
					s.Status,
					orDashIfEmpty(s.TriageStatus),
					truncate(orDash(deref(s.AssignedTo)), 12),
					orDash(deref(s.ReporterName)),
					cutDate(s.CreatedAt),
				})
			}
			A.out.Table([]string{"ID", "Title", "Platform", "Priority", "Status", "Triage", "Assignee", "Reporter", "Created"}, rows)
			A.out.Printf("(%d shown, %d total)", len(resp.Submissions), resp.Total)
			return nil
		},
	}
	list.Flags().IntVar(&limit, "limit", 50, "Maximum submissions to list (1-200, server page cap 200)")
	list.Flags().IntVar(&offset, "offset", 0, "Offset for pagination (values above 10000 are clamped to 10000 by the server)")
	list.Flags().StringVar(&triageStatus, "triage-status", "", "Filter by triage status: open, in_progress, resolved, archived or active (open + in_progress)")
	list.Flags().StringVar(&assignedTo, "assigned-to", "", "Filter by assignee user ID, or \"unassigned\"")
	list.Flags().StringVar(&priority, "priority", "", "Filter by priority: !!!, !! or !")
	list.Flags().StringVar(&appID, "app-id", "", "Filter by app ID")
	list.Flags().StringVar(&q, "q", "", "Title substring search (max 50 chars)")
	return list
}

// inboxPageSize is the submissions listing's server page cap (the route's
// parseListPagination maxLimit); the list command clamps --limit to it.
const inboxPageSize = 200

// maxInboxResolvePages caps submission ID-resolution paging the way
// maxChangelogResolvePages does: 51 pages of the listing's 200-entry page cap
// covers offsets 0 through 10,000 — the whole reachable universe, since the
// server clamps offset at the shared 10,000-entry cap and still answers 200
// with the final page (issue #178). The +1 matters for correctness, not
// thrift (PR #414 review): without it the offset-10,000 page is never
// scanned, and a prefix matching both a submission in the scanned window and
// one on that final page would wrongly resolve to the scanned one — a
// triage/assign acting on the wrong row.
const maxInboxResolvePages = consoleListMaxOffset/inboxPageSize + 1

// resolveSubmissionRecord resolves a submission reference — the exact id or a
// unique prefix of one — against the workspace listing (issue #418): the
// inbox list and deliveries tables print 12-character ids, and the
// submission-taking commands used to forward the truncated form verbatim,
// which the server can only answer with a guaranteed 404. The walk is
// unfiltered (the whole workspace — resolution never applies the list
// command's filters), and matching, ambiguity, and the page-cap guard live in
// the shared resolveIDRef.
func resolveSubmissionRecord(ctx context.Context, ws, ref string) (*api.SubmissionRecord, error) {
	return resolveIDRef(ref, idRefNouns{what: "submission", plural: "submissions", listHint: "cupthread inbox list"}, maxInboxResolvePages, true,
		func(page int) ([]api.SubmissionRecord, bool, error) {
			offset := page * inboxPageSize
			q := url.Values{"limit": {strconv.Itoa(inboxPageSize)}, "offset": {strconv.Itoa(offset)}}
			var resp api.ListSubmissionsResponse
			if err := A.client.Do(ctx, "GET", wsPath(ws, "/submissions"), q, nil, &resp); err != nil {
				return nil, false, err
			}
			return resp.Submissions, len(resp.Submissions) < inboxPageSize || offset+len(resp.Submissions) >= resp.Total, nil
		},
		submissionNotFound(ws),
		func(s *api.SubmissionRecord) string { return s.SubmissionID },
	)
}

// submissionNotFound mirrors the changelog not-found contract: it names the
// scope the scan actually covered — the workspace, since resolution walks the
// unfiltered listing — and the listing the table ids come from.
func submissionNotFound(ws string) func(ref string, scanned int, exhausted bool, prefixMatches int) error {
	return func(ref string, scanned int, exhausted bool, prefixMatches int) error {
		if !exhausted {
			return fmt.Errorf("submission %q not found in the first %d submissions (resolution scans at most %d pages)", ref, scanned, maxInboxResolvePages)
		}
		return fmt.Errorf("submission %q not found (scanned %d submissions in workspace %s)", ref, scanned, ws)
	}
}

// fireSubmissionRef fires one submission-scoped request at ref verbatim —
// the one-request exact path every full-id caller keeps — and only when that
// answers 404, the truncated reference's guaranteed answer, resolves the
// reference against the workspace listing and fires again at the resolved
// full id. It returns the id the successful request used, so every echo line
// carries the resolved-full-id contract the other families follow. The probe
// is safe to fire first: the server matches exact ids only, so a truncated
// reference 404s without touching any row, and the API client never retries a
// 404. A non-404 failure (5xx, auth, tier limit) propagates without
// resolution, so the server's own diagnosis is never masked.
func fireSubmissionRef(ctx context.Context, ws, ref string, fire func(id string) error) (string, error) {
	if err := fire(ref); err == nil {
		return ref, nil
	} else if !apiNotFound(err) {
		return "", err
	}
	rec, err := resolveSubmissionRecord(ctx, ws, ref)
	if err != nil {
		return "", err
	}
	if err := fire(rec.SubmissionID); err != nil {
		return "", err
	}
	return rec.SubmissionID, nil
}

// fetchOneSubmission resolves a submission reference for the one caller that
// must hold the full id before it fires: bulk-triage's body lists every id,
// and its endpoint skips unknown ids instead of 404ing, so a truncated
// reference would be silently skipped rather than probe-and-refired like the
// single-submission commands. The exact id keeps its one-request fast path
// against the detail route; only its 404 falls back to the workspace listing
// walk (the fetchOneImportJob pattern).
func fetchOneSubmission(ctx context.Context, ref string) (*api.SubmissionRecord, error) {
	ws, err := workspaceClient(ctx)
	if err != nil {
		return nil, err
	}
	var detail api.SubmissionDetail
	if err := A.client.Do(ctx, "GET", wsPath(ws, "/submissions/"+pathID(ref)), nil, nil, &detail); err == nil {
		return &detail.Submission, nil
	} else if !apiNotFound(err) {
		return nil, err
	}
	return resolveSubmissionRecord(ctx, ws, ref)
}

// newInboxGetCmd shows the full triage detail of one submission, including the
// workspace-scoped activity log.
func newInboxGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <submission-id>",
		Short: "Show one submission's triage detail and activity log",
		Args:  exactNonBlankArg("submission id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.SubmissionDetail
			if _, err := fireSubmissionRef(cmd.Context(), ws, args[0], func(id string) error {
				return A.client.Do(cmd.Context(), "GET", wsPath(ws, "/submissions/"+pathID(id)), nil, nil, &resp)
			}); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			s := resp.Submission
			A.out.Table([]string{"Field", "Value"}, [][]string{
				{"ID", s.SubmissionID},
				{"Title", s.Title},
				{"Description", truncate(s.Description, 400)},
				{"Platform", s.Platform},
				{"App", orDash(derefAppRef(resp.App))},
				{"App version", orDash(deref(s.AppVersion))},
				{"Build", orDash(deref(s.BuildNumber))},
				{"Priority", s.Priority},
				{"Delivery status", s.Status},
				{"Triage status", orDashIfEmpty(s.TriageStatus)},
				{"Assignee", orDash(deref(s.AssignedTo))},
				{"Reporter", orDash(deref(s.ReporterName))},
				{"Reporter email", orDash(deref(s.ReporterEmail))},
				{"GitHub discussion", orDash(deref(s.GithubDiscussionURL))},
				{"First triaged", orDash(deref(s.FirstTriagedAt))},
				{"Resolved", orDash(deref(s.ResolvedAt))},
				{"Created", s.CreatedAt},
				{"Updated", s.UpdatedAt},
			})
			if len(resp.Attachments) > 0 {
				A.out.Printf("\nAttachments (%d):", len(resp.Attachments))
				rows := make([][]string, 0, len(resp.Attachments))
				for _, a := range resp.Attachments {
					rows = append(rows, []string{
						a.AttachmentID,
						a.Kind,
						orDash(deref(a.Filename)),
						orDash(deref(a.MimeType)),
						orDash(derefSize(a.SizeBytes)),
						cutDate(a.CreatedAt),
					})
				}
				A.out.Table([]string{"ID", "Kind", "Filename", "Type", "Size", "Created"}, rows)
			}
			if len(resp.DeliveryAttempts) > 0 {
				A.out.Printf("\nDelivery attempts (%d):", len(resp.DeliveryAttempts))
				rows := make([][]string, 0, len(resp.DeliveryAttempts))
				for _, d := range resp.DeliveryAttempts {
					rows = append(rows, []string{
						cutDate(d.AttemptedAt),
						d.Status,
						orDash(deref(d.GithubDiscussionURL)),
						truncate(orDash(deref(d.ErrorMessage)), 60),
					})
				}
				A.out.Table([]string{"Attempted", "Status", "Discussion", "Error"}, rows)
			}
			A.out.Printf("\nActivity (%d):", len(resp.Activity))
			if len(resp.Activity) == 0 {
				A.out.Printf("  (no activity recorded)")
				return nil
			}
			rows := make([][]string, 0, len(resp.Activity))
			for _, e := range resp.Activity {
				rows = append(rows, []string{
					cutDate(e.CreatedAt),
					e.Kind,
					truncate(orDash(e.ActorID), 16),
					activityDetail(e.Payload),
				})
			}
			A.out.Table([]string{"When", "Kind", "Actor", "Detail"}, rows)
			return nil
		},
	}
}

func newInboxPriorityCmd() *cobra.Command {
	priority := &cobra.Command{
		Use:   "priority <submission-id> <!!!|!!|!>",
		Short: "Raise or reset a submission's priority",
		Args:  cobra.MatchAll(cobra.ExactArgs(2), nonBlankArg(0, "submission id")),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := args[1]
			switch p {
			case "!!!", "!!", "!":
			default:
				return fmt.Errorf("invalid priority %q: use \"!!!\", \"!!\" or \"!\"", p)
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]string{"priority": p}
			var resp api.SubmissionRecord
			id, err := fireSubmissionRef(cmd.Context(), ws, args[0], func(id string) error {
				return A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/submissions/"+pathID(id)+"/priority"), nil, body, &resp)
			})
			if err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.PrintfSafe("✓ Priority of %s set to %s", id, p)
			return nil
		},
	}
	return priority
}

// newInboxTriageCmd sets the triage lifecycle status of one submission. This
// is distinct from the delivery status (received/forwarded/forward_failed),
// which the CLI never writes.
func newInboxTriageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "triage <submission-id> <open|in_progress|resolved|archived>",
		Short: "Set a submission's triage status",
		Long: `Set a submission's triage lifecycle status.

Triage status (open, in_progress, resolved, archived) is separate from the
delivery status (received/forwarded/forward_failed), which is managed by the
platform and only changeable via 'cupthread inbox retry'.`,
		Args: cobra.MatchAll(cobra.ExactArgs(2), nonBlankArg(0, "submission id")),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, status := args[0], args[1]
			if !isValidTriageStatus(status) {
				return fmt.Errorf("invalid triage status %q: use open, in_progress, resolved or archived", status)
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]string{"triageStatus": status}
			var resp api.SubmissionRecord
			resolved, err := fireSubmissionRef(cmd.Context(), ws, id, func(id string) error {
				return A.client.Do(cmd.Context(), "PATCH", wsPath(ws, "/submissions/"+pathID(id)+"/triage"), nil, body, &resp)
			})
			if err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.PrintfSafe("✓ Triage status of %s set to %s", resolved, status)
			return nil
		},
	}
}

// newInboxAssignCmd assigns a submission to a workspace member. Without a
// user ID the submission is unassigned (the API receives null).
func newInboxAssignCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "assign <submission-id> [clerk-user-id]",
		Short: "Assign a submission to a workspace member (no user ID unassigns)",
		Long: `Assign a submission to a workspace member.

The assignee must be a member of the workspace. Assigning an open submission
also moves it to in_progress. Omitting the user ID unassigns it.`,
		Args:                  cobra.MatchAll(cobra.RangeArgs(1, 2), nonBlankArg(0, "submission id"), nonBlankArg(1, "clerk user id")),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			var body struct {
				AssignedTo *string `json:"assignedTo"`
			}
			if len(args) == 2 {
				body.AssignedTo = &args[1]
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.SubmissionRecord
			resolved, err := fireSubmissionRef(cmd.Context(), ws, id, func(id string) error {
				return A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/submissions/"+pathID(id)+"/assign"), nil, body, &resp)
			})
			if err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			if body.AssignedTo == nil {
				A.out.PrintfSafe("✓ Submission %s unassigned", resolved)
			} else {
				A.out.PrintfSafe("✓ Submission %s assigned to %s", resolved, *body.AssignedTo)
			}
			return nil
		},
	}
}

// newInboxBulkTriageCmd applies a triage status and/or assignee to up to 50
// submissions in one call.
func newInboxBulkTriageCmd() *cobra.Command {
	var status, assignee string
	var unassign bool
	bulk := &cobra.Command{
		Use:   "bulk-triage <submission-id>...",
		Short: "Bulk-set triage status and/or assignee (1-50 submissions)",
		Long: `Bulk-update the triage status and/or assignee of 1-50 submissions.

Pass at least one of --triage-status, --assignee or --unassign. Submissions
outside the workspace are skipped; the command reports which IDs were updated.
Blank ids are refused at argument validation, before any request (issue #427).`,
		Args: cobra.MatchAll(cobra.MinimumNArgs(1), noBlankArgs("submission id")),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 50 {
				return errors.New("too many submission IDs: bulk-triage accepts at most 50")
			}
			if status == "" && assignee == "" && !unassign {
				return errors.New("nothing to do: pass --triage-status, --assignee or --unassign")
			}
			if status != "" && !isValidTriageStatus(status) {
				return fmt.Errorf("invalid --triage-status %q: use open, in_progress, resolved or archived", status)
			}
			if assignee != "" && unassign {
				return errors.New("--assignee and --unassign are mutually exclusive")
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			// Every positional reference resolves before the body is built
			// (issue #418): the bulk endpoint skips ids outside the workspace
			// instead of 404ing, so a truncated reference must never reach it
			// verbatim — it would be silently skipped, and UpdatedCount would
			// under-report with exit code 0. The single-submission commands'
			// probe-and-refire cannot exist here, so each reference takes the
			// detail-GET fast path and its 404 the listing walk. Blank
			// references never get this far: the Args-layer noBlankArgs guard
			// refuses them before RunE (issue #427), so the loop can interpolate
			// every ref into the detail path without a local empty check.
			ids := make([]string, 0, len(args))
			for i, ref := range args {
				rec, err := fetchOneSubmission(cmd.Context(), ref)
				if err != nil {
					return fmt.Errorf("argument %d: %w", i+1, err)
				}
				ids = append(ids, rec.SubmissionID)
			}
			body := map[string]any{"submissionIds": ids}
			if status != "" {
				body["triageStatus"] = status
			}
			switch {
			case unassign:
				body["assignedTo"] = nil
			case assignee != "":
				body["assignedTo"] = assignee
			}
			var resp api.BulkSubmissionTriageResponse
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/submissions/bulk-triage"), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Printf("✓ Updated %d submission(s)", resp.UpdatedCount)
			// The IDs are server-derived (issue #275): strip each one so no
			// control bytes ride into the human output, but keep the
			// deliberate multi-line join — PrintfSafe would eat the layout.
			stripped := make([]string, 0, len(resp.UpdatedIds))
			for _, id := range resp.UpdatedIds {
				stripped = append(stripped, output.StripTerminalControls(id))
			}
			A.out.Printf("  %s", strings.Join(stripped, "\n  "))
			return nil
		},
	}
	bulk.Flags().StringVar(&status, "triage-status", "", "Triage status to apply: open, in_progress, resolved or archived")
	bulk.Flags().StringVar(&assignee, "assignee", "", "Clerk user ID to assign all submissions to")
	bulk.Flags().BoolVar(&unassign, "unassign", false, "Unassign all submissions (sends assignedTo: null)")
	return bulk
}

func newInboxRetryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retry <submission-id>",
		Short: "Retry GitHub forwarding for a failed submission",
		Args:  exactNonBlankArg("submission id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.RetrySubmissionResponse
			if _, err := fireSubmissionRef(cmd.Context(), ws, args[0], func(id string) error {
				// 502 carries a structured partial-failure payload; surface it.
				return A.client.Do(cmd.Context(), "POST", wsPath(ws, "/submissions/"+pathID(id)+"/retry"), nil, nil, &resp)
			}); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			if resp.Error != nil {
				// The error text is free-form server output that cobra prints
				// to stderr raw (issue #275): strip it at the boundary.
				return fmt.Errorf("retry failed: %s", output.StripTerminalControls(*resp.Error))
			}
			if resp.ForwardedToGithub {
				A.out.PrintfSafe("✓ Forwarded to GitHub: %s", orDash(deref(resp.GithubDiscussionURL)))
			} else {
				A.out.PrintfSafe("✓ Submission %s processed (no GitHub forwarding)", resp.SubmissionID)
			}
			return nil
		},
	}
}

// deliveryJobsPageSize is the delivery-queue listing's hardcoded server
// window: the route takes no query parameters and passes a literal 50 to
// listDeliveryJobsForWorkspace (SaaS apps/api/src/domains/jobs/routes.ts,
// ORDER BY created_at DESC LIMIT ?), so the newest 50 jobs are all any
// client can see (issue #352).
const deliveryJobsPageSize = 50

func newInboxDeliveriesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deliveries",
		Short: "Show GitHub delivery queue jobs (newest first)",
		Long: `Show GitHub delivery queue jobs (newest first).

The server caps this listing at the newest 50 jobs and offers no paging or
total, so older jobs — including failed or dead ones — can be missing from
the view. When the cap is hit the command says so, in table mode on stdout
and in --json/-o yaml mode on stderr.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.ListDeliveryJobsResponse
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/delivery-jobs"), nil, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				if err := A.out.Structured(resp); err != nil {
					return err
				}
				A.warnDeliveryJobsTruncation(&resp)
				return nil
			}
			rows := make([][]string, 0, len(resp.Jobs))
			for _, j := range resp.Jobs {
				sub := j.SubmissionID
				if len(sub) > 12 {
					sub = sub[:12]
				}
				rows = append(rows, []string{
					j.ID, sub, j.Status,
					fmt.Sprintf("%d/%d", j.Attempts, j.MaxAttempts),
					j.NextAttemptAt, truncate(orDash(deref(j.LastError)), 40),
				})
			}
			A.out.Table([]string{"Job", "Submission", "Status", "Attempts", "Next attempt", "Last error"}, rows)
			if resp.Total != nil {
				A.out.Printf("(%d shown, %d total)", len(resp.Jobs), *resp.Total)
			}
			A.warnDeliveryJobsTruncation(&resp)
			return nil
		},
	}
}

// warnDeliveryJobsTruncation discloses the delivery-queue listing's
// server-side window so a full page is never indistinguishable from the
// whole queue (issue #352, the #346 disclosure discipline). Today's route
// sends no total, so a cap-sized answer is the only honest truncation signal
// and carries a "may be" (a queue of exactly 50 jobs trips it too); once the
// SaaS side adds total, the notice keys off total > shown instead. warnf
// routes the notice: stdout in table mode, stderr in --json/-o yaml mode so
// the structured document stays byte-faithful.
func (a *app) warnDeliveryJobsTruncation(resp *api.ListDeliveryJobsResponse) {
	switch {
	case resp.Total != nil && *resp.Total > len(resp.Jobs):
		a.warnf("⚠ Delivery queue is truncated: showing the %d newest of %d jobs; older jobs (including failed or dead ones) are not retrievable through this listing", len(resp.Jobs), *resp.Total)
	case resp.Total == nil && len(resp.Jobs) >= deliveryJobsPageSize:
		a.warnf("⚠ Delivery queue may be truncated: this listing serves only the newest %d jobs (server cap, no paging or total); older jobs — including failed or dead ones — are not shown", deliveryJobsPageSize)
	}
}

// truncate caps s at n bytes, appending "…" when content is dropped. The cut
// always lands on a rune boundary so non-ASCII content (CJK titles, emoji)
// never emits an invalid UTF-8 sequence into table output. Pure-ASCII
// over-cap input keeps the historical exact result s[:n-1] + "…".
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	cut := n - 1
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func cutDate(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

// orDashIfEmpty renders an empty (absent) API string as a dash, e.g. a
// triageStatus missing from an older API response.
func orDashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func derefAppRef(a *api.SubmissionAppRef) string {
	if a == nil {
		return ""
	}
	return a.Name + " (" + a.Slug + ")"
}

func derefSize(n *int64) string {
	if n == nil {
		return ""
	}
	return strconv.FormatInt(*n, 10) + " B"
}

// activityDetail renders the interesting payload keys of an activity event as
// a compact "k=v k=v" summary.
func activityDetail(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v, err := json.Marshal(payload[k])
		if err != nil {
			continue
		}
		parts = append(parts, k+"="+string(v))
	}
	return truncate(strings.Join(parts, " "), 60)
}
