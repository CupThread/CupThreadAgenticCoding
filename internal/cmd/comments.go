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
	cmd.AddCommand(newCommentsListCmd(), newCommentsCreateCmd())
	return cmd
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
