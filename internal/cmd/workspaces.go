package cmd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

func newWorkspacesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspaces",
		Short: "Manage workspaces and their members",
	}
	cmd.AddCommand(
		newWorkspacesListCmd(),
		newWorkspacesCreateCmd(),
		newWorkspacesUseCmd(),
		newWorkspaceMembersCmd(),
		newWorkspaceInvitationsCmd(),
	)
	return cmd
}

func newWorkspacesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the workspaces you belong to",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var me api.MeResponse
			if err := A.client.Do(cmd.Context(), "GET", "/api/v1/console/me", nil, nil, &me); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(me.Workspaces)
			}
			rows := make([][]string, 0, len(me.Workspaces))
			for _, entry := range me.Workspaces {
				tier := "—"
				if entry.Subscription != nil {
					tier = entry.Subscription.Tier + "/" + entry.Subscription.Status
				}
				marker := ""
				if entry.Workspace.ID == A.cfg.DefaultWorkspace {
					marker = " *"
				}
				rows = append(rows, []string{entry.Workspace.ID, entry.Workspace.Name + marker, entry.Workspace.Slug, entry.Membership.Role, tier})
			}
			A.out.Table([]string{"ID", "Name", "Slug", "Role", "Tier"}, rows)
			return nil
		},
	}
}

var slugPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

func newWorkspacesCreateCmd() *cobra.Command {
	var name, slug string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a new workspace",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return errors.New("--name is required")
			}
			if slug == "" {
				slug = slugify(name)
			}
			if !slugPattern.MatchString(slug) || len(slug) < 2 {
				return fmt.Errorf("slug %q must match [a-z0-9-] with at least 2 characters", slug)
			}
			var resp api.CreateWorkspaceResponse
			body := map[string]string{"name": name, "slug": slug}
			if err := A.client.Do(cmd.Context(), "POST", "/api/v1/console/workspaces", nil, body, &resp); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(resp)
			}
			A.out.Printf("✓ Created workspace %s (%s)", resp.Workspace.Name, resp.Workspace.ID)
			A.out.Printf("  Make it the default with: cupthread workspaces use %s", resp.Workspace.ID)
			return nil
		},
	}
	create.Flags().StringVar(&name, "name", "", "Workspace name (required)")
	create.Flags().StringVar(&slug, "slug", "", "Workspace slug (default: slugified name)")
	return create
}

func slugify(name string) string {
	s := strings.ToLower(name)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func newWorkspacesUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <workspace-id-or-slug>",
		Short: "Set your default workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := A.lookupWorkspace(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			A.cfg.DefaultWorkspace = ws.ID
			if err := A.saveConfig(); err != nil {
				return err
			}
			A.out.Printf("✓ Default workspace: %s (%s)", ws.Name, ws.ID)
			return nil
		},
	}
}

// workspaceClient returns a client scoped to the resolved workspace.
func workspaceClient(ctx context.Context) (string, error) {
	ws, err := A.workspaceID()
	if err != nil {
		return "", err
	}
	A.client.WorkspaceID = ws
	return ws, nil
}

func wsPath(ws, suffix string) string {
	return "/api/v1/console/workspaces/" + ws + suffix
}

func newWorkspaceMembersCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "members", Short: "Manage workspace members"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List workspace members",
			DisableFlagsInUseLine: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				ws, err := workspaceClient(cmd.Context())
				if err != nil {
					return err
				}
				var resp api.ListMembersResponse
				if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/members"), nil, nil, &resp); err != nil {
					return err
				}
				if flagJSON {
					return A.out.JSON(resp)
				}
				rows := make([][]string, 0, len(resp.Members))
				for _, m := range resp.Members {
					rows = append(rows, []string{m.ID, orDash(deref(m.DisplayName)), orDash(deref(m.Email)), m.Role, m.ClerkUserID})
				}
				A.out.Table([]string{"Member ID", "Name", "Email", "Role", "Clerk User ID"}, rows)
				return nil
			},
		},
		newMembersInviteCmd(),
		newMembersAddCmd(),
		newMembersSetRoleCmd(),
		newMembersRemoveCmd(),
	)
	return cmd
}

var memberRoles = []string{"admin", "member"}

func newMembersInviteCmd() *cobra.Command {
	var email, role, displayName string
	invite := &cobra.Command{
		Use:   "invite",
		Short: "Invite a member by email",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if email == "" {
				return errors.New("--email is required")
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]any{"email": email, "role": role}
			if displayName != "" {
				body["displayName"] = displayName
			}
			var resp api.AddMemberResponse
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/members"), nil, body, &resp); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(resp)
			}
			verb := "Added"
			if resp.Status == "invited" {
				verb = "Invited"
			}
			A.out.Printf("✓ %s %s (%s)", verb, orDash(email), role)
			return nil
		},
	}
	invite.Flags().StringVar(&email, "email", "", "Email address to invite (required)")
	invite.Flags().StringVar(&role, "role", "member", "Role: admin or member")
	invite.Flags().StringVar(&displayName, "display-name", "", "Display name for the new member")
	_ = invite.RegisterFlagCompletionFunc("role", cobra.FixedCompletions(memberRoles, cobra.ShellCompDirectiveNoFileComp))
	return invite
}

func newMembersAddCmd() *cobra.Command {
	var clerkUserID, role string
	add := &cobra.Command{
		Use:   "add",
		Short: "Add an existing CupThread user by Clerk user ID",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if clerkUserID == "" {
				return errors.New("--clerk-user-id is required")
			}
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]any{"clerkUserId": clerkUserID, "role": role}
			var resp api.AddMemberResponse
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/members"), nil, body, &resp); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(resp)
			}
			if resp.Member != nil {
				A.out.Printf("✓ Added member %s (%s)", clerkUserID, resp.Member.ID)
			} else {
				A.out.Printf("✓ Member added (status: %s)", resp.Status)
			}
			return nil
		},
	}
	add.Flags().StringVar(&clerkUserID, "clerk-user-id", "", "Clerk user ID of an existing CupThread user (required)")
	add.Flags().StringVar(&role, "role", "member", "Role: admin or member")
	_ = add.RegisterFlagCompletionFunc("role", cobra.FixedCompletions(memberRoles, cobra.ShellCompDirectiveNoFileComp))
	return add
}

func newMembersSetRoleCmd() *cobra.Command {
	var role string
	setRole := &cobra.Command{
		Use:   "set-role <member-id>",
		Short: "Change a member's role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]string{"role": role}
			if err := A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/members/"+args[0]), nil, body, nil); err != nil {
				return err
			}
			if !flagJSON {
				A.out.Printf("✓ Role of %s set to %s", args[0], role)
			}
			return nil
		},
	}
	setRole.Flags().StringVar(&role, "role", "", "New role: admin or member (required)")
	_ = setRole.MarkFlagRequired("role")
	_ = setRole.RegisterFlagCompletionFunc("role", cobra.FixedCompletions(memberRoles, cobra.ShellCompDirectiveNoFileComp))
	return setRole
}

func newMembersRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <member-id>",
		Aliases: []string{"rm"},
		Short:   "Remove a member from the workspace",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			if err := A.client.Do(cmd.Context(), "DELETE", wsPath(ws, "/members/"+args[0]), nil, nil, nil); err != nil {
				return err
			}
			if !flagJSON {
				A.out.Printf("✓ Removed member %s", args[0])
			}
			return nil
		},
	}
}

func newWorkspaceInvitationsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "invitations", Short: "Manage pending workspace invitations"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List pending invitations",
			DisableFlagsInUseLine: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				ws, err := workspaceClient(cmd.Context())
				if err != nil {
					return err
				}
				var resp api.ListInvitationsResponse
				if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/invitations"), nil, nil, &resp); err != nil {
					return err
				}
				if flagJSON {
					return A.out.JSON(resp)
				}
				rows := make([][]string, 0, len(resp.Invitations))
				for _, inv := range resp.Invitations {
					rows = append(rows, []string{inv.ID, inv.Email, inv.Role, inv.CreatedAt})
				}
				A.out.Table([]string{"ID", "Email", "Role", "Created"}, rows)
				return nil
			},
		},
		&cobra.Command{
			Use:     "revoke <invitation-id>",
			Aliases: []string{"rm"},
			Short:   "Revoke a pending invitation",
			Args:    cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ws, err := workspaceClient(cmd.Context())
				if err != nil {
					return err
				}
				if err := A.client.Do(cmd.Context(), "DELETE", wsPath(ws, "/invitations/"+args[0]), nil, nil, nil); err != nil {
					return err
				}
				if !flagJSON {
					A.out.Printf("✓ Revoked invitation %s", args[0])
				}
				return nil
			},
		},
	)
	return cmd
}
