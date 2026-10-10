package cmd

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

// maxCommentPages caps thread paging (50 pages ≈ 10k comments at the
// server's 200-per-request page cap, PROD-31) so a server that never stops
// reporting pages cannot hang the CLI.
const maxCommentPages = 50

// fetchCommentThread walks a comment thread by feeding each page's
// nextCursor back as the cursor query parameter until the server reports
// the last page, aggregating every page's comments. The metadata of the
// returned aggregate comes from the final page: Total is the thread's
// authoritative size under the endpoint's visibility rules — not
// len(Comments), which shrinks to the last page size once threads exceed
// the per-request cap — and a completed walk by definition has no next
// page. fetchPage must pass cursor through verbatim ("" on the first call).
func fetchCommentThread(fetchPage func(cursor string) (*api.ListCommentsResponse, error)) (*api.ListCommentsResponse, error) {
	aggregated := &api.ListCommentsResponse{}
	cursor := ""
	for page := 0; page < maxCommentPages; page++ {
		resp, err := fetchPage(cursor)
		if err != nil {
			return nil, err
		}
		aggregated.Comments = append(aggregated.Comments, resp.Comments...)
		aggregated.Total = resp.Total
		if resp.NextCursor == nil {
			aggregated.HasMore = false
			aggregated.NextCursor = nil
			return aggregated, nil
		}
		cursor = *resp.NextCursor
	}
	return nil, fmt.Errorf("comment thread did not end within %d pages (~%d comments fetched); giving up rather than paging forever", maxCommentPages, len(aggregated.Comments))
}

// commentCount reports the thread size for human output: the server's
// authoritative total when the response carries one, falling back to
// len(Comments) for a pre-pagination response with no total field.
func commentCount(resp *api.ListCommentsResponse) int {
	if resp.Total > 0 || len(resp.Comments) == 0 {
		return resp.Total
	}
	return len(resp.Comments)
}

// commentRows renders the shared comment table body.
func commentRows(comments []api.FeatureRequestComment) [][]string {
	rows := make([][]string, 0, len(comments))
	for _, c := range comments {
		id := c.ID
		if len(id) > 12 {
			id = id[:12]
		}
		hidden := ""
		if c.IsHidden {
			hidden = "yes"
		}
		rows = append(rows, []string{
			id,
			orDash(deref(c.AuthorName)),
			truncate(c.Body, 60),
			orDash(deref(c.ReplyToAuthorName)),
			hidden,
			cutDate(c.CreatedAt),
		})
	}
	return rows
}

func newCommentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comments",
		Short: "Manage feature-request comments",
	}
	cmd.AddCommand(newCommentsListCmd(), newCommentsCreateCmd(), newCommentsModerationCmd())
	return cmd
}

// newCommentsModerationCmd groups the Console Moderation endpoints
// (workspace-scoped, bearer-token authenticated) as opposed to the public
// portal list/create commands above.
func newCommentsModerationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "moderation",
		Short: "Moderate comments in a workspace (Console Moderation API)",
	}
	cmd.AddCommand(newCommentsModerationListCmd(), newCommentsModerationHideCmd(true), newCommentsModerationHideCmd(false), newCommentsModerationDeleteCmd())
	return cmd
}

// notFoundErr converts an API 404 into an explicit message naming the
// workspace, since moderation endpoints answer 404 (not success:false) when
// the comment or feature request is missing from the workspace.
func notFoundErr(kind, id, ws string) error {
	return fmt.Errorf("%s %q not found in workspace %s", kind, id, ws)
}

func newCommentsModerationListCmd() *cobra.Command {
	list := &cobra.Command{
		Use:   "list <feature-request-id>",
		Short: "List all comments on a feature request, including hidden ones",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			path := wsPath(ws, "/feature-requests/"+args[0]+"/comments")
			resp, err := fetchCommentThread(func(cursor string) (*api.ListCommentsResponse, error) {
				// Page 1 keeps the historic nil query; later pages echo
				// the server's nextCursor back as the cursor parameter.
				var q url.Values
				if cursor != "" {
					q = url.Values{"cursor": {cursor}}
				}
				var page api.ListCommentsResponse
				if err := A.client.Do(cmd.Context(), "GET", path, q, nil, &page); err != nil {
					if apiErr, ok := err.(*api.APIError); ok && apiErr.NotFound() {
						return nil, notFoundErr("feature request", args[0], ws)
					}
					return nil, err
				}
				return &page, nil
			})
			if err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Table([]string{"ID", "Author", "Body", "Reply To", "Hidden", "Created"}, commentRows(resp.Comments))
			A.out.Printf("(%d comments)", commentCount(resp))
			return nil
		},
	}
	return list
}

// newCommentsModerationHideCmd builds the hide (isHidden=true) and unhide
// (isHidden=false) variants of PATCH .../comments/{commentId}/hide.
func newCommentsModerationHideCmd(hidden bool) *cobra.Command {
	use, short := "hide", "Hide a comment from public portals (kept for moderation)"
	if !hidden {
		use, short = "unhide", "Make a hidden comment visible again"
	}
	return &cobra.Command{
		Use:   use + " <comment-id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.SuccessResponse
			body := api.HideCommentInput{IsHidden: hidden}
			if err := A.client.Do(cmd.Context(), "PATCH", wsPath(ws, "/comments/"+args[0]+"/hide"), nil, body, &resp); err != nil {
				if apiErr, ok := err.(*api.APIError); ok && apiErr.NotFound() {
					return notFoundErr("comment", args[0], ws)
				}
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			if hidden {
				A.out.Printf("✓ Comment %s hidden", args[0])
			} else {
				A.out.Printf("✓ Comment %s unhidden", args[0])
			}
			return nil
		},
	}
}

func newCommentsModerationDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <comment-id>",
		Short: "Permanently delete a comment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.SuccessResponse
			if err := A.client.Do(cmd.Context(), "DELETE", wsPath(ws, "/comments/"+args[0]), nil, nil, &resp); err != nil {
				if apiErr, ok := err.(*api.APIError); ok && apiErr.NotFound() {
					return notFoundErr("comment", args[0], ws)
				}
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Printf("✓ Comment %s deleted", args[0])
			return nil
		},
	}
}

func newCommentsListCmd() *cobra.Command {
	var appKey, userToken string
	list := &cobra.Command{
		Use:   "list <feature-request-id>",
		Short: "List comments on a feature request (no login required)",
		Long: `List the comments on a feature request's public board thread.

The thread GET is a public endpoint (issue #182), so this works on a clean
machine with no 'cupthread auth login' — the CLI never sends the console
bearer here, since it cannot satisfy a sign-in-only board either. Pass
--app-key/--user-token to identify an SDK-style caller. The route shares a
60 requests/minute per-IP budget; boards with anonymous roadmap view
disabled answer 401.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/v1/feature-requests/" + args[0] + "/comments"
			headers := map[string]string{}
			if appKey != "" {
				headers["X-App-Key"] = appKey
			}
			if userToken != "" {
				headers["X-User-Token"] = userToken
			}
			resp, err := fetchCommentThread(func(cursor string) (*api.ListCommentsResponse, error) {
				var q url.Values
				if cursor != "" {
					q = url.Values{"cursor": {cursor}}
				}
				var page api.ListCommentsResponse
				if err := A.unauthenticatedClient().DoWithHeaders(cmd.Context(), "GET", path, q, headers, nil, &page); err != nil {
					// PRIV-12: the public thread endpoint answers 404 both for a
					// missing id and for an unapproved request (existence
					// hiding), so the message must not imply the request never
					// existed.
					if apiErr, ok := err.(*api.APIError); ok && apiErr.NotFound() {
						return nil, fmt.Errorf("comments for feature request %q are not available: the request may not exist, may be private, or may not be approved yet (unapproved requests are hidden from the public board)", args[0])
					}
					return nil, err
				}
				return &page, nil
			})
			if err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Table([]string{"ID", "Author", "Body", "Reply To", "Hidden", "Created"}, commentRows(resp.Comments))
			A.out.Printf("(%d comments)", commentCount(resp))
			return nil
		},
	}
	list.Flags().StringVar(&appKey, "app-key", "", "Client App Key header (X-App-Key)")
	list.Flags().StringVar(&userToken, "user-token", "", "User device token header (X-User-Token)")
	return list
}

// commentsCreateUnavailableErr is the only outcome `comments create` can
// ever have (issue #181): POST /api/v1/feature-requests/{id}/comments
// authenticates the caller with verifyClerkToken — it parses the bearer as a
// three-part Clerk end-user session JWT. Every CLI credential is a cpt_
// token (personal access token or OAuth login), which fails that parse, so
// the wire request could only ever answer 401 authentication_required. The
// command fails locally, before any round-trip, instead of guaranteeing
// that 401.
const commentsCreateUnavailableErr = "posting comments requires a signed-in end-user session in the CupThread web portal; no CLI credential — a personal access token or an OAuth login — can perform it, so nothing was sent. Post the comment from the CupThread web portal while signed in"

func newCommentsCreateCmd() *cobra.Command {
	var body, parentID, replyToAuthorName string
	create := &cobra.Command{
		Use:   "create <feature-request-id>",
		Short: "Post a comment or @reply on a feature request (always fails: needs a web-portal sign-in)",
		Long: `Post a comment or @reply on a feature request.

This command cannot succeed with any CLI credential: the comment endpoint
requires a signed-in end-user session, and every CLI credential — a personal
access token or an OAuth login — is a cpt_ bearer the endpoint cannot
accept. It therefore fails locally, before sending anything, instead of
burning a guaranteed 401 round-trip. Post the comment from the CupThread web
portal while signed in.

The command and its --body/--parent-id flags stay declared for a future
credential that can sign in. The former --author-name/--author-email/
--author-avatar-url/--reply-to flags and the --app-key/--user-token headers
are gone: the server schema strips author and reply identity (PRIV-04,
SEC-50) and the route reads neither header.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New(commentsCreateUnavailableErr)
		},
	}
	create.Flags().StringVar(&body, "body", "", "Comment text (kept for a future credential that can sign in)")
	create.Flags().StringVar(&parentID, "parent-id", "", "Parent comment ID for threading (must be a visible comment on the same feature request; anything else is rejected with 400 invalid_parent)")
	create.Flags().StringVar(&replyToAuthorName, "reply-to-author-name", "", "Display name of the author being replied to (ignored by the API; it is resolved from the parent comment)")
	return create
}
