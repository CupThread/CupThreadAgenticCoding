---
name: cupthread-cli
description: Guide for using the cupthread CLI command-line tool to manage workspaces, apps, feedback inbox, feature requests, roadmaps, changelogs, and integrations.
---

# CupThread CLI Guide

The `cupthread` CLI allows developers and AI agents to manage all resources on [CupThread.com](https://cupthread.com) (workspaces, apps, feedback inbox, feature requests, roadmap columns, versions, changelogs, integrations, billing) directly from the terminal.

---

## 1. Installation

If `cupthread` is not yet installed or not found in `$PATH`:

### Option A: Homebrew Tap (Recommended)
```sh
brew tap CupThread/tap
brew install cupthread

# Or one-liner:
brew install CupThread/tap/cupthread
```

### Option B: Go Install (requires Go 1.25+)
```sh
go install github.com/CupThread/CupThreadAgenticCoding/cmd/cupthread@latest
```

### Option C: Build from Source
If working directly inside the `CupThreadAgenticCoding` repository:
```sh
go build -o bin/cupthread ./cmd/cupthread
```

---

## 2. Authentication

### Method A: Browser / Device OAuth (Interactive)
```sh
# Browser OAuth flow
cupthread auth login

# Headless / SSH device code flow
cupthread auth login --device
```

### Method B: Personal Access Token (CI / Agents)
```sh
# Stored token
cupthread auth login --token cpt_...

# Or inject via environment variable (overrides stored credentials)
export CUPTHREAD_TOKEN="cpt_..."
```

Check current authentication status:
```sh
cupthread auth status
```

---

## 3. Global Flags & Output Options

- `--json`: Shorthand for `-o json` (emits indented machine-readable JSON). **Recommended for AI agents.**
- `-o, --output <table|json|yaml>`: Select output formatting (default `table`).
- `-w, --workspace <id>`: Target workspace ID (overrides default).
- `-a, --app <id>`: Target app ID (overrides default).
- `--base-url <url>`: API endpoint override (default `https://api.cupthread.com`).

---

## 4. Key CLI Commands

### Workspaces & Context
```sh
cupthread workspaces list                  # List all available workspaces
cupthread workspaces use <workspace-id>    # Set active workspace context
cupthread me                               # Show current user, workspaces, and roles
cupthread workspaces members list          # List workspace members & roles (any role, tokens OK)
cupthread billing show                     # Show subscription tier, limits, and usage (tokens OK)
```
Member mutations (`workspaces members invite/add/set-role/remove`, `workspaces invitations revoke`) and billing
changes (`billing checkout/portal/addons`) are interactive-session-only: with a `cpt_` token they fail with
`403 interactive_session_required` — sign in via `cupthread auth login` or use the Console web UI (see
Agent Best Practices #6).

### Apps Management
```sh
cupthread apps list                        # List apps in current workspace
cupthread apps use <app-id>                # Set active app context
cupthread apps get <app-id>                # Show app details and configuration
cupthread apps create --name "My App"      # Create a new app
cupthread apps update <app-id> --icon ./icon.png   # Upload an app icon (PNG/JPEG/WebP/GIF, or screened SVG;
                                           # requires workspace admin/owner). A declared type that does not
                                           # match the file content fails with 415 "unsupported image type".
cupthread apps public-config <app-key>     # Show the public portal config (no login required);
                                           # also accepts --workspace-slug <slug> --app-slug <slug>;
                                           # private apps fail with 404 like unknown keys (fail-closed)
cupthread apps public-changelog <app-key>  # Fetch the public changelog feed (no login required);
                                           # cursor-paginated: follow --cursor <nextCursor> until hasMore=false
cupthread apps public-feature-requests <app-key>  # Fetch the public feature-request feed (no login required);
                                           # keyset-cursor-paginated (DATA-01): follow --cursor <nextCursor> until
                                           # hasMore=false; --offset is ignored when --cursor is set; --q filters
```

### Feedback Inbox Triage
```sh
cupthread inbox list                       # List recent feedback submissions
cupthread inbox list --triage-status open --json  # Filter: open | in_progress | resolved | archived | active
cupthread inbox list --assigned-to unassigned --q "crash"  # Assignee filter + title search
cupthread inbox get <feedback-id>          # Triage detail: attachments, delivery attempts, activity log
cupthread inbox priority <feedback-id> !!! # Raise priority (! / !! / !!!)
cupthread inbox triage <feedback-id> in_progress   # Set triage status: open | in_progress | resolved | archived
cupthread inbox assign <feedback-id> <clerk-user-id>  # Assign to a workspace member (omit ID to unassign)
cupthread inbox bulk-triage <id1> <id2> --triage-status resolved  # Bulk update 1-50 submissions (also --assignee / --unassign)
cupthread inbox retry <feedback-id>        # Retry GitHub forwarding for a failed delivery
```

> Delivery `status` (`received` / `forwarded` / `forward_failed`) is managed by
> the platform and is separate from the triage lifecycle — use `inbox triage`
> (never a `--status` flag) to change triage state.

### Feature Requests & Roadmap
```sh
cupthread features list                    # List feature requests
cupthread features list --sort revenue     # Sort by user ARR/MRR (Pro plan)
cupthread features get <request-id>        # View feature request details (requester info, commenters)
cupthread features create --title "Dark mode" --description "Add dark theme support"
cupthread columns list                     # List public roadmap columns
cupthread versions list                    # List release milestones / versions
```
`features list` reads the console (workspace-scoped) listing. To walk the
**public** feed an end user would see, use
`cupthread apps public-feature-requests <app-key>` — keyset-cursor-paginated
(DATA-01): start without `--cursor`, then echo each page's `nextCursor` back
until the table reports no more pages. `hasMore`/`nextCursor` are always
present in the `--json` output (`nextCursor` is `null` on the last page), and
`--offset` is ignored whenever `--cursor` is set.

### Comments & @Replies
```sh
cupthread comments list <featureRequestId> # List comments on a feature request
cupthread comments create <featureRequestId> --body "Great idea!" [--reply-to <clerkId>] [--parent-id <commentId>]
```

### Comment Moderation (workspace)
```sh
cupthread comments moderation list <featureRequestId>  # All comments incl. hidden ones (404 if not in workspace)
cupthread comments moderation hide <commentId>         # Hide a comment from public portals
cupthread comments moderation unhide <commentId>       # Restore a hidden comment
cupthread comments moderation delete <commentId>       # Permanently delete a comment (404 if not in workspace)
```

### User Profiles
```sh
cupthread users profile <userId>           # Look up a public developer profile, apps, and comments
cupthread users profile u_9f2c… --app-key key_live_…  # App-scoped u_* ids from board/comment payloads need --app-key
```
User ids on public boards and comments are app-scoped pseudonyms (`u_<32 hex>`); they only resolve within their app, so pass the app's key with `--app-key`. Legacy `user_*` ids still work without it.

### Changelog & Releases
```sh
cupthread changelog list                   # List published and draft changelogs
cupthread changelog list --limit 50 --offset 100   # Page through large changelogs (server default: 100/page)
cupthread changelog create --title "v1.2.0" --body-file ./release-notes.md --publish-now
```
`changelog list` (console) is offset/limit-paginated and reports `total` +
`hasMore`; the table view prints the next `--offset` when more pages remain.
The public feed (`apps public-changelog`) instead uses opaque keyset cursors.

Drafts (create/edit/delete without publishing) work for every workspace role
and with `cpt_` API tokens. Publishing, `--publish-now`, and `--schedule-at`
require the admin/owner `changelog.publish` capability (SEC-40): with a
`cpt_` API token they fail with `403 interactive_session_required` (run
`cupthread auth login` first), and for member-role callers with
`403 capability_required` — the CLI appends both hints to the error.

### Search
```sh
cupthread search "crash on login"          # Global fuzzy search across apps, feedback, and roadmap
```

### Raw API Passthrough
Agents can invoke any API endpoint directly:
```sh
cupthread api request GET /api/v1/console/me --json
```

Every CLI request carries an `X-Request-Id` correlation header (`cli-<uuid>`; the API echoes it on every response). CLI errors quote the server-echoed value as `request-id=…`, and `api request` prints it on success lines — include that value verbatim in bug reports and support requests so the exact request can be found server-side.

### Repository & Skills Management
```sh
# Inspect git status of local CupThread repositories
bin/cupthread status --json

# Symlink skills into target project (.agents, .claude, .zcode)
bin/cupthread skills list
bin/cupthread skills link /path/to/target/project
```

### SDK Payment-Attribute Signing Helper
Bodies sent to `PUT /api/v1/public/apps/{appKey}/user` that report payment attributes (`isPaying`, `mrr`, `plan` — an explicit `null` counts) must carry an HMAC-SHA256 `signature` + `timestamp` (contract: API skill, "SDK Payment-Attribute Signing (DATA-03)"). The CLI computes reference signatures so coding agents can cross-check platform implementations:

```sh
cupthread api sign-user-attrs --app-key app_demo12345 --secret cpt_sk_... \
  --user-token 3fa85f64-5717-4562-b3fc-2c963f66afa6 \
  --input ./user-attrs.json
```

`--input` takes the **exact JSON body you plan to send** (`"-"` or `"@"` reads stdin); `--user-token` is the `X-User-Token` header value used only when the body carries no `userToken`. Output: the canonical string, the lowercase-hex signature, and the epoch timestamp (pin with `--timestamp <epoch>` for reproducible vectors; `--json` emits `{appKey, userToken, timestamp, canonical, signature}`). Add the returned `signature` and `timestamp` fields to the body without changing the signed values, and send within ±300 seconds of the timestamp. A body with no payment attributes prints a note that it may be sent unsigned.

---

## 5. Agent Best Practices

1. **Always use `--json`**: When calling CLI commands from automated tools, subagents, or scripts, append `--json` for predictable, parseable output.
2. **Set context once**: Use `cupthread workspaces use <id>` and `cupthread apps use <id>` to avoid repeating `-w` and `-a` on every command.
3. **Use `$CUPTHREAD_TOKEN` in CI**: Inject credentials via environment variable rather than storing them in config files.
4. **Handle `402 Payment Required`**: Writes are rejected by two kinds of quotas. Submission endpoints (`features create`, `inbox`-fed feedback) reject when the workspace hits its plan limits (`tier_limit_submissions` → upgrade the plan in Console → Billing; `subscription_inactive` → renew the subscription). `cupthread workspaces create` rejects with `workspace_limit_reached` when the developer account already owns the maximum number of workspaces (see `maxWorkspaces` on `cupthread me`; only owner-role memberships count) — delete or transfer ownership of one you own, then retry. In `--json` mode, the `api request` escape hatch returns the same guidance as `{error, code, status, hint}`. Treat 402 as a deterministic business rule — do not retry automatically.
5. **Handle `429 Too Many Requests`**: Public write endpoints are rate limited per client IP (changelog subscribe/unsubscribe: 10 req/min; `PUT /user` attribute upsert: 60 req/min) and respond with `{"error": "Too many requests. Please try again shortly."}`. Unlike 402, a 429 is transient: wait and retry with exponential backoff. The CLI renders the guidance as `rate limited: Too many requests. Please try again shortly. (HTTP 429) — <hint>` and, in `--json` mode, as `{error, status, hint}`.
6. **Handle `403 Forbidden` (AUTH-01 workspace RBAC)**: Every console workspace route declares a capability checked against the caller's workspace role, and the high-impact ones (`members.manage`, `billing.manage`, `integration.manage`) additionally reject `cpt_` API tokens regardless of role. Two structured codes come back with HTTP 403: `capability_required` — the role lacks the capability; ask a workspace admin/owner to perform the action or have an owner upgrade the role (Console → Members) — and `interactive_session_required` — a `cpt_` token can never do this; sign in interactively with `cupthread auth login` (browser/device OAuth) or use the Console web UI. Affected commands: `workspaces members invite/add/set-role/remove`, `workspaces invitations revoke`, `billing checkout/portal/addons`, and integration auth-url/connect/disconnect/sync — reads like `workspaces members list`, `billing show`, and `integrations status` are unaffected. The checks are ordered role-first-then-token-type, so a member-role token on a members route reports `capability_required` while an admin/owner token reports `interactive_session_required`. The CLI renders the guidance as `forbidden: <error> (HTTP 403, code=…) — <hint>` and, in `--json` mode via `api request`, as `{error, code, status, hint}`.
