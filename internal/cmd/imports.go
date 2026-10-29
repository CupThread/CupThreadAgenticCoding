package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

var importSources = []string{"github_issues", "github_discussions", "linear", "notion", "slack"}

// validateImportOptionsFlags mirrors ImportOptionsSchema's zod intake bounds
// (owner/repo/categorySlug at most 100 characters, labels 1-60 characters
// each with at most 10 entries, state enum, limit 1-200, columnSlug at most
// 60) so a value the server always rejects fails locally naming the flag
// (issue #345). Only values the command actually sends are checked: empty
// scalars are omitted from the body and the raw --options JSON stays the
// documented verbatim escape hatch.
func validateImportOptionsFlags(owner, repo string, labels []string, state, categorySlug string, limit int, limitSet bool, columnSlug string) error {
	if owner != "" {
		if n := utf8.RuneCountInString(owner); n > 100 {
			return fmt.Errorf("invalid --owner: must be 1-100 characters, got %d", n)
		}
	}
	if repo != "" {
		if n := utf8.RuneCountInString(repo); n > 100 {
			return fmt.Errorf("invalid --repo: must be 1-100 characters, got %d", n)
		}
	}
	if len(labels) > 0 {
		if len(labels) > 10 {
			return fmt.Errorf("invalid --labels: at most 10 labels, got %d", len(labels))
		}
		for _, label := range labels {
			if n := utf8.RuneCountInString(label); n < 1 || n > 60 {
				return fmt.Errorf("invalid --labels: each label must be 1-60 characters, got %d", n)
			}
		}
	}
	if state != "" && state != "open" && state != "closed" && state != "all" {
		return fmt.Errorf("invalid --state %q: must be open, closed or all", state)
	}
	if categorySlug != "" {
		if n := utf8.RuneCountInString(categorySlug); n > 100 {
			return fmt.Errorf("invalid --category-slug: must be 1-100 characters, got %d", n)
		}
	}
	if limitSet && (limit < 1 || limit > 200) {
		return fmt.Errorf("invalid --limit: must be 1-200, got %d", limit)
	}
	if columnSlug != "" {
		if n := utf8.RuneCountInString(columnSlug); n > 60 {
			return fmt.Errorf("invalid --column-slug: must be 1-60 characters, got %d", n)
		}
	}
	return nil
}

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

// printImportJobsList renders the jobs table and, when the route answers a
// total, the sibling (N shown, M total) footer every paginated listing
// prints (issue #346).
func printImportJobsList(resp *api.ListImportJobsResponse) {
	printImportJobs(resp.Jobs)
	if resp.Total != nil {
		A.out.Printf("(%d shown, %d total)", len(resp.Jobs), *resp.Total)
	}
}

// importsAppPageSize is the app-scoped imports listing's hardcoded server
// window: the route passes no limit to listImportJobsInWorkspace, so the
// shared helper's default cap of 50 applies (SaaS apps/api/src/lib/imports.ts,
// ORDER BY created_at DESC LIMIT ?; issue #346).
const importsAppPageSize = 50

// importsWorkspacePageSize is the workspace-scoped imports history listing's
// hardcoded server cap (SaaS apps/api/src/domains/imports/routes.ts; issue
// #346).
const importsWorkspacePageSize = 100

// importsListCapNotice / importsHistoryCapNotice are the cap-keyed
// advisories; Sprintf keeps their numbers tied to the page-size constants so
// message and trigger cannot drift apart (issue #346).
var (
	importsListCapNotice = fmt.Sprintf(
		"Import job list may be truncated: this listing serves only the newest %d jobs (server cap, no paging or total); use 'cupthread imports history' for the workspace-wide %d-job view",
		importsAppPageSize, importsWorkspacePageSize)
	importsHistoryCapNotice = fmt.Sprintf(
		"Import job history may be truncated: this listing serves only the newest %d jobs (server cap, no paging or total); older jobs — including failed ones worth re-running — are not shown",
		importsWorkspacePageSize)
)

// warnImportJobsTruncation discloses the imports listings' server-side
// windows so a full page is never indistinguishable from the whole set
// (issue #346, the #352 disclosure discipline). Neither route sends a total
// today, so a cap-sized answer is the only honest truncation signal and
// carries a "may be" (a workspace with exactly 50 or 100 jobs trips it too);
// once the SaaS side adds total, the notice keys off total > shown instead.
// warnf routes the notice: stdout in table mode, stderr in --json/-o yaml
// mode so the structured document stays byte-faithful.
func (a *app) warnImportJobsTruncation(resp *api.ListImportJobsResponse, pageSize int, capNotice string) {
	switch {
	case resp.Total != nil && *resp.Total > len(resp.Jobs):
		a.warnf("⚠ Import jobs are truncated: showing the %d newest of %d jobs; older jobs (including failed ones) are not retrievable through this listing", len(resp.Jobs), *resp.Total)
	case resp.Total == nil && len(resp.Jobs) >= pageSize:
		a.warnf("⚠ %s", capNotice)
	}
}

func newImportsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List import jobs of the app (newest first, server cap 50)",
		Long: `List import jobs of the app (newest first).

The server caps this listing at the newest 50 jobs and offers no paging or
total, so older jobs — including failed ones worth re-running — can be
missing from the view. When the cap is hit the command says so, in table
mode on stdout and in --json/-o yaml mode on stderr. 'cupthread imports
history' widens the window to the newest 100 jobs across the workspace.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, appID, err := resolveAppScope(cmd)
			if err != nil {
				return err
			}
			var resp api.ListImportJobsResponse
			path := appPath(ws, appID, "/imports")
			if err := A.client.Do(cmd.Context(), "GET", path, nil, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				if err := A.out.Structured(resp); err != nil {
					return err
				}
				A.warnImportJobsTruncation(&resp, importsAppPageSize, importsListCapNotice)
				return nil
			}
			printImportJobsList(&resp)
			A.warnImportJobsTruncation(&resp, importsAppPageSize, importsListCapNotice)
			return nil
		},
	}
}

func newImportsHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history",
		Short: "List all import jobs in the workspace (newest first, server cap 100)",
		Long: `List all import jobs in the workspace (newest first).

The server caps this listing at the newest 100 jobs and offers no paging or
total, so older jobs — including failed ones worth re-running — can be
missing from the view. When the cap is hit the command says so, in table
mode on stdout and in --json/-o yaml mode on stderr.`,
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
				if err := A.out.Structured(resp); err != nil {
					return err
				}
				A.warnImportJobsTruncation(&resp, importsWorkspacePageSize, importsHistoryCapNotice)
				return nil
			}
			printImportJobsList(&resp)
			A.warnImportJobsTruncation(&resp, importsWorkspacePageSize, importsHistoryCapNotice)
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
run on the queue and actually create feature requests. Every import source
requires Pro (402 tier_limit_import_pro). The subscription tier is only
free or pro.

Pass --options (a file path, "@path", or "-" / "@" for stdin) to send the
raw ImportOptions JSON instead of the per-source flags.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if source == "" {
				return errors.New("--source is required (github_issues, github_discussions, linear, notion or slack)")
			}
			if !slices.Contains(importSources, source) {
				return fmt.Errorf("invalid --source %q: must be one of %s", source, strings.Join(importSources, ", "))
			}
			if mode != "preview" && mode != "commit" {
				return fmt.Errorf("invalid --mode %q: use preview or commit", mode)
			}

			// Read and validate --options before anything else: an oversized
			// or malformed body must fail locally, before any HTTP request
			// goes out (issue #142).
			//
			// json.RawMessage values keep every number in the --options body
			// byte-identical on the wire (issue #75); a map[string]any decode
			// would round-trip them through float64 and silently rewrite
			// integers a float64 cannot represent exactly.
			var options map[string]json.RawMessage
			if optionsFile != "" {
				data, err := readInputFile(cmd.Context(), optionsFile, maxConsoleBodyBytes)
				if err != nil {
					if perr := A.reportInputTooLarge(err); perr != nil {
						return perr
					}
					return flagInputError("options", err)
				}
				if err := json.Unmarshal(data, &options); err != nil {
					return fmt.Errorf("parse options JSON: %w", err)
				}
			} else {
				if err := validateImportOptionsFlags(owner, repo, labels, state, categorySlug, limit, cmd.Flags().Changed("limit"), columnSlug); err != nil {
					return err
				}
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

			appID, err := A.requireAppID()
			if err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}

			body := map[string]any{"appId": appID, "source": source, "mode": mode, "options": options}
			path := appPath(ws, appID, "/imports")
			var resp api.CreateImportJobResponse
			if err := A.client.Do(cmd.Context(), "POST", path, nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.PrintfSafe("✓ Import job %s (%s/%s, status %s)", resp.Job.ID, resp.Job.Source, resp.Job.Mode, resp.Job.Status)
			A.out.Printf("  Drain: %d processed, %d succeeded, %d failed", resp.Drain.Processed, resp.Drain.Succeeded, resp.Drain.Failed)
			if resp.Job.Status == "completed" && len(resp.Job.Candidates) > 0 {
				A.out.PrintfSafe("  Preview candidates: cupthread imports get %s --json | jq .job.candidates", resp.Job.ID)
			}
			return nil
		},
	}
	create.Flags().StringVar(&source, "source", "", "Import source (required)")
	create.Flags().StringVar(&mode, "mode", "preview", "preview shows the diff, commit creates requests")
	create.Flags().StringVar(&optionsFile, "options", "", "Raw ImportOptions JSON: file path, \"@path\", or \"-\"/\"@\" for stdin (max 1 MB), sent verbatim (numbers keep full precision); overrides per-source flags")
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
		Args:  exactNonBlankArg("import job id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.GetImportJobResponse
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/imports/"+pathID(args[0])), nil, nil, &resp); err != nil {
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
		Args: exactNonBlankArg("import job id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirmDestructive(cmd, fmt.Sprintf("cancel import job %q", args[0])); err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/imports/"+pathID(args[0])+"/cancel"), nil, nil, nil); err != nil {
				return err
			}
			if A.structured() {
				return A.emitMutationResult("canceled", args[0])
			}
			A.out.Printf("✓ Canceled import %s", args[0])
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
		Args:  exactNonBlankArg("import job id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// ImportModeSchema is a preview/commit enum: a Changed but invalid
			// --mode (including "") is a guaranteed 400, so fail locally
			// before any request (issue #345).
			if cmd.Flags().Changed("mode") && mode != "preview" && mode != "commit" {
				return fmt.Errorf("invalid --mode %q: use preview or commit", mode)
			}
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
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/imports/"+pathID(args[0])+"/rerun"), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.PrintfSafe("✓ Re-running import %s (status %s)", resp.Job.ID, resp.Job.Status)
			return nil
		},
	}
	rerun.Flags().StringVar(&mode, "mode", "", "Override the mode: preview or commit")
	rerun.Flags().BoolVar(&includeDuplicates, "include-duplicates", false, "Also create duplicate-looking requests")
	return rerun
}
