package cmd

import (
	"errors"
	"fmt"
	"net/url"

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
	return &cobra.Command{
		Use:   "list",
		Short: "List changelog entries of the app",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, appID, err := resolveAppScope(cmd)
			if err != nil {
				return err
			}
			var resp api.ListChangelogResponse
			q := query(map[string]string{"appId": appID})
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/changelog"), q, nil, &resp); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(resp)
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
			return nil
		},
	}
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func newChangelogCreateCmd() *cobra.Command {
	var title, bodyText, bodyFile, versionLabel, versionID, scheduleAt string
	var linkIDs []string
	var publishNow bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a changelog entry (draft by default)",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if title == "" {
				return errors.New("--title is required")
			}
			text, err := resolveBody(bodyText, bodyFile)
			if err != nil {
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
			if flagJSON {
				return A.out.JSON(resp)
			}
			A.out.Printf("✓ Created changelog entry %s", resp.ID)
			return nil
		},
	}
	create.Flags().StringVar(&title, "title", "", "Entry title (required)")
	create.Flags().StringVar(&bodyText, "body", "", "Markdown body")
	create.Flags().StringVar(&bodyFile, "body-file", "", "Read the markdown body from a file (\"-\" for stdin)")
	create.Flags().StringVar(&versionLabel, "version-label", "", "Version label to show, e.g. 1.2.0")
	create.Flags().StringVar(&versionID, "version-id", "", "Linked version ID")
	create.Flags().StringSliceVar(&linkIDs, "link-request-ids", nil, "Comma-separated feature request IDs to close the loop")
	create.Flags().BoolVar(&publishNow, "publish-now", false, "Publish immediately")
	create.Flags().StringVar(&scheduleAt, "schedule-at", "", "Schedule publishing (ISO 8601 datetime)")
	return create
}

// resolveBody returns the markdown body from --body or --body-file.
func resolveBody(bodyText, bodyFile string) (string, error) {
	if bodyFile != "" {
		data, err := readInputFile(bodyFile)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return bodyText, nil
}

func newChangelogUpdateCmd() *cobra.Command {
	var title, bodyText, bodyFile, versionLabel, versionID, scheduleAt string
	var linkIDs []string
	update := &cobra.Command{
		Use:   "update <entry-id>",
		Short: "Update a changelog entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("title") {
				body["title"] = title
			}
			if cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") {
				text, err := resolveBody(bodyText, bodyFile)
				if err != nil {
					return err
				}
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
			if err := A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/changelog/"+args[0]), nil, body, &resp); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(resp)
			}
			A.out.Printf("✓ Updated changelog entry %s", resp.ID)
			return nil
		},
	}
	update.Flags().StringVar(&title, "title", "", "New title")
	update.Flags().StringVar(&bodyText, "body", "", "New markdown body")
	update.Flags().StringVar(&bodyFile, "body-file", "", "Read the new body from a file (\"-\" for stdin)")
	update.Flags().StringVar(&versionLabel, "version-label", "", "Version label (\"\" clears it)")
	update.Flags().StringVar(&versionID, "version-id", "", "Linked version ID (\"\" clears it)")
	update.Flags().StringSliceVar(&linkIDs, "link-request-ids", nil, "Feature request IDs to link")
	update.Flags().StringVar(&scheduleAt, "schedule-at", "", "Schedule publishing (\"\" clears it)")
	return update
}

func newChangelogDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <entry-id>",
		Aliases: []string{"rm"},
		Short:   "Delete a changelog entry",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "DELETE", wsPath(ws, "/changelog/"+args[0]), nil, nil, nil); err != nil {
				return err
			}
			if !flagJSON {
				A.out.Printf("✓ Deleted changelog entry %s", args[0])
			}
			return nil
		},
	}
}

func newChangelogPublishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "publish <entry-id>",
		Short: "Publish a changelog entry now",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.ChangelogEntry
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/changelog/"+args[0]+"/publish"), nil, nil, &resp); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(resp)
			}
			A.out.Printf("✓ Published %s", resp.ID)
			return nil
		},
	}
}

func newChangelogUnpublishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unpublish <entry-id>",
		Short: "Revert a changelog entry to draft",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/changelog/"+args[0]+"/unpublish"), nil, nil, nil); err != nil {
				return err
			}
			if !flagJSON {
				A.out.Printf("✓ Unpublished %s", args[0])
			}
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
