package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

// query builds URL query values for GET requests.
func query(m map[string]string) url.Values {
	q := url.Values{}
	for k, v := range m {
		q.Set(k, v)
	}
	return q
}

func newChangelogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "changelog",
		Short: "Manage changelog entries (drafts, scheduling, publishing)",
		Long: `Manage changelog entries (drafts, scheduling, publishing).

Creating, editing, listing, and deleting drafts works for every workspace
role and with cpt_ API tokens. Publishing, --publish-now, and setting a
schedule with --schedule-at <datetime> require the changelog.publish
capability (workspace admin or owner, SEC-40), and no CLI credential
qualifies — personal access tokens and OAuth logins are both cpt_ tokens,
so they fail with 403 interactive_session_required; publish in the Console
web UI. For member-role callers they fail with 403 capability_required.
Passing --schedule-at "" sends scheduledAt: null, which only clears an
existing schedule and needs plain content.manage (no admin role required).
A schedule value must be the strict UTC datetime form the server's schema
enforces, e.g. 2026-11-05T09:00:00Z (fractional seconds allowed): date-only
strings, free text, and numeric offsets like +02:00 are rejected locally
before any request.
--body and --body-file are mutually exclusive: both carry the entry body,
and passing both would silently send the file's content while dropping the
--body value, so the CLI refuses the pair before any request.`,
	}
	cmd.AddCommand(
		newChangelogListCmd(),
		newChangelogCreateCmd(),
		newChangelogUpdateCmd(),
		newChangelogDeleteCmd(),
		newChangelogPublishCmd(),
		newChangelogUnpublishCmd(),
	)
	return cmd
}

func newChangelogListCmd() *cobra.Command {
	var limit, offset int
	list := &cobra.Command{
		Use:                   "list",
		Short:                 "List changelog entries of the app",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, appID, err := resolveAppScope(cmd)
			if err != nil {
				return err
			}
			// The server clamps the page size to 100 silently; clamp here
			// too so an offset walk steps by the served size (issue #201).
			limit = A.clampListLimit(limit, changelogPageSize)
			// The console listing clamps the offset at 10,000 and still
			// answers 200; announce the rewrite so a deep walk notices it is
			// re-reading the final page (issue #178).
			offset = A.clampListOffset(offset)
			var resp api.ListChangelogResponse
			q := query(map[string]string{
				"appId":  appID,
				"limit":  strconv.Itoa(limit),
				"offset": strconv.Itoa(offset),
			})
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/changelog"), q, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			rows := make([][]string, 0, len(resp.Entries))
			for _, e := range resp.Entries {
				state := "draft"
				switch {
				case e.ScheduledAt != nil:
					state = "scheduled " + cutDate(*e.ScheduledAt)
				case e.PublishedAt != nil:
					state = "published " + cutDate(*e.PublishedAt)
				}
				rows = append(rows, []string{
					shortID(e.ID), truncate(e.Title, 44), orDash(deref(e.VersionLabel)),
					state, fmt.Sprintf("%d", len(e.LinkedRequests)), fmt.Sprintf("%d", e.SubscriberCount),
				})
			}
			A.out.Table([]string{"ID", "Title", "Version", "State", "Linked", "Subscribers"}, rows)
			if resp.HasMore {
				// At the server's offset cap the suggested next offset would
				// clamp right back to the same page forever (issue #408), so
				// the hint is replaced with the boundary disclosure instead
				// of instructing the caller into that loop.
				if offset < consoleListMaxOffset {
					A.out.Printf("Showing %d of %d entries — re-run with --offset %d for the next page.",
						len(resp.Entries), resp.Total, offset+len(resp.Entries))
				} else {
					A.out.Printf("Showing %d of %d entries — this listing ends at the server's %d-entry offset cap; the remaining entries are unreachable through offset paging.",
						len(resp.Entries), resp.Total, consoleListMaxOffset)
				}
			}
			return nil
		},
	}
	list.Flags().IntVar(&limit, "limit", 100, "Entries per page (1-100, server page cap 100)")
	list.Flags().IntVar(&offset, "offset", 0, "Entries to skip for paging (values above 10000 are clamped to 10000 by the server)")
	return list
}

// changelogPageSize is the changelog listing's server page cap (the route's
// parseListPagination maxLimit); the list command clamps --limit to it.
const changelogPageSize = 100

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// maxChangelogResolvePages caps ID-resolution paging: 101 pages of the
// listing's 100-entry page cap covers offsets 0 through 10,000 — the whole
// reachable universe, since the server clamps offset at the shared
// 10,000-entry cap and still answers 200 with the final page (issue #178).
// The +1 matters for correctness, not thrift (PR #414 review): without it
// the offset-10,000 page is never scanned, and a prefix matching both an
// entry in the scanned window and one on that final page would wrongly
// resolve to the scanned entry — a delete/publish acting on the wrong row.
const maxChangelogResolvePages = consoleListMaxOffset/changelogPageSize + 1

// fetchOneChangelogEntry finds an entry by exact ID or unique ID prefix
// (issue #413): the list table prints 12-character IDs via shortID, and the
// write commands used to take only the full form, so every copy-from-table
// update/delete/publish/unpublish was a guaranteed 404.
//
// Resolution walks the changelog listing paged at the server's 100-entry
// page cap, scoped to the resolved app (--app flag, else the saved default
// from 'apps use') like 'changelog list'; with no app resolved it stays
// workspace-wide, which the listing route serves when appId is omitted.
// Matching, ambiguity, and the page-cap guard live in the shared resolveIDRef.
func fetchOneChangelogEntry(ctx context.Context, ref string) (*api.ChangelogEntry, error) {
	ws, err := workspaceClient(ctx)
	if err != nil {
		return nil, err
	}
	appID := A.optionalAppID()
	return resolveIDRef(ref, idRefNouns{what: "changelog entry", plural: "entries", listHint: "cupthread changelog list"}, maxChangelogResolvePages, true,
		func(page int) ([]api.ChangelogEntry, bool, error) {
			offset := page * changelogPageSize
			q := query(map[string]string{
				"limit":  strconv.Itoa(changelogPageSize),
				"offset": strconv.Itoa(offset),
			})
			if appID != "" {
				q.Set("appId", appID)
			}
			var resp api.ListChangelogResponse
			if err := A.client.Do(ctx, "GET", wsPath(ws, "/changelog"), q, nil, &resp); err != nil {
				return nil, false, err
			}
			return resp.Entries, len(resp.Entries) < changelogPageSize || offset+len(resp.Entries) >= resp.Total, nil
		},
		changelogEntryNotFound(ws, appID),
		func(e *api.ChangelogEntry) string { return e.ID },
	)
}

// changelogEntryNotFound mirrors the features not-found contract: it names
// the scope the scan actually covered — the resolved app, or the workspace —
// and the --app escape hatch, so an ID copied from the wrong scope is
// diagnosable instead of reading as a missing entry.
func changelogEntryNotFound(ws, appID string) func(ref string, scanned int, exhausted bool, prefixMatches int) error {
	return func(ref string, scanned int, exhausted bool, prefixMatches int) error {
		if appID != "" {
			if !exhausted {
				return fmt.Errorf("changelog entry %q not found in the first %d entries of app %s (resolution scans at most %d pages); pass --app <id> to search another app", ref, scanned, appID, maxChangelogResolvePages)
			}
			return fmt.Errorf("changelog entry %q not found among the %d entries of app %s in workspace %s (resolution is scoped to the resolved app: pass --app <id> to search another app, or run 'cupthread apps use' to change the default)", ref, scanned, appID, ws)
		}
		if !exhausted {
			return fmt.Errorf("changelog entry %q not found in the first %d entries (resolution scans at most %d pages); narrow the search with --app", ref, scanned, maxChangelogResolvePages)
		}
		return fmt.Errorf("changelog entry %q not found (scanned %d entries in workspace %s)", ref, scanned, ws)
	}
}

// changelogBodyLimit mirrors the shared changelog schema's body intake bound
// (z.string().max(20000)); the generic 1 MB console cap still governs how
// much --body-file may read, but a body past the server's 20000-character
// bound can never succeed and fails here instead of as a guaranteed 400
// (issue #341). Length is counted in runes like #336 settled.
const changelogBodyLimit = 20000

// zodDatetimePattern mirrors z.string().datetime()'s default (offset=false)
// shape exactly: only the strict UTC form YYYY-MM-DDTHH:MM:SS(.fff...)Z
// passes the shared changelog schema's scheduledAt check. This is stricter
// than Go's time.RFC3339, which also accepts numeric offsets — zod rejects
// those, so a plain RFC3339 parse would still let guaranteed-400 values
// through (issue #349).
var zodDatetimePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$`)

// validateUTCDatetimeFlag rejects a datetime flag value the server's zod
// .datetime() check can never accept, naming the flag, so the write fails
// locally instead of as a guaranteed 400 Validation failed (issue #349).
// The shape regex is zod's exact default pattern; time.Parse then rejects
// impossible calendar datetimes the shape alone would accept (month 13,
// February 30th, leap seconds).
func validateUTCDatetimeFlag(flag, value string) error {
	if !zodDatetimePattern.MatchString(value) {
		return fmt.Errorf("invalid --%s %q: must be a UTC ISO 8601 datetime (e.g. 2026-11-05T09:00:00Z)", flag, value)
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return fmt.Errorf("invalid --%s %q: must be a real calendar datetime in UTC form (e.g. 2026-11-05T09:00:00Z)", flag, value)
	}
	return nil
}

// validateChangelogWriteFlags mirrors Create/UpdateChangelogEntryInputSchema's
// zod intake bounds (title 3-160, versionLabel 1-64, linkedRequestIds at most
// 50 of 1+ characters each, scheduledAt a strict UTC datetime) so a rejected
// write fails locally naming the flag instead of as a guaranteed 400
// (issues #341, #349). On create the flags are always sent and checked
// regardless of cobra's Changed; on update only a passed flag is checked.
// Clearing --version-label with "" on update is legal (sent as null), so only
// non-empty values are length-checked; the same holds for --schedule-at,
// whose "" is the documented clear form and is simply omitted on create.
func validateChangelogWriteFlags(cmd *cobra.Command, title, versionLabel, scheduleAt string, linkIDs []string, creating bool) error {
	if creating || cmd.Flags().Changed("title") {
		if n := utf8.RuneCountInString(title); n < 3 || n > 160 {
			return fmt.Errorf("invalid --title: must be 3-160 characters, got %d", n)
		}
	}
	if versionLabel != "" && (creating || cmd.Flags().Changed("version-label")) {
		if n := utf8.RuneCountInString(versionLabel); n > 64 {
			return fmt.Errorf("invalid --version-label: must be 1-64 characters, got %d", n)
		}
	}
	if scheduleAt != "" && (creating || cmd.Flags().Changed("schedule-at")) {
		if err := validateUTCDatetimeFlag("schedule-at", scheduleAt); err != nil {
			return err
		}
	}
	if len(linkIDs) > 50 {
		return fmt.Errorf("invalid --link-request-ids: at most 50 request IDs, got %d", len(linkIDs))
	}
	for _, id := range linkIDs {
		if id == "" {
			return errors.New(`invalid --link-request-ids: request IDs cannot be empty (check for a stray comma)`)
		}
	}
	return nil
}

// resolveLinkRequestIDs resolves each --link-request-ids entry through the
// features family's exact-or-unique-prefix resolution before the write
// (issue #416): the flag is a *body field* of cross-family IDs, so unlike
// the changelog entry arguments it used to be forwarded verbatim — and the
// 12-character forms every features table prints could never match the
// server's app-scoped `id IN (...)`, which silently drops unmatched IDs
// from the saved entry. An error names the offending list entry and fails
// before any write; full IDs keep their fast path (an exact match returns
// on the first listing page).
func resolveLinkRequestIDs(ctx context.Context, refs []string) ([]string, error) {
	resolved := make([]string, len(refs))
	for i, ref := range refs {
		r, err := fetchOneFeatureRequest(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("invalid --link-request-ids entry %q: %w", ref, err)
		}
		resolved[i] = r.ID
	}
	return resolved, nil
}

// warnUnlinkedRequests discloses IDs the server silently dropped from the
// saved entry's links (issue #416): the create/update routes answer 200 and
// simply omit unknown, cross-app, or otherwise unmatched request IDs, which
// made the partial link set indistinguishable from a complete one — and the
// close-the-loop voter blast quietly never reached the dropped requests'
// voters. warnf keeps the structured stdout document byte-faithful (the
// advisory rides stderr in --json/-o yaml mode).
func warnUnlinkedRequests(sent []string, linked []api.LinkedRequest) {
	linkedIDs := make(map[string]bool, len(linked))
	for _, l := range linked {
		linkedIDs[l.ID] = true
	}
	var dropped []string
	for _, id := range sent {
		if !linkedIDs[id] {
			dropped = append(dropped, id)
		}
	}
	if len(dropped) == 0 {
		return
	}
	A.warnf("⚠ %d of the --link-request-ids were not linked (unknown or not part of this app): %s", len(dropped), strings.Join(dropped, ", "))
}

func newChangelogCreateCmd() *cobra.Command {
	var title, bodyText, bodyFile, versionLabel, versionID, scheduleAt string
	var linkIDs []string
	var publishNow bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a changelog entry (draft by default)",
		Long: `Create a changelog entry (draft by default).

--publish-now and --schedule-at are mutually exclusive: the server silently
resolves the contradictory pair in favor of --publish-now, which would
publish the entry immediately and notify subscribers while dropping the
requested schedule, so the CLI refuses the pair before any request.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// --body and --body-file both write the entry body, and
			// resolveBody silently resolved the contradictory pair in favor
			// of --body-file (issue #426): the --body value was dropped with
			// exit code 0, so with --publish-now the stale draft file was
			// published — and subscribers notified — instead of the corrected
			// inline text. Refuse the pair before any request and before the
			// other flag validations, mirroring the #369 guard's ordering
			// rationale: a caller fixing a bad --title or --schedule-at would
			// otherwise hit this same refusal on the next run. Unlike that
			// pair, neither body flag has an "empty means omitted" form — a
			// passed flag always carries the intended body — so this checks
			// cobra's Changed rather than the values.
			if cmd.Flags().Changed("body") && cmd.Flags().Changed("body-file") {
				return errors.New("--body and --body-file are mutually exclusive; pass one (previously --body-file silently won and --body was dropped)")
			}
			if title == "" {
				return errors.New("--title is required")
			}
			// --publish-now together with a non-empty --schedule-at is
			// refused locally (issue #369): the server resolves the pair in
			// favor of --publish-now (createChangelogEntry drops scheduledAt
			// when publishNow is set), so the entry would publish
			// immediately and notify subscribers — the exact premature
			// announcement --schedule-at exists to avoid — with exit code 0.
			// Refuse before any request, like the --enable/--disable and
			// --events/--all-events guards in notifications prefs set, and
			// before the flag validations so the conflict is reported first
			// (a caller fixing a bad datetime would otherwise hit this same
			// refusal on the next run). A passed-but-empty --schedule-at is
			// no contradiction: create simply omits "", so the invocation is
			// just --publish-now — hence the value check instead of Changed.
			if publishNow && scheduleAt != "" {
				return errors.New("--publish-now and --schedule-at are mutually exclusive; pass one")
			}
			text, err := resolveBody(cmd, bodyText, bodyFile)
			if err != nil {
				if perr := A.reportInputTooLarge(err); perr != nil {
					return perr
				}
				return err
			}
			if err := validateChangelogWriteFlags(cmd, title, versionLabel, scheduleAt, linkIDs, true); err != nil {
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
			// Resolve the cross-family --link-request-ids entries before the
			// write (issue #416): the server silently drops any ID it cannot
			// match, so a truncated table form sent verbatim would save an
			// entry quietly missing its links.
			linkIDs, err = resolveLinkRequestIDs(cmd.Context(), linkIDs)
			if err != nil {
				return err
			}
			body := map[string]any{
				"appId": appID, "title": title, "body": text,
				"linkedRequestIds": orEmptySlice(linkIDs), "publishNow": publishNow,
			}
			if versionLabel != "" {
				body["versionLabel"] = versionLabel
			}
			if versionID != "" {
				body["versionId"] = versionID
			}
			if scheduleAt != "" {
				body["scheduledAt"] = scheduleAt
			}
			var resp api.ChangelogEntry
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/changelog"), nil, body, &resp); err != nil {
				return err
			}
			warnUnlinkedRequests(linkIDs, resp.LinkedRequests)
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.PrintfSafe("✓ Created changelog entry %s", resp.ID)
			return nil
		},
	}
	create.Flags().StringVar(&title, "title", "", "Entry title (required)")
	create.Flags().StringVar(&bodyText, "body", "", "Markdown body (mutually exclusive with --body-file)")
	create.Flags().StringVar(&bodyFile, "body-file", "", "Read the markdown body from a file path, \"@path\", or \"-\"/\"@\" for stdin (max 1 MB; mutually exclusive with --body)")
	create.Flags().StringVar(&versionLabel, "version-label", "", "Version label to show, e.g. 1.2.0")
	create.Flags().StringVar(&versionID, "version-id", "", "Linked version ID")
	create.Flags().StringSliceVar(&linkIDs, "link-request-ids", nil, "Comma-separated feature request IDs to close the loop (full IDs or unique prefixes, resolved like 'features get')")
	create.Flags().BoolVar(&publishNow, "publish-now", false, "Publish immediately (mutually exclusive with --schedule-at)")
	create.Flags().StringVar(&scheduleAt, "schedule-at", "", "Schedule publishing (strict UTC ISO 8601 datetime, e.g. 2026-11-05T09:00:00Z; mutually exclusive with --publish-now)")
	return create
}

// resolveBody returns the markdown body from --body or --body-file. Callers
// refuse the contradictory --body + --body-file pair before calling (issue
// #426), so resolveBody only ever sees one of the two flags Changed; it used
// to resolve that pair silently in favor of --body-file. The file
// form is capped at the console route's 1 MB request-body limit, and both
// forms are capped at the server's 20000-character intake bound
// (changelogBodyLimit) — a bigger body can never be accepted and fails here
// naming the flag it came from instead of as a guaranteed 400 (issue #341).
// An explicitly passed blank --body-file fails locally instead of falling
// through to --body (issue #293): the fall-through turned `--body-file
// "$UNSET_VAR"` into body "" — a silent wipe of the entry body on update, an
// empty-bodied entry on create — while "" is documented as a deliberate clear
// only for --version-label and --schedule-at, never for the body.
func resolveBody(cmd *cobra.Command, bodyText, bodyFile string) (string, error) {
	if cmd.Flags().Changed("body-file") {
		if strings.TrimSpace(bodyFile) == "" {
			return "", errors.New(`--body-file is empty — pass a file path, "@path", or "-" for stdin`)
		}
		data, err := readInputFile(cmd.Context(), bodyFile, maxConsoleBodyBytes)
		if err != nil {
			return "", flagInputError("body-file", err)
		}
		if err := checkChangelogBodyLimit("body-file", string(data)); err != nil {
			return "", err
		}
		return string(data), nil
	}
	if err := checkChangelogBodyLimit("body", bodyText); err != nil {
		return "", err
	}
	return bodyText, nil
}

// checkChangelogBodyLimit rejects a resolved body over the shared schema's
// 20000-character intake bound (issue #341).
func checkChangelogBodyLimit(flagName, text string) error {
	if n := utf8.RuneCountInString(text); n > changelogBodyLimit {
		return fmt.Errorf("invalid --%s: the changelog body limit is %d characters, got %d", flagName, changelogBodyLimit, n)
	}
	return nil
}

func newChangelogUpdateCmd() *cobra.Command {
	var title, bodyText, bodyFile, versionLabel, versionID, scheduleAt string
	var linkIDs []string
	update := &cobra.Command{
		Use:   "update <entry-id>",
		Short: "Update a changelog entry",
		Long: `Update a changelog entry.

--body and --body-file are mutually exclusive: both carry the new body, and
passing both would silently save the file's content while dropping the
--body value, so the CLI refuses the pair before the ID resolution and any
request.`,
		Args: exactNonBlankArg("changelog entry id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Same contradictory-pair refusal as create (issue #426):
			// resolveBody silently favored --body-file, so an update passing
			// both saved the file's — possibly older — text over the intended
			// --body correction with exit code 0. Refuse before the flag
			// validations and before the ID-resolution round trips (the
			// validate-before-lookup discipline of #341).
			if cmd.Flags().Changed("body") && cmd.Flags().Changed("body-file") {
				return errors.New("--body and --body-file are mutually exclusive; pass one (previously --body-file silently won and --body was dropped)")
			}
			if err := validateChangelogWriteFlags(cmd, title, versionLabel, scheduleAt, linkIDs, false); err != nil {
				return err
			}
			// Resolve the body before the ID resolution: an unreadable or
			// oversized body must fail locally without burning the listing
			// round trips (the validate-before-lookup discipline of #341).
			text := ""
			if cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") {
				resolved, err := resolveBody(cmd, bodyText, bodyFile)
				if err != nil {
					if perr := A.reportInputTooLarge(err); perr != nil {
						return perr
					}
					return err
				}
				text = resolved
			}
			e, err := fetchOneChangelogEntry(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			// Resolve the cross-family --link-request-ids entries before the
			// write (issue #416, mirroring create): only when the flag is
			// passed — an untouched flag means the caller is not touching the
			// links, so the pre-existing set must not trigger the drop check.
			changedLinks := cmd.Flags().Changed("link-request-ids")
			if changedLinks {
				linkIDs, err = resolveLinkRequestIDs(cmd.Context(), linkIDs)
				if err != nil {
					return err
				}
			}
			body := map[string]any{}
			if cmd.Flags().Changed("title") {
				body["title"] = title
			}
			if cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") {
				body["body"] = text
			}
			if cmd.Flags().Changed("version-label") {
				body["versionLabel"] = nilIfEmpty(versionLabel)
			}
			if cmd.Flags().Changed("version-id") {
				body["versionId"] = nilIfEmpty(versionID)
			}
			if cmd.Flags().Changed("link-request-ids") {
				body["linkedRequestIds"] = orEmptySlice(linkIDs)
			}
			if cmd.Flags().Changed("schedule-at") {
				body["scheduledAt"] = nilIfEmpty(scheduleAt)
			}
			if len(body) == 0 {
				return errors.New("nothing to update: pass at least one flag")
			}
			var resp api.ChangelogEntry
			if err := A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/changelog/"+pathID(e.ID)), nil, body, &resp); err != nil {
				return err
			}
			if changedLinks {
				warnUnlinkedRequests(linkIDs, resp.LinkedRequests)
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.PrintfSafe("✓ Updated changelog entry %s", resp.ID)
			return nil
		},
	}
	update.Flags().StringVar(&title, "title", "", "New title")
	update.Flags().StringVar(&bodyText, "body", "", "New markdown body (mutually exclusive with --body-file)")
	update.Flags().StringVar(&bodyFile, "body-file", "", "Read the new body from a file path, \"@path\", or \"-\"/\"@\" for stdin (max 1 MB; mutually exclusive with --body)")
	update.Flags().StringVar(&versionLabel, "version-label", "", "Version label (\"\" clears it)")
	update.Flags().StringVar(&versionID, "version-id", "", "Linked version ID (\"\" clears it)")
	update.Flags().StringSliceVar(&linkIDs, "link-request-ids", nil, "Feature request IDs to link (full IDs or unique prefixes, resolved like 'features get')")
	update.Flags().StringVar(&scheduleAt, "schedule-at", "", "Schedule publishing (strict UTC ISO 8601 datetime, e.g. 2026-11-05T09:00:00Z; \"\" clears it)")
	return update
}

func newChangelogDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <entry-id>",
		Aliases: []string{"rm"},
		Short:   "Delete a changelog entry",
		Long: `Delete a changelog entry permanently, including its links.

The server hard-deletes the entry and it cannot be restored (unpublish is
the reversible alternative). On an interactive terminal you are asked to
confirm before anything is sent; non-interactive callers must pass --yes.`,
		Args: exactNonBlankArg("changelog entry id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirmDestructive(cmd, fmt.Sprintf("permanently delete changelog entry %q", args[0])); err != nil {
				return err
			}
			e, err := fetchOneChangelogEntry(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "DELETE", wsPath(ws, "/changelog/"+pathID(e.ID)), nil, nil, nil); err != nil {
				return err
			}
			if A.structured() {
				return A.emitMutationResult("deleted", e.ID)
			}
			A.out.PrintfSafe("✓ Deleted changelog entry %s", e.ID)
			return nil
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	return cmd
}

func newChangelogPublishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "publish <entry-id>",
		Short: "Publish a changelog entry now",
		Args:  exactNonBlankArg("changelog entry id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := fetchOneChangelogEntry(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.ChangelogEntry
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/changelog/"+pathID(e.ID)+"/publish"), nil, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.PrintfSafe("✓ Published %s", resp.ID)
			return nil
		},
	}
}

func newChangelogUnpublishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unpublish <entry-id>",
		Short: "Revert a changelog entry to draft",
		Args:  exactNonBlankArg("changelog entry id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := fetchOneChangelogEntry(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/changelog/"+pathID(e.ID)+"/unpublish"), nil, nil, nil); err != nil {
				return err
			}
			if A.structured() {
				return A.emitMutationResult("unpublished", e.ID)
			}
			A.out.PrintfSafe("✓ Unpublished %s", e.ID)
			return nil
		},
	}
}

// orEmptySlice ensures a JSON array (not null) for slice flags that were set.
func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
