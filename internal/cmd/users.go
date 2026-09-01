package cmd

import (
	"strconv"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

func newUsersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "users",
		Short: "Look up public user profiles",
	}
	cmd.AddCommand(newUsersProfileCmd())
	return cmd
}

func newUsersProfileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "profile <user-id>",
		Short: "Show a public user profile, apps, and recent comments",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/v1/users/" + args[0] + "/profile"
			var resp api.PublicUserProfileResponse
			if err := A.client.Do(cmd.Context(), "GET", path, nil, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			p := resp.Profile
			A.out.Table([]string{"Field", "Value"}, [][]string{
				{"User ID", p.ClerkUserID},
				{"Display name", orDash(deref(p.DisplayName))},
				{"Bio", orDash(deref(p.Bio))},
				{"Website", orDash(deref(p.WebsiteURL))},
				{"Avatar", orDash(deref(p.AvatarURL))},
				{"Hide comments", boolYesNo(p.HideComments)},
				{"Created", cutDate(p.CreatedAt)},
			})
			if len(resp.Apps) > 0 {
				A.out.Printf("\nApps (%d):", len(resp.Apps))
				appRows := make([][]string, 0, len(resp.Apps))
				for _, app := range resp.Apps {
					appRows = append(appRows, []string{
						app.ID,
						app.Name,
						app.Slug,
						strconv.Itoa(app.RequestCount),
					})
				}
				A.out.Table([]string{"ID", "Name", "Slug", "Requests"}, appRows)
			}
			if !resp.HideComments && len(resp.RecentComments) > 0 {
				A.out.Printf("\nRecent comments (%d):", len(resp.RecentComments))
				commentRows := make([][]string, 0, len(resp.RecentComments))
				for _, c := range resp.RecentComments {
					commentRows = append(commentRows, []string{
						c.AppName,
						truncate(c.FeatureRequestTitle, 30),
						truncate(c.Body, 50),
						cutDate(c.CreatedAt),
					})
				}
				A.out.Table([]string{"App", "Request", "Body", "Created"}, commentRows)
			}
			return nil
		},
	}
}
