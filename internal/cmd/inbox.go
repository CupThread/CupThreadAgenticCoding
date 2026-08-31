package cmd

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

func newInboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Triage incoming feedback submissions",
	}
	cmd.AddCommand(newInboxListCmd(), newInboxPriorityCmd(), newInboxRetryCmd(), newInboxDeliveriesCmd())
	return cmd
}

func newInboxListCmd() *cobra.Command {
	var limit, offset int
	list := &cobra.Command{
		Use:   "list",
		Short: "List feedback submissions (newest first)",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
			var resp api.ListSubmissionsResponse
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/submissions"), q, nil, &resp); err != nil {
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
					orDash(deref(s.ReporterName)),
					cutDate(s.CreatedAt),
				})
			}
			A.out.Table([]string{"ID", "Title", "Platform", "Priority", "Status", "Reporter", "Created"}, rows)
			A.out.Printf("(%d shown, %d total)", len(resp.Submissions), resp.Total)
			return nil
		},
	}
	list.Flags().IntVar(&limit, "limit", 50, "Maximum submissions to list")
	list.Flags().IntVar(&offset, "offset", 0, "Offset for pagination")
	return list
}

// inboxSubmissionRef resolves a submission by exact ID or unique ID prefix.
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
		Use:   "deliveries",
		Short: "Show GitHub delivery queue jobs",
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
