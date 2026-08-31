package cmd

import (
	"net/url"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search <query>",
		Short: "Fuzzy-search workspaces, apps, requests, roadmap and changelog",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Search is identity-scoped; do not force a default workspace.
			q := url.Values{"q": {args[0]}}
			var resp api.SearchResponse
			if err := A.client.Do(cmd.Context(), "GET", "/api/v1/console/search", q, nil, &resp); err != nil {
				return err
			}
			if flagJSON {
				return A.out.JSON(resp)
			}
			rows := make([][]string, 0, len(resp.Results))
			for _, r := range resp.Results {
				snippet := ""
				if r.Snippet != nil {
					snippet = *r.Snippet
				}
				rows = append(rows, []string{
					r.Type,
					truncate(r.Title, 40),
					truncate(snippet, 40),
					r.WorkspaceName,
					orDash(deref(r.AppName)),
					orDash(deref(r.Status)),
				})
			}
			A.out.Table([]string{"Type", "Title", "Snippet", "Workspace", "App", "Status"}, rows)
			A.out.Printf("(%d results)", resp.Total)
			return nil
		},
	}
}
