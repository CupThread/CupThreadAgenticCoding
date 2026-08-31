package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

var notificationTypes = []string{
	"feedback.received",
	"feature_request.submitted",
	"feature_request.approved",
	"feature_request.shipped",
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
		Use:     "notifications",
		Aliases: []string{"notifications"},
		Short:   "Read and configure workspace notifications",
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
		Use:   "list",
		Short: "List notifications (newest first)",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
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
	list.Flags().IntVar(&limit, "limit", 50, "Maximum notifications to list")
	list.Flags().IntVar(&offset, "offset", 0, "Offset for pagination")
	return list
}

func newNotificationsReadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "read <notification-id>",
		Short: "Mark one notification as read",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/notifications/"+args[0]+"/read"), nil, nil, nil); err != nil {
				return err
			}
			if !A.structured() {
				A.out.Printf("✓ Marked %s as read", args[0])
			}
			return nil
		},
	}
}

func newNotificationsReadAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "read-all",
		Short: "Mark every notification as read",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/notifications/read-all"), nil, nil, nil); err != nil {
				return err
			}
			if !A.structured() {
				A.out.Printf("✓ Marked all notifications as read")
			}
			return nil
		},
	}
}

func newNotificationPrefsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "prefs", Short: "Per-channel notification preferences"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Show notification preferences",
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
types below); --all-events enables every type. Pass --enable/--disable to
switch the channel on or off.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if channel != "inbox" && channel != "email" {
				return fmt.Errorf("invalid --channel %q: use inbox or email", channel)
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
			switch {
			case cmd.Flags().Changed("enable"):
				body["enabled"] = enabled
			case disabled:
				body["enabled"] = false
			case enabled:
				body["enabled"] = true
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
			A.out.Printf("✓ %s notifications: %s", resp.Channel, boolYesNo(resp.Enabled))
			return nil
		},
	}
	set.Flags().StringVar(&channel, "channel", "", "Channel to update: inbox or email (required)")
	set.Flags().StringSliceVar(&events, "events", nil, "Comma-separated event types")
	set.Flags().BoolVar(&allEvents, "all-events", false, "Enable every event type on this channel")
	set.Flags().BoolVar(&enabled, "enable", false, "Enable the channel")
	set.Flags().BoolVar(&disabled, "disable", false, "Disable the channel")
	_ = set.MarkFlagRequired("channel")
	_ = set.RegisterFlagCompletionFunc("channel", cobra.FixedCompletions([]string{"inbox", "email"}, cobra.ShellCompDirectiveNoFileComp))
	_ = set.RegisterFlagCompletionFunc("events", cobra.FixedCompletions(notificationTypes, cobra.ShellCompDirectiveNoFileComp))
	return set
}
