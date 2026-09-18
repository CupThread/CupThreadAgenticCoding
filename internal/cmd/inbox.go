package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
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
					return fmt.Errorf("invalid --priority %q: use !!!, !! or !", priority)
				}
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
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
	list.Flags().IntVar(&limit, "limit", 50, "Maximum submissions to list")
	list.Flags().IntVar(&offset, "offset", 0, "Offset for pagination")
	list.Flags().StringVar(&triageStatus, "triage-status", "", "Filter by triage status: open, in_progress, resolved, archived or active (open + in_progress)")
	list.Flags().StringVar(&assignedTo, "assigned-to", "", "Filter by assignee user ID, or \"unassigned\"")
	list.Flags().StringVar(&priority, "priority", "", "Filter by priority: !!!, !! or !")
	list.Flags().StringVar(&appID, "app-id", "", "Filter by app ID")
	list.Flags().StringVar(&q, "q", "", "Title substring search (max 50 chars)")
	return list
}

// newInboxGetCmd shows the full triage detail of one submission, including the
// workspace-scoped activity log.
func newInboxGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <submission-id>",
		Short: "Show one submission's triage detail and activity log",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.SubmissionDetail
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/submissions/"+args[0]), nil, nil, &resp); err != nil {
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
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := args[1]
			switch p {
			case "!!!", "!!", "!":
			default:
				return fmt.Errorf("invalid priority %q: use !!!, !! or !", p)
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]string{"priority": p}
			var resp api.SubmissionRecord
			if err := A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/submissions/"+args[0]+"/priority"), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Printf("✓ Priority of %s set to %s", args[0], p)
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
		Args: cobra.ExactArgs(2),
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
			if err := A.client.Do(cmd.Context(), "PATCH", wsPath(ws, "/submissions/"+id+"/triage"), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Printf("✓ Triage status of %s set to %s", id, status)
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
		Args:                  cobra.RangeArgs(1, 2),
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
			if err := A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/submissions/"+id+"/assign"), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			if body.AssignedTo == nil {
				A.out.Printf("✓ Submission %s unassigned", id)
			} else {
				A.out.Printf("✓ Submission %s assigned to %s", id, *body.AssignedTo)
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
outside the workspace are skipped; the command reports which IDs were updated.`,
		Args: cobra.MinimumNArgs(1),
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
			body := map[string]any{"submissionIds": args}
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
			A.out.Printf("  %s", strings.Join(resp.UpdatedIds, "\n  "))
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.RetrySubmissionResponse
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/submissions/"+args[0]+"/retry"), nil, nil, &resp); err != nil {
				// 502 carries a structured partial-failure payload; surface it.
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			if resp.Error != nil {
				return fmt.Errorf("retry failed: %s", *resp.Error)
			}
			if resp.ForwardedToGithub {
				A.out.Printf("✓ Forwarded to GitHub: %s", orDash(deref(resp.GithubDiscussionURL)))
			} else {
				A.out.Printf("✓ Submission %s processed (no GitHub forwarding)", resp.SubmissionID)
			}
			return nil
		},
	}
}

func newInboxDeliveriesCmd() *cobra.Command {
	return &cobra.Command{
		Use:                   "deliveries",
		Short:                 "Show GitHub delivery queue jobs",
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
				return A.out.Structured(resp)
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
			return nil
		},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
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
