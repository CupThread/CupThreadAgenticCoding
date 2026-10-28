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

// legacyUserIDRe matches raw Clerk user ids (PRIV-06). Since SaaS #1322
// (issue #261) the public profile endpoint resolves app-scoped u_* ids only,
// and any user_* id just answers the anonymous placeholder — so the CLI
// rejects it locally with an actionable error instead of rendering a
// misleading empty profile.
var legacyUserIDRe = regexp.MustCompile(`^user_[0-9A-Za-z_-]+$`)

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
		Short: "Show a public user profile, apps, and recent comments (no login required)",
		Long: "Show a public user profile, apps, and recent comments.\n" +
			"\n" +
			"USER-ID must be the app-scoped pseudonym (u_<32 hex>) taken\n" +
			"from a public board or comment payload. App-scoped ids resolve\n" +
			"only within their app, so they require --app-key. Raw Clerk ids\n" +
			"(user_*) are no longer resolved (issue #261): the endpoint\n" +
			"answers the anonymous placeholder for them, so this command\n" +
			"rejects them up front.\n" +
			"\n" +
			"The profile GET is a public endpoint (issue #182), so this works\n" +
			"logged out; the CLI never sends the console bearer here. The route\n" +
			"is rate limited per client IP.",
		Args: exactNonBlankArg("user id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			userID := args[0]
			if legacyUserIDRe.MatchString(userID) {
				return fmt.Errorf("user id %s is a raw Clerk id (user_*); the public profile endpoint no longer resolves it — pass the app-scoped u_* id from a board or comment payload (with --app-key)", userID)
			}
			if scopedUserIDRe.MatchString(userID) && appKey == "" {
				return fmt.Errorf("user id %s is app-scoped (u_*); pass --app-key so it can be resolved within its app", userID)
			}
			path := "/api/v1/users/" + pathID(userID) + "/profile"
			var q url.Values
			if appKey != "" {
				q = url.Values{"appKey": {appKey}}
			}
			var resp api.PublicUserProfileResponse
			if err := A.unauthenticatedClient().Do(cmd.Context(), "GET", path, q, nil, &resp); err != nil {
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
