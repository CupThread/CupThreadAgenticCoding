package cmd

import (
	"errors"
	"fmt"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

func newColumnsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "columns",
		Aliases: []string{"column"},
		Short:   "Manage roadmap columns",
	}
	cmd.AddCommand(newColumnsListCmd(), newColumnsCreateCmd(), newColumnsUpdateCmd(), newColumnsDeleteCmd())
	return cmd
}

var columnKinds = []string{"pending_review", "normal", "done"}

func newColumnsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List roadmap columns of the app",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, appID, err := resolveAppScope(cmd)
			if err != nil {
				return err
			}
			var resp api.ListColumnsResponse
			if err := A.client.Do(cmd.Context(), "GET", fmt.Sprintf("%s/apps/%s/columns", wsPath(ws, ""), appID), nil, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			rows := make([][]string, 0, len(resp.Columns))
			for _, c := range resp.Columns {
				rows = append(rows, []string{
					c.ID, c.Name, c.Slug, fmt.Sprintf("%d", c.Position),
					boolYesNo(c.IsVisible), c.Kind, c.Color,
				})
			}
			A.out.Table([]string{"ID", "Name", "Slug", "Pos", "Visible", "Kind", "Color"}, rows)
			return nil
		},
	}
}

// resolveAppScope resolves both workspace and app, scoping the client.
func resolveAppScope(cmd *cobra.Command) (ws, appID string, err error) {
	ws, err = workspaceClient(cmd.Context())
	if err != nil {
		return "", "", err
	}
	appID, err = A.requireAppID()
	if err != nil {
		return "", "", err
	}
	return ws, appID, nil
}

func newColumnsCreateCmd() *cobra.Command {
	var name, slug, kind, color string
	var visible bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a roadmap column",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return errors.New("--name is required")
			}
			ws, appID, err := resolveAppScope(cmd)
			if err != nil {
				return err
			}
			body := map[string]any{"appId": appID, "name": name, "isVisible": visible, "kind": kind}
			if slug != "" {
				body["slug"] = slug
			}
			if color != "" {
				body["color"] = color
			}
			var resp api.Column
			if err := A.client.Do(cmd.Context(), "POST", fmt.Sprintf("%s/apps/%s/columns", wsPath(ws, ""), appID), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Printf("✓ Created column %s (%s)", resp.Name, resp.ID)
			return nil
		},
	}
	create.Flags().StringVar(&name, "name", "", "Column name (required)")
	create.Flags().StringVar(&slug, "slug", "", "Column slug ([a-z0-9_-]; default: derived from name)")
	create.Flags().StringVar(&kind, "kind", "normal", "Column kind: pending_review, normal or done")
	create.Flags().StringVar(&color, "color", "", "Column color as #rrggbb")
	create.Flags().BoolVar(&visible, "visible", true, "Show the column on the public roadmap")
	_ = create.RegisterFlagCompletionFunc("kind", cobra.FixedCompletions(columnKinds, cobra.ShellCompDirectiveNoFileComp))
	return create
}

func newColumnsUpdateCmd() *cobra.Command {
	var name, color string
	var visible bool
	var position int
	update := &cobra.Command{
		Use:   "update <column-id>",
		Short: "Update a roadmap column",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("name") {
				body["name"] = name
			}
			if cmd.Flags().Changed("visible") {
				body["isVisible"] = visible
			}
			if cmd.Flags().Changed("position") {
				body["position"] = position
			}
			if cmd.Flags().Changed("color") {
				body["color"] = color
			}
			if len(body) == 0 {
				return errors.New("nothing to update: pass at least one flag")
			}
			if err := A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/columns/"+args[0]), nil, body, nil); err != nil {
				return err
			}
			if !A.structured() {
				A.out.Printf("✓ Updated column %s", args[0])
			}
			return nil
		},
	}
	update.Flags().StringVar(&name, "name", "", "New column name")
	update.Flags().BoolVar(&visible, "visible", true, "Visibility on the public roadmap")
	update.Flags().IntVar(&position, "position", 0, "Sort position (0-based)")
	update.Flags().StringVar(&color, "color", "", "Column color as #rrggbb")
	return update
}

func newColumnsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <column-id>",
		Aliases: []string{"rm"},
		Short:   "Delete a roadmap column",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "DELETE", wsPath(ws, "/columns/"+args[0]), nil, nil, nil); err != nil {
				return err
			}
			if !A.structured() {
				A.out.Printf("✓ Deleted column %s", args[0])
			}
			return nil
		},
	}
}

func newVersionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "versions",
		Aliases: []string{"version"},
		Short:   "Manage release versions",
	}
	cmd.AddCommand(newVersionsListCmd(), newVersionsCreateCmd(), newVersionsUpdateCmd(), newVersionsDeleteCmd())
	return cmd
}

func newVersionsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List versions of the app",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, appID, err := resolveAppScope(cmd)
			if err != nil {
				return err
			}
			var resp api.ListVersionsResponse
			if err := A.client.Do(cmd.Context(), "GET", fmt.Sprintf("%s/apps/%s/versions", wsPath(ws, ""), appID), nil, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			rows := make([][]string, 0, len(resp.Versions))
			for _, v := range resp.Versions {
				rows = append(rows, []string{
					v.ID, v.Label, fmt.Sprintf("%d", v.Position), boolYesNo(v.Released),
					orDash(cutDate(deref(v.ReleasedAt))), truncate(orDash(deref(v.Description)), 40),
				})
			}
			A.out.Table([]string{"ID", "Label", "Pos", "Released", "Released at", "Description"}, rows)
			return nil
		},
	}
}

func newVersionsCreateCmd() *cobra.Command {
	var label, description, releasedAt string
	var released bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a version",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if label == "" {
				return errors.New("--label is required")
			}
			ws, appID, err := resolveAppScope(cmd)
			if err != nil {
				return err
			}
			body := map[string]any{"appId": appID, "label": label, "released": released}
			if description != "" {
				body["description"] = description
			}
			if releasedAt != "" {
				body["releasedAt"] = releasedAt
			}
			var resp api.Version
			if err := A.client.Do(cmd.Context(), "POST", fmt.Sprintf("%s/apps/%s/versions", wsPath(ws, ""), appID), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Printf("✓ Created version %s (%s)", resp.Label, resp.ID)
			return nil
		},
	}
	create.Flags().StringVar(&label, "label", "", "Version label, e.g. 1.2.0 (required)")
	create.Flags().StringVar(&description, "description", "", "Version description")
	create.Flags().BoolVar(&released, "released", false, "Mark as released")
	create.Flags().StringVar(&releasedAt, "released-at", "", "Release date (ISO 8601)")
	return create
}

func newVersionsUpdateCmd() *cobra.Command {
	var label, description, releasedAt string
	var released bool
	var position int
	update := &cobra.Command{
		Use:   "update <version-id>",
		Short: "Update a version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("label") {
				body["label"] = label
			}
			if cmd.Flags().Changed("description") {
				body["description"] = nilIfEmpty(description)
			}
			if cmd.Flags().Changed("released") {
				body["released"] = released
			}
			if cmd.Flags().Changed("released-at") {
				body["releasedAt"] = nilIfEmpty(releasedAt)
			}
			if cmd.Flags().Changed("position") {
				body["position"] = position
			}
			if len(body) == 0 {
				return errors.New("nothing to update: pass at least one flag")
			}
			if err := A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/versions/"+args[0]), nil, body, nil); err != nil {
				return err
			}
			if !A.structured() {
				A.out.Printf("✓ Updated version %s", args[0])
			}
			return nil
		},
	}
	update.Flags().StringVar(&label, "label", "", "New label")
	update.Flags().StringVar(&description, "description", "", "New description (\"\" clears it)")
	update.Flags().BoolVar(&released, "released", false, "Released state")
	update.Flags().StringVar(&releasedAt, "released-at", "", "Release date (\"\" clears it)")
	update.Flags().IntVar(&position, "position", 0, "Sort position (0-based)")
	return update
}

func newVersionsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <version-id>",
		Aliases: []string{"rm"},
		Short:   "Delete a version",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "DELETE", wsPath(ws, "/versions/"+args[0]), nil, nil, nil); err != nil {
				return err
			}
			if !A.structured() {
				A.out.Printf("✓ Deleted version %s", args[0])
			}
			return nil
		},
	}
}
