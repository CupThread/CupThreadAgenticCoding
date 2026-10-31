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

// notificationTypes mirrors the server's NotificationTypeSchema enum
// (SaaS packages/shared/src/schemas/notifications.ts) in enum order. It feeds
// --all-events, so a missing type silently suppresses that notification for
// the whole channel — keep it in sync with the enum listed in
// skills/cupthread-cli/SKILL.md (guarded by TestNotificationTypeEnumDrift).
var notificationTypes = []string{
	"feedback.received",
	"feature_request.submitted",
	"feature_request.approved",
	"feature_request.shipped",
	"comment.received",
	"vote.milestone",
	"changelog.published",
	"weekly.digest",
	"delivery.success",
	"delivery.failed",
	"import.completed",
	"import.failed",
	"subscription.updated",
	"system",
}

func newNotificationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notifications",
		Short: "Read and configure workspace notifications",
	}
	cmd.AddCommand(
		newNotificationsListCmd(),
		newNotificationsReadCmd(),
		newNotificationsReadAllCmd(),
		newNotificationPrefsCmd(),
	)
	return cmd
}

func newNotificationsListCmd() *cobra.Command {
	var limit, offset int
	list := &cobra.Command{
		Use:                   "list",
		Short:                 "List notifications (newest first)",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			// The server clamps the page size to 200 silently; clamp here
			// too so an offset walk steps by the served size (issue #201).
			limit = A.clampListLimit(limit, notificationPageSize)
			// The console listing clamps the offset at 10,000 and still
			// answers 200; announce the rewrite so a deep walk notices it is
			// re-reading the final page (issue #178).
			offset = A.clampListOffset(offset)
			q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
			var resp api.ListNotificationsResponse
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/notifications"), q, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			rows := make([][]string, 0, len(resp.Notifications))
			for _, n := range resp.Notifications {
				read := "unread"
				if n.ReadAt != nil {
					read = "read"
				}
				rows = append(rows, []string{
					shortID(n.ID), n.Type, truncate(n.Title, 44), read, cutDate(n.CreatedAt),
				})
			}
			A.out.Table([]string{"ID", "Type", "Title", "State", "Created"}, rows)
			// Always printed (even when total equals the page size) so the
			// line's presence is a stable part of the output shape, like
			// features list's trailers: `total` is the server's COUNT(*) over
			// the whole notification set, so total > shown is the truncation
			// signal (issue #410).
			A.out.Printf("(%d shown, %d total)", len(resp.Notifications), resp.Total)
			A.out.Printf("(%d unread)", resp.UnreadCount)
			return nil
		},
	}
	list.Flags().IntVar(&limit, "limit", 50, "Maximum notifications to list (1-200, server page cap 200)")
	list.Flags().IntVar(&offset, "offset", 0, "Offset for pagination (values above 10000 are clamped to 10000 by the server)")
	return list
}

// notificationPageSize is the notifications listing's server page cap (the
// route's parseListPagination maxLimit); the list command clamps --limit to it.
const notificationPageSize = 200

// maxNotificationResolvePages caps notifications read's ID-resolution paging
// the way maxChangelogResolvePages does: 51 pages of the listing's 200-entry
// page cap covers offsets 0 through 10,000 — the whole reachable universe,
// since the server clamps offset at the shared 10,000-entry cap and still
// answers 200 with the final page (issue #178). The +1 matters for
// correctness, not thrift (PR #414 review): without it the offset-10,000 page
// is never scanned, and a prefix matching both a notification in the scanned
// window and one on that final page would wrongly resolve to the scanned one.
const maxNotificationResolvePages = consoleListMaxOffset/notificationPageSize + 1

// markNotificationRead resolves a notification reference — the exact id or a
// unique prefix of one — and marks it read (issue #418): the list table
// prints 12-character ids via shortID, and `notifications read` used to
// forward the truncated form verbatim, which the server can only answer with
// a guaranteed 404.
//
// The exact id keeps its one-request fast path — today's single POST — and
// only its 404, the truncated reference's guaranteed answer (and equally the
// answer for a genuinely absent id), falls back to the workspace listing walk
// and a second POST at the resolved id, the fetchOneImportJob pattern. The
// probe POST is safe to fire first: the server matches exact ids only, so a
// truncated reference 404s without touching any row, and the API client never
// retries a 404. Matching, ambiguity, and the page-cap guard live in the
// shared resolveIDRef. Returns the id the successful POST used, so the ✓ line
// and the structured mutation record carry the resolved-full-id contract
// every other family follows.
func markNotificationRead(ctx context.Context, ws, ref string) (string, error) {
	fire := func(id string) error {
		return A.client.Do(ctx, "POST", wsPath(ws, "/notifications/"+pathID(id)+"/read"), nil, nil, nil)
	}
	if err := fire(ref); err == nil {
		return ref, nil
	} else if !apiNotFound(err) {
		return "", err
	}
	n, err := resolveIDRef(ref, idRefNouns{what: "notification", plural: "notifications", listHint: "cupthread notifications list"}, maxNotificationResolvePages, true,
		func(page int) ([]api.Notification, bool, error) {
			offset := page * notificationPageSize
			q := url.Values{"limit": {strconv.Itoa(notificationPageSize)}, "offset": {strconv.Itoa(offset)}}
			var resp api.ListNotificationsResponse
			if err := A.client.Do(ctx, "GET", wsPath(ws, "/notifications"), q, nil, &resp); err != nil {
				return nil, false, err
			}
			return resp.Notifications, len(resp.Notifications) < notificationPageSize || offset+len(resp.Notifications) >= resp.Total, nil
		},
		notificationNotFound(ws),
		func(n *api.Notification) string { return n.ID },
	)
	if err != nil {
		return "", err
	}
	if err := fire(n.ID); err != nil {
		return "", err
	}
	return n.ID, nil
}

// notificationNotFound mirrors the changelog not-found contract: it names the
// scope the scan actually covered — the workspace, the notifications
// listing's only scope — and the listing the table ids come from.
func notificationNotFound(ws string) func(ref string, scanned int, exhausted bool, prefixMatches int) error {
	return func(ref string, scanned int, exhausted bool, prefixMatches int) error {
		if !exhausted {
			return fmt.Errorf("notification %q not found in the first %d notifications (resolution scans at most %d pages)", ref, scanned, maxNotificationResolvePages)
		}
		return fmt.Errorf("notification %q not found (scanned %d notifications in workspace %s)", ref, scanned, ws)
	}
}

func newNotificationsReadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "read <notification-id>",
		Short: "Mark one notification as read",
		Args:  exactNonBlankArg("notification id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			id, err := markNotificationRead(cmd.Context(), ws, args[0])
			if err != nil {
				return err
			}
			if A.structured() {
				return A.emitMutationResult("read", id)
			}
			A.out.Printf("✓ Marked %s as read", id)
			return nil
		},
	}
}

func newNotificationsReadAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:                   "read-all",
		Short:                 "Mark every notification as read",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/notifications/read-all"), nil, nil, nil); err != nil {
				return err
			}
			if A.structured() {
				return A.emitMutationResult("read_all", "")
			}
			A.out.Printf("✓ Marked all notifications as read")
			return nil
		},
	}
}

func newNotificationPrefsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "prefs", Short: "Per-channel notification preferences"}
	cmd.AddCommand(
		&cobra.Command{
			Use:                   "show",
			Short:                 "Show notification preferences",
			DisableFlagsInUseLine: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				ws, err := workspaceClient(cmd.Context())
				if err != nil {
					return err
				}
				var resp api.ListNotificationPrefsResponse
				if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/notification-prefs"), nil, nil, &resp); err != nil {
					return err
				}
				if A.structured() {
					return A.out.Structured(resp)
				}
				rows := make([][]string, 0, len(resp.Prefs))
				for _, p := range resp.Prefs {
					rows = append(rows, []string{p.Channel, boolYesNo(p.Enabled), truncate(fmt.Sprint(p.EventMask), 80)})
				}
				A.out.Table([]string{"Channel", "Enabled", "Events"}, rows)
				return nil
			},
		},
		newNotificationPrefsSetCmd(),
	)
	return cmd
}

func newNotificationPrefsSetCmd() *cobra.Command {
	var channel string
	var events []string
	var allEvents, enabled, disabled bool
	set := &cobra.Command{
		Use:   "set",
		Short: "Update one channel's notification preferences",
		Long: `Update notification preferences for a channel (inbox or email).

--events takes a comma-separated list of event types (see 'prefs show' or the
types below); --all-events enables every type. --events and --all-events are
mutually exclusive, as are --enable and --disable, which switch the channel
on or off.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if channel != "inbox" && channel != "email" {
				return fmt.Errorf("invalid --channel %q: use inbox or email", channel)
			}
			// The channel-switch flags are mutually exclusive (issue #364):
			// silently resolving the pair sent enabled:true and dropped
			// --disable with exit code 0, the opposite of what the script
			// asked for. Refuse before any request, like inbox bulk-triage.
			if cmd.Flags().Changed("enable") && cmd.Flags().Changed("disable") {
				return errors.New("--enable and --disable are mutually exclusive; pass one")
			}
			// --events and --all-events both write body["eventMask"]
			// (issue #367): with both passed, the --all-events branch
			// silently overwrote the explicit list, enabling every type the
			// caller meant to restrict, with exit code 0. Refuse before any
			// request, like the --enable/--disable guard above — and before
			// the --events enum check, so a contradictory invocation reports
			// the conflict first instead of surfacing an enum error the
			// caller would fix and then hit the same refusal on the next run
			// (CodeRabbit review of #368).
			if cmd.Flags().Changed("events") && cmd.Flags().Changed("all-events") {
				return errors.New("--events and --all-events are mutually exclusive; pass one")
			}
			if cmd.Flags().Changed("events") {
				known := make(map[string]bool, len(notificationTypes))
				for _, t := range notificationTypes {
					known[t] = true
				}
				for _, e := range events {
					if !known[e] {
						return fmt.Errorf("invalid --events %q: not one of the notification types (%s)", e, strings.Join(notificationTypes, ", "))
					}
				}
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]any{"channel": channel}
			if cmd.Flags().Changed("events") {
				body["eventMask"] = events
			}
			if allEvents {
				body["eventMask"] = notificationTypes
			}
			// Only an explicitly passed flag writes the enabled field, and
			// the condition is Changed — not the bare bool — so explicit
			// false values (--enable=false, --disable=false) write the field
			// symmetrically instead of --disable=false falling through and
			// silently setting nothing. --disable negates into the enabled
			// field: a BoolVar bound to --disable is true when the channel
			// must be switched off, so assigning it verbatim would send
			// enabled:true (issue #364).
			switch {
			case cmd.Flags().Changed("enable"):
				body["enabled"] = enabled
			case cmd.Flags().Changed("disable"):
				body["enabled"] = !disabled
			}
			if len(body) == 1 {
				return errors.New("nothing to set: pass --events/--all-events and/or --enable/--disable")
			}
			var resp api.NotificationPref
			if err := A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/notification-prefs"), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.PrintfSafe("✓ %s notifications: %s", resp.Channel, boolYesNo(resp.Enabled))
			return nil
		},
	}
	set.Flags().StringVar(&channel, "channel", "", "Channel to update: inbox or email (required)")
	set.Flags().StringSliceVar(&events, "events", nil, "Comma-separated event types (validated locally against the notification enum)")
	set.Flags().BoolVar(&allEvents, "all-events", false, "Enable every event type on this channel")
	set.Flags().BoolVar(&enabled, "enable", false, "Enable the channel")
	set.Flags().BoolVar(&disabled, "disable", false, "Disable the channel")
	_ = set.MarkFlagRequired("channel")
	_ = set.RegisterFlagCompletionFunc("channel", cobra.FixedCompletions([]string{"inbox", "email"}, cobra.ShellCompDirectiveNoFileComp))
	_ = set.RegisterFlagCompletionFunc("events", cobra.FixedCompletions(notificationTypes, cobra.ShellCompDirectiveNoFileComp))
	return set
}
