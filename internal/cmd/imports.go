package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

var importSources = []string{"github_issues", "github_discussions", "linear", "notion", "slack"}

func newImportsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "imports",
		Aliases: []string{"import"},
		Short:   "Import feature requests from GitHub / Linear / Notion / Slack",
	}
	cmd.AddCommand(
		newImportsListCmd(),
		newImportsHistoryCmd(),
		newImportsCreateCmd(),
		newImportsGetCmd(),
		newImportsCancelCmd(),
		newImportsRerunCmd(),
	)
	return cmd
}

func printImportJobs(jobs []api.ImportJob) {
	rows := make([][]string, 0, len(jobs))
	for _, j := range jobs {
		errMsg := "—"
		if j.LastError != nil {
			errMsg = truncate(*j.LastError, 36)
		}
		rows = append(rows, []string{
			shortID(j.ID), j.Source, j.Mode, j.Status,
			fmt.Sprintf("%d", j.Attempts), errMsg, cutDate(j.CreatedAt),
		})
	}
	A.out.Table([]string{"Job", "Source", "Mode", "Status", "Attempts", "Error", "Created"}, rows)
}

func newImportsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List import jobs of the app",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, appID, err := resolveAppScope(cmd)
			if err != nil {
				return err
			}
			var resp api.ListImportJobsResponse
			path := fmt.Sprintf("%s/apps/%s/imports", wsPath(ws, ""), appID)
			if err := A.client.Do(cmd.Context(), "GET", path, nil, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			printImportJobs(resp.Jobs)
			return nil
		},
	}
}

func newImportsHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history",
		Short: "List all import jobs in the workspace",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.ListImportJobsResponse
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/imports"), nil, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			printImportJobs(resp.Jobs)
			return nil
		},
	}
}

func newImportsCreateCmd() *cobra.Command {
	var source, mode, columnSlug string
	var limit int
	var includeDuplicates bool
	var owner, repo, state, categorySlug, teamID, databaseID, channelID string
	var labels []string
	var optionsFile string

	create := &cobra.Command{
		Use:   "create",
		Short: "Create an import job (preview by default)",
		Long: `Create an import job for the app.

Preview jobs complete synchronously and show the candidate diff; commit jobs
run on the queue and actually create feature requests. Source availability is
tier-gated (GitHub issues/discussions need Pro; Linear/Notion/Slack need
Business).

Pass --options @file to send the raw ImportOptions JSON instead of the
per-source flags.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if source == "" {
				return errors.New("--source is required (github_issues, github_discussions, linear, notion or slack)")
			}
			if mode != "preview" && mode != "commit" {
				return fmt.Errorf("invalid --mode %q: use preview or commit", mode)
			}
			appID, err := A.requireAppID()
			if err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}

			// json.RawMessage values keep every number in the --options body
			// byte-identical on the wire (issue #75); a map[string]any decode
			// would round-trip them through float64 and silently rewrite
			// integers a float64 cannot represent exactly.
			var options map[string]json.RawMessage
			if optionsFile != "" {
				data, err := readInputFile(optionsFile)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(data, &options); err != nil {
					return fmt.Errorf("parse options JSON: %w", err)
				}
			} else {
				flagged := map[string]any{}
				if owner != "" {
					flagged["owner"] = owner
				}
				if repo != "" {
					flagged["repo"] = repo
				}
				if len(labels) > 0 {
					flagged["labels"] = labels
				}
				if state != "" {
					flagged["state"] = state
				}
				if categorySlug != "" {
					flagged["categorySlug"] = categorySlug
				}
				if teamID != "" {
					flagged["teamId"] = teamID
				}
				if databaseID != "" {
					flagged["databaseId"] = databaseID
				}
				if channelID != "" {
					flagged["channelId"] = channelID
				}
				if cmd.Flags().Changed("limit") {
					flagged["limit"] = limit
				}
				if columnSlug != "" {
					flagged["columnSlug"] = columnSlug
				}
				if includeDuplicates {
					flagged["includeDuplicates"] = true
				}
				// The flag values are plain strings/ints/bools, so marshaling
				// each into a RawMessage is lossless and gives both sources
				// one body type.
				options = make(map[string]json.RawMessage, len(flagged))
				for key, v := range flagged {
					encoded, err := json.Marshal(v)
					if err != nil {
						return fmt.Errorf("encode option %q: %w", key, err)
					}
					options[key] = encoded
				}
			}

			body := map[string]any{"appId": appID, "source": source, "mode": mode, "options": options}
			path := fmt.Sprintf("%s/apps/%s/imports", wsPath(ws, ""), appID)
			var resp api.CreateImportJobResponse
			if err := A.client.Do(cmd.Context(), "POST", path, nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Printf("✓ Import job %s (%s/%s, status %s)", resp.Job.ID, resp.Job.Source, resp.Job.Mode, resp.Job.Status)
			A.out.Printf("  Drain: %d processed, %d succeeded, %d failed", resp.Drain.Processed, resp.Drain.Succeeded, resp.Drain.Failed)
			if resp.Job.Status == "completed" && len(resp.Job.Candidates) > 0 {
				A.out.Printf("  Preview candidates: cupthread imports get %s --json | jq .job.candidates", resp.Job.ID)
			}
			return nil
		},
	}
	create.Flags().StringVar(&source, "source", "", "Import source (required)")
	create.Flags().StringVar(&mode, "mode", "preview", "preview shows the diff, commit creates requests")
	create.Flags().StringVar(&optionsFile, "options", "", "Raw ImportOptions JSON file (\"-\" for stdin), sent verbatim (numbers keep full precision); overrides per-source flags")
	create.Flags().StringVar(&owner, "owner", "", "GitHub owner")
	create.Flags().StringVar(&repo, "repo", "", "GitHub repository")
	create.Flags().StringSliceVar(&labels, "labels", nil, "GitHub labels filter (comma-separated)")
	create.Flags().StringVar(&state, "state", "", "GitHub item state: open, closed or all")
	create.Flags().StringVar(&categorySlug, "category-slug", "", "GitHub Discussions category slug")
	create.Flags().StringVar(&teamID, "team-id", "", "Linear team ID")
	create.Flags().StringVar(&databaseID, "database-id", "", "Notion database ID")
	create.Flags().StringVar(&channelID, "channel-id", "", "Slack channel ID")
	create.Flags().IntVar(&limit, "limit", 0, "Maximum items to import (1-200)")
	create.Flags().StringVar(&columnSlug, "column-slug", "", "Target roadmap column for imported requests")
	create.Flags().BoolVar(&includeDuplicates, "include-duplicates", false, "Create requests that look like duplicates")
	_ = create.RegisterFlagCompletionFunc("source", cobra.FixedCompletions(importSources, cobra.ShellCompDirectiveNoFileComp))
	_ = create.RegisterFlagCompletionFunc("mode", cobra.FixedCompletions([]string{"preview", "commit"}, cobra.ShellCompDirectiveNoFileComp))
	_ = create.RegisterFlagCompletionFunc("state", cobra.FixedCompletions([]string{"open", "closed", "all"}, cobra.ShellCompDirectiveNoFileComp))
	return create
}

func newImportsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <job-id>",
		Short: "Show one import job (including preview candidates)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.GetImportJobResponse
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/imports/"+args[0]), nil, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			j := resp.Job
			errMsg := "—"
			if j.LastError != nil {
				errMsg = *j.LastError
			}
			A.out.Table([]string{"Field", "Value"}, [][]string{
				{"ID", j.ID},
				{"Source", j.Source},
				{"Mode", j.Mode},
				{"Status", j.Status},
				{"Attempts", strconv.Itoa(j.Attempts)},
				{"Error", errMsg},
				{"Created", j.CreatedAt},
				{"Updated", j.UpdatedAt},
			})
			return nil
		},
	}
}

func newImportsCancelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel <job-id>",
		Short: "Cancel a queued or running import job",
		Long: `Cancel a queued or running import job.

A canceled job cannot be resumed; re-run the import from scratch instead.
On an interactive terminal you are asked to confirm before anything is
sent; non-interactive callers must pass --yes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirmDestructive(cmd, fmt.Sprintf("cancel import job %q", args[0])); err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/imports/"+args[0]+"/cancel"), nil, nil, nil); err != nil {
				return err
			}
			if !A.structured() {
				A.out.Printf("✓ Canceled import %s", args[0])
			}
			return nil
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	return cmd
}

func newImportsRerunCmd() *cobra.Command {
	var mode string
	var includeDuplicates bool
	rerun := &cobra.Command{
		Use:   "rerun <job-id>",
		Short: "Re-run an import job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("mode") {
				body["mode"] = mode
			}
			if cmd.Flags().Changed("include-duplicates") {
				body["includeDuplicates"] = includeDuplicates
			}
			var resp api.GetImportJobResponse
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/imports/"+args[0]+"/rerun"), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Printf("✓ Re-running import %s (status %s)", resp.Job.ID, resp.Job.Status)
			return nil
		},
	}
	rerun.Flags().StringVar(&mode, "mode", "", "Override the mode: preview or commit")
	rerun.Flags().BoolVar(&includeDuplicates, "include-duplicates", false, "Also create duplicate-looking requests")
	return rerun
}
