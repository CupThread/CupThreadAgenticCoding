package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"unicode/utf8"

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

// columnKindSet mirrors FeatureRequestColumnKindSchema's enum for the
// create/update pre-flight (shell completion alone never rejected anything,
// issue #341).
var columnKindSet = map[string]bool{"pending_review": true, "normal": true, "done": true}

var (
	// columnColorRE mirrors ColumnColorSchema (^#[0-9a-fA-F]{6}$).
	columnColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	// columnSlugRE mirrors the slug regex inside CreateColumnInputSchema.
	columnSlugRE = regexp.MustCompile(`^[a-z0-9_-]+$`)
)

// validateColumnsCreateFlags mirrors CreateColumnInputSchema's zod intake
// bounds (name 1-60, slug 1-60 [a-z0-9_-], kind enum, #rrggbb color) so an
// out-of-bounds create fails locally naming the flag instead of as a
// guaranteed 400 Validation failed (issue #341, following
// validateAppsUpdateFlags from #336). Slug and color are only sent when
// non-empty, so only non-empty values are checked.
func validateColumnsCreateFlags(name, slug, kind, color string) error {
	if n := utf8.RuneCountInString(name); n > 60 {
		return fmt.Errorf("invalid --name: must be 1-60 characters, got %d", n)
	}
	if slug != "" {
		if n := utf8.RuneCountInString(slug); n > 60 {
			return fmt.Errorf("invalid --slug: must be 1-60 characters, got %d", n)
		}
		if !columnSlugRE.MatchString(slug) {
			return fmt.Errorf("invalid --slug %q: may only contain a-z, 0-9, _ and -", slug)
		}
	}
	if !columnKindSet[kind] {
		return fmt.Errorf("invalid --kind %q: must be one of pending_review, normal, done", kind)
	}
	if color != "" && !columnColorRE.MatchString(color) {
		return fmt.Errorf("invalid --color %q: must be a #rrggbb hex color (e.g. #4f46e5)", color)
	}
	return nil
}

// validateColumnsUpdateFlags mirrors UpdateColumnInputSchema's zod intake
// bounds (name 1-60, #rrggbb color, nonnegative position) so a rejected
// update fails before any request (issue #341). Neither field is nullable,
// so --name ""/--color "" below the bound are rejected with the accepted
// range named rather than sent as a guaranteed 400.
func validateColumnsUpdateFlags(cmd *cobra.Command, name, color string, position int) error {
	if cmd.Flags().Changed("name") {
		if n := utf8.RuneCountInString(name); n < 1 || n > 60 {
			return fmt.Errorf("invalid --name: must be 1-60 characters, got %d", n)
		}
	}
	if cmd.Flags().Changed("position") && position < 0 {
		return fmt.Errorf("invalid --position: must be 0 or greater, got %d", position)
	}
	if cmd.Flags().Changed("color") && !columnColorRE.MatchString(color) {
		return fmt.Errorf("invalid --color %q: must be a #rrggbb hex color (e.g. #4f46e5)", color)
	}
	return nil
}

// validateVersionsCreateFlags mirrors CreateVersionInputSchema's zod intake
// bounds (label 1-64, description at most 2000 characters) so an
// out-of-bounds create fails locally naming the flag (issue #341).
func validateVersionsCreateFlags(label, description string) error {
	if n := utf8.RuneCountInString(label); n > 64 {
		return fmt.Errorf("invalid --label: must be 1-64 characters, got %d", n)
	}
	if description != "" {
		if n := utf8.RuneCountInString(description); n > 2000 {
			return fmt.Errorf("invalid --description: must be at most 2000 characters, got %d", n)
		}
	}
	return nil
}

// validateVersionsUpdateFlags mirrors UpdateVersionInputSchema's zod intake
// bounds (label 1-64 and not nullable, description at most 2000 with ""
// clearing it, nonnegative position) so a rejected update fails before any
// request (issue #341).
func validateVersionsUpdateFlags(cmd *cobra.Command, label, description string, position int) error {
	if cmd.Flags().Changed("label") {
		if n := utf8.RuneCountInString(label); n < 1 || n > 64 {
			return fmt.Errorf("invalid --label: must be 1-64 characters, got %d", n)
		}
	}
	if cmd.Flags().Changed("description") && description != "" {
		if n := utf8.RuneCountInString(description); n > 2000 {
			return fmt.Errorf("invalid --description: must be at most 2000 characters, got %d", n)
		}
	}
	if cmd.Flags().Changed("position") && position < 0 {
		return fmt.Errorf("invalid --position: must be 0 or greater, got %d", position)
	}
	return nil
}

func newColumnsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:                   "list",
		Short:                 "List roadmap columns of the app",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, appID, err := resolveAppScope(cmd)
			if err != nil {
				return err
			}
			var resp api.ListColumnsResponse
			if err := A.client.Do(cmd.Context(), "GET", appPath(ws, appID, "/columns"), nil, nil, &resp); err != nil {
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
		Use:                   "create",
		Short:                 "Create a roadmap column",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return errors.New("--name is required")
			}
			if err := validateColumnsCreateFlags(name, slug, kind, color); err != nil {
				return err
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
			if err := A.client.Do(cmd.Context(), "POST", appPath(ws, appID, "/columns"), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.PrintfSafe("✓ Created column %s (%s)", resp.Name, resp.ID)
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
		Args:  exactNonBlankArg("column id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateColumnsUpdateFlags(cmd, name, color, position); err != nil {
				return err
			}
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
			if err := A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/columns/"+pathID(args[0])), nil, body, nil); err != nil {
				return err
			}
			if A.structured() {
				return A.emitMutationResult("updated", args[0])
			}
			A.out.Printf("✓ Updated column %s", args[0])
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
	cmd := &cobra.Command{
		Use:     "delete <column-id>",
		Aliases: []string{"rm"},
		Short:   "Delete a roadmap column",
		Long: `Delete a roadmap column permanently.

The server hard-deletes the column (its feature requests move back to the
review queue) and it cannot be restored. On an interactive terminal you are
asked to confirm before anything is sent; non-interactive callers must pass
--yes.`,
		Args: exactNonBlankArg("column id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirmDestructive(cmd, fmt.Sprintf("permanently delete roadmap column %q", args[0])); err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "DELETE", wsPath(ws, "/columns/"+pathID(args[0])), nil, nil, nil); err != nil {
				return err
			}
			if A.structured() {
				return A.emitMutationResult("deleted", args[0])
			}
			A.out.Printf("✓ Deleted column %s", args[0])
			return nil
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	return cmd
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
		Use:                   "list",
		Short:                 "List versions of the app",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, appID, err := resolveAppScope(cmd)
			if err != nil {
				return err
			}
			var resp api.ListVersionsResponse
			if err := A.client.Do(cmd.Context(), "GET", appPath(ws, appID, "/versions"), nil, nil, &resp); err != nil {
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
		Use:                   "create",
		Short:                 "Create a version",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if label == "" {
				return errors.New("--label is required")
			}
			if err := validateVersionsCreateFlags(label, description); err != nil {
				return err
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
			if err := A.client.Do(cmd.Context(), "POST", appPath(ws, appID, "/versions"), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.PrintfSafe("✓ Created version %s (%s)", resp.Label, resp.ID)
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
		Args:  exactNonBlankArg("version id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateVersionsUpdateFlags(cmd, label, description, position); err != nil {
				return err
			}
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
			if err := A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/versions/"+pathID(args[0])), nil, body, nil); err != nil {
				return err
			}
			if A.structured() {
				return A.emitMutationResult("updated", args[0])
			}
			A.out.Printf("✓ Updated version %s", args[0])
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
	var confirmLabel, reassignTo string
	cmd := &cobra.Command{
		Use:     "delete <version-id>",
		Aliases: []string{"rm"},
		Short:   "Delete a version",
		Long: `Delete a release version permanently.

The server hard-deletes the version and it cannot be restored. On an
interactive terminal you are asked to confirm before anything is sent;
non-interactive callers must pass --yes.

When feature requests are still linked to the version (shipped-in set),
the server refuses with 409 version_has_feature_requests until the
deletion is acknowledged: pass --confirm-label with the version's
current label (cupthread versions list) to delete it and unlink the
linked requests, adding --reassign-to <version-id> to move them to a
sibling version of the same app instead of unlinking them (the target
must not be the version being deleted). --reassign-to never replaces --confirm-label:
the server checks the label before any reassign, so with requests still
linked --reassign-to alone re-409s forever —
this command refuses that combination before sending anything.`,
		Args: exactNonBlankArg("version id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// --reassign-to never replaces --confirm-label (issue #370):
			// the server checks the label acknowledgment before any
			// reassign handling, so with requests still linked the bare
			// flag re-409s forever, and with none linked there is nothing
			// to move. Refuse the pair before the confirm prompt and any
			// request, like the sibling write-command guards (#364, #367).
			if reassignTo != "" && confirmLabel == "" {
				return errors.New("--reassign-to requires --confirm-label <current label> (cupthread versions list): with requests still linked the server checks the label first, so --reassign-to alone re-409s forever, and with none linked there is nothing to move")
			}
			if err := confirmDestructive(cmd, fmt.Sprintf("permanently delete version %q", args[0])); err != nil {
				return err
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var body any
			if confirmLabel != "" || reassignTo != "" {
				fields := map[string]any{}
				if confirmLabel != "" {
					fields["confirmLabel"] = confirmLabel
				}
				if reassignTo != "" {
					fields["reassignToVersionId"] = reassignTo
				}
				body = fields
			}
			if err := A.client.Do(cmd.Context(), "DELETE", wsPath(ws, "/versions/"+pathID(args[0])), nil, body, nil); err != nil {
				var apiErr *api.APIError
				if errors.As(err, &apiErr) {
					if versionHasFeatureRequests(apiErr) {
						// The guard fires whenever requests are still
						// linked and the label was not confirmed; state
						// the required label plus the reassign add-on
						// instead of a bare 409 (issue #199; #370 made
						// the label-first dependency explicit).
						count := linkedRequestCount(apiErr)
						return fmt.Errorf("version %s still has %s linked feature request(s) — re-run with --confirm-label <current label> (cupthread versions list) to delete it and unlink them, adding --reassign-to <sibling-version-id> to move them to another version of the same app instead of unlinking (--reassign-to still requires --confirm-label): %w", args[0], count, err)
					}
					if apiErr.Code == "reassign_target_not_in_app" {
						return fmt.Errorf("cannot reassign: --reassign-to must name a version of the same app (not the version being deleted) — pick a target from cupthread versions list: %w", err)
					}
				}
				return err
			}
			if A.structured() {
				return A.emitMutationResult("deleted", args[0])
			}
			A.out.Printf("✓ Deleted version %s", args[0])
			return nil
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt (required when stdin is not a terminal)")
	cmd.Flags().StringVar(&confirmLabel, "confirm-label", "", "Current label of the version (versions list); required by the server when feature requests are still linked, even alongside --reassign-to — deletes the version and unlinks them otherwise")
	cmd.Flags().StringVar(&reassignTo, "reassign-to", "", "Version ID to move the linked feature requests to instead of unlinking them (still requires --confirm-label; must be a version of the same app, not the version being deleted)")
	return cmd
}

// versionHasFeatureRequests reports whether err is the server's 409
// linked-requests guard. The guard body carries `error` but no `code`, so
// the identifier lands in APIError.Message.
func versionHasFeatureRequests(apiErr *api.APIError) bool {
	return apiErr.Status == http.StatusConflict &&
		(apiErr.Message == "version_has_feature_requests" || apiErr.Code == "version_has_feature_requests")
}

// linkedRequestCount extracts the linked-request count the 409 guard body
// carries ("count"); "an unknown number" when the body is missing or
// undecodable, so the remediation line never quotes a fabricated 0.
func linkedRequestCount(apiErr *api.APIError) string {
	var parsed struct {
		Count int `json:"count"`
	}
	if json.Unmarshal(apiErr.Body, &parsed) != nil || parsed.Count <= 0 {
		return "an unknown number of"
	}
	return strconv.Itoa(parsed.Count)
}
