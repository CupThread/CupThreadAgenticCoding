package cmd

import (
	"fmt"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

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
			var resp api.ListCommentsResponse
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/feature-requests/"+args[0]+"/comments"), nil, nil, &resp); err != nil {
				if apiErr, ok := err.(*api.APIError); ok && apiErr.NotFound() {
					return notFoundErr("feature request", args[0], ws)
				}
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			rows := make([][]string, 0, len(resp.Comments))
			for _, c := range resp.Comments {
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
			A.out.Table([]string{"ID", "Author", "Body", "Reply To", "Hidden", "Created"}, rows)
			A.out.Printf("(%d comments)", len(resp.Comments))
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
		Short: "List comments on a feature request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/v1/feature-requests/" + args[0] + "/comments"
			headers := map[string]string{}
			if appKey != "" {
				headers["X-App-Key"] = appKey
			}
			if userToken != "" {
				headers["X-User-Token"] = userToken
			}
			var resp api.ListCommentsResponse
			if err := A.client.DoWithHeaders(cmd.Context(), "GET", path, nil, headers, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			rows := make([][]string, 0, len(resp.Comments))
			for _, c := range resp.Comments {
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
			A.out.Table([]string{"ID", "Author", "Body", "Reply To", "Hidden", "Created"}, rows)
			A.out.Printf("(%d comments)", len(resp.Comments))
			return nil
		},
	}
	list.Flags().StringVar(&appKey, "app-key", "", "Client App Key header (X-App-Key)")
	list.Flags().StringVar(&userToken, "user-token", "", "User device token header (X-User-Token)")
	return list
}

func newCommentsCreateCmd() *cobra.Command {
	var body, replyTo, parentID, authorName, authorEmail, authorAvatarURL, replyToAuthorName, appKey, userToken string
	create := &cobra.Command{
		Use:   "create <feature-request-id>",
		Short: "Post a comment or @reply on a feature request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if body == "" {
				return fmt.Errorf("--body is required")
			}
			path := "/api/v1/feature-requests/" + args[0] + "/comments"
			reqBody := map[string]any{"body": body}
			if authorName != "" {
				reqBody["authorName"] = authorName
			}
			if authorEmail != "" {
				reqBody["authorEmail"] = authorEmail
			}
			if authorAvatarURL != "" {
				reqBody["authorAvatarUrl"] = authorAvatarURL
			}
			if parentID != "" {
				reqBody["parentId"] = parentID
			}
			if replyTo != "" {
				reqBody["replyToClerkId"] = replyTo
			}
			if replyToAuthorName != "" {
				reqBody["replyToAuthorName"] = replyToAuthorName
			}

			headers := map[string]string{}
			if appKey != "" {
				headers["X-App-Key"] = appKey
			}
			if userToken != "" {
				headers["X-User-Token"] = userToken
			}

			var resp api.FeatureRequestComment
			if err := A.client.DoWithHeaders(cmd.Context(), "POST", path, nil, headers, reqBody, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			A.out.Printf("✓ Comment %s created", resp.ID)
			return nil
		},
	}
	create.Flags().StringVar(&body, "body", "", "Comment text (required)")
	create.Flags().StringVar(&replyTo, "reply-to", "", "Clerk user ID to @reply")
	create.Flags().StringVar(&parentID, "parent-id", "", "Parent comment ID for threading")
	create.Flags().StringVar(&authorName, "author-name", "", "Display name for the comment author")
	create.Flags().StringVar(&authorEmail, "author-email", "", "Email for the comment author")
	create.Flags().StringVar(&authorAvatarURL, "author-avatar-url", "", "Avatar image URL for the comment author")
	create.Flags().StringVar(&replyToAuthorName, "reply-to-author-name", "", "Display name of the author being replied to")
	create.Flags().StringVar(&appKey, "app-key", "", "Client App Key header (X-App-Key)")
	create.Flags().StringVar(&userToken, "user-token", "", "User device token header (X-User-Token)")
	return create
}
