package cmd

import (
	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

func newMeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "me",
		Short: "Show your identity, workspaces, and roles",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var me api.MeResponse
			if err := A.client.Do(cmd.Context(), "GET", "/api/v1/console/me", nil, nil, &me); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(me)
			}
			email := "—"
			if me.Email != nil {
				email = *me.Email
			}
			A.out.Printf("User: %s (%s)", orDash(email), me.ClerkUserID)
			rows := make([][]string, 0, len(me.Workspaces))
			for _, entry := range me.Workspaces {
				tier := "—"
				if entry.Subscription != nil {
					tier = entry.Subscription.Tier
				}
				marker := ""
				if entry.Workspace.ID == A.cfg.DefaultWorkspace {
					marker = " *"
				}
				rows = append(rows, []string{
					entry.Workspace.ID,
					entry.Workspace.Name + marker,
					entry.Workspace.Slug,
					entry.Membership.Role,
					tier,
				})
			}
			A.out.Table([]string{"ID", "Name", "Slug", "Role", "Tier"}, rows)
			return nil
		},
	}
}
