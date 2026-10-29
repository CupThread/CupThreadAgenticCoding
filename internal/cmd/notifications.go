package cmd

import (
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
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/notifications/"+pathID(args[0])+"/read"), nil, nil, nil); err != nil {
				return err
			}
			if A.structured() {
				return A.emitMutationResult("read", args[0])
			}
			A.out.Printf("✓ Marked %s as read", args[0])
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
			// request, like the --enable/--disable guard above.
			if cmd.Flags().Changed("events") && cmd.Flags().Changed("all-events") {
				return errors.New("--events and --all-events are mutually exclusive; pass one")
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
