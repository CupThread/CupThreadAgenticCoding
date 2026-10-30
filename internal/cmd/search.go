package cmd

import (
	"net/url"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

// consoleSearchWindow is the merged result cap the console search route
// applies after merging every content class (SaaS apps/api:
// searchConsoleEntities slices the scored merge to limit = 30; issue #404).
const consoleSearchWindow = 30

func newSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search <query>",
		Short: "Fuzzy-search workspaces, apps, requests, roadmap and changelog",
		Long: `Fuzzy-search workspaces, apps, requests, roadmap and changelog.

The server merges every content class and serves only the top-scored 30
hits, with no paging. When more entries match, the footer reads
"(showing N of M results)" and the command warns about the window (stdout
in table mode, stderr in --json/-o yaml so the structured document stays
byte-faithful). Each class is fetched under its own cap before the merge,
so M is a lower bound, not an exact count, when a class's matches exceed
its fetch cap.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Search is identity-scoped; do not force a default workspace.
			q := url.Values{"q": {args[0]}}
			var resp api.SearchResponse
			if err := A.client.Do(cmd.Context(), "GET", "/api/v1/console/search", q, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				if err := A.out.Structured(resp); err != nil {
					return err
				}
				A.warnSearchTruncation(&resp)
				return nil
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
			// Every sibling listing keys its footer on shown vs total so a
			// capped window is never indistinguishable from the whole set
			// (issue #404, the #346/#352 disclosure discipline); the
			// complete-answer shape stays (%d results) for existing parsers.
			if resp.Total > len(resp.Results) {
				A.out.Printf("(showing %d of %d results)", len(resp.Results), resp.Total)
			} else {
				A.out.Printf("(%d results)", resp.Total)
			}
			A.warnSearchTruncation(&resp)
			return nil
		},
	}
}

// warnSearchTruncation discloses the console search's server-side window so
// a capped answer is never indistinguishable from the whole set (issue #404,
// the #346/#352 disclosure discipline). The route slices the scored merge to
// the top consoleSearchWindow hits with no paging, and each content class is
// fetched under its own SQL cap before the merge, so total is itself a lower
// bound when more entries match than were fetched — the warning says so
// rather than presenting total as exact. warnf routes the notice: stdout in
// table mode, stderr in --json/-o yaml mode so the structured document stays
// byte-faithful.
func (a *app) warnSearchTruncation(resp *api.SearchResponse) {
	if resp.Total > len(resp.Results) {
		a.warnf("⚠ Search results are truncated: showing the top-scored %d of %d matching entries; the console search serves only the top %d hits (server cap, no paging), and per-class fetch caps mean the total itself is a lower bound when many entries match", len(resp.Results), resp.Total, consoleSearchWindow)
	}
}
