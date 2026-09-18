package cmd

import (
	"fmt"
	"net/url"
	"regexp"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

// scopedUserIDRe mirrors the API's app-scoped public id format (PRIV-06):
// only well-formed u_<32 hex> ids require the appKey query parameter.
var scopedUserIDRe = regexp.MustCompile(`^u_[0-9a-f]{32}$`)

func newUsersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "users",
		Short: "Look up public user profiles",
	}
	cmd.AddCommand(newUsersProfileCmd())
	return cmd
}

func newUsersProfileCmd() *cobra.Command {
	var appKey string
	cmd := &cobra.Command{
		Use:   "profile <user-id>",
		Short: "Show a public user profile, apps, and recent comments",
		Long: "Show a public user profile, apps, and recent comments.\n" +
			"\n" +
			"USER-ID is either a legacy user id (user_*) or an app-scoped\n" +
			"pseudonym (u_<32 hex>) taken from a public board or comment\n" +
			"payload. App-scoped ids resolve only within their app, so they\n" +
			"require --app-key.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			userID := args[0]
			if scopedUserIDRe.MatchString(userID) && appKey == "" {
				return fmt.Errorf("user id %s is app-scoped (u_*); pass --app-key so it can be resolved within its app", userID)
			}
			path := "/api/v1/users/" + userID + "/profile"
			var q url.Values
			if appKey != "" {
				q = url.Values{"appKey": {appKey}}
			}
			var resp api.PublicUserProfileResponse
			if err := A.client.Do(cmd.Context(), "GET", path, q, nil, &resp); err != nil {
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
				{"Created", cutDate(p.CreatedAt)},
			})
			if len(resp.PublicApps) > 0 {
				A.out.Printf("\nApps (%d):", len(resp.PublicApps))
				appRows := make([][]string, 0, len(resp.PublicApps))
				for _, app := range resp.PublicApps {
					appRows = append(appRows, []string{
						app.ID,
						app.Name,
						app.AppSlug,
						app.WorkspaceName,
					})
				}
				A.out.Table([]string{"ID", "Name", "App Slug", "Workspace"}, appRows)
			}
			if len(resp.RecentComments) > 0 {
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
	cmd.Flags().StringVar(&appKey, "app-key", "", "Public app key (required to resolve app-scoped u_* user IDs)")
	return cmd
}
