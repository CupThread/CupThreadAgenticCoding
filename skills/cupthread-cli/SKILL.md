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

The browser flow binds a random `127.0.0.1` port and uses `http://127.0.0.1:<port>/cupthread/callback` as its redirect URI — the loopback exception in the server's OAuth redirect-URI scheme policy (SEC-46); every non-loopback redirect URI must be `https:`.

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

Remove stored credentials from this machine (local only — revoke tokens
server-side in the Console under Settings → API Tokens):
```sh
cupthread auth logout
```

Logging in against a non-default API endpoint (`--base-url <url>` or `$CUPTHREAD_BASE_URL`) stores that
endpoint in the config until `auth logout`, so later invocations without the flag reach the same server
instead of silently falling back to production. Flags and env still override it per invocation; when a
non-default endpoint is stored, `auth status` shows it as "Credential issued for".

Switching accounts: `cupthread auth logout` clears the credential plus the saved default workspace,
per-workspace app defaults and base URL (back to pristine first-run state); `cupthread auth login`
drops saved defaults the new account cannot see (with a warning) instead of silently targeting the
previous user's workspace. Add `--revoke` to also invalidate the stored OAuth token pair server-side
(best-effort POST of the refresh token to the RFC 7009 `/api/v1/oauth/revoke` endpoint, which
cascades to the paired access token). PATs cannot be revoked from the CLI — the token-management API
requires an interactive Console session — so `--revoke` then prints the Console path
(Settings → API Tokens) instead of sending a request. The interactive OAuth flows store the token
pair as soon as the server issues it; the post-login session check is advisory — if it fails, login
still succeeds with a warning on stderr and saved workspace defaults are cleared because they could
not be verified (`cupthread auth status` confirms the session once the API is reachable again).

---

## 3. Global Flags & Output Options

- `--json`: Shorthand for `-o json` (emits indented machine-readable JSON). **Recommended for AI agents.**
- `-o, --output <table|json|yaml>`: Select output formatting (default `table`).
- `-w, --workspace <id>`: Target workspace ID (overrides default).
- `-a, --app <id>`: Target app ID (overrides default).
- `--base-url <url>`: API endpoint override (default `https://api.cupthread.com`; a non-default login is
  remembered until `auth logout`).
- `--no-retry`: Disable automatic retry/backoff on transient failures. By default body-less GET requests
  that answer 429/502/503/504 are retried up to 3 times with capped exponential backoff (honoring
  `Retry-After` when present), so a mid-batch blip no longer aborts a command; mutations (POST/PUT/
  PATCH/DELETE) are always single-shot. Each retry logs one line to stderr (never stdout); `api request
  --json` error payloads add `"attempts"` when retries were exhausted. `$CUPTHREAD_NO_RETRY=1` is the
  env equivalent.

---

## 4. Key CLI Commands

### Workspaces & Context
```sh
cupthread workspaces list                  # List all available workspaces
cupthread workspaces use <workspace-id>    # Set active workspace context
cupthread me                               # Show current user, workspaces, and roles
cupthread workspaces members list          # List workspace members & roles
cupthread workspaces invitations list      # List pending invitations
cupthread billing show                     # Show subscription tier, limits, and usage
```
All of the above are `[token-safe]`: any workspace role can run them and `cpt_`
API tokens work.

**Console-only (interactive session required — AUTH-01):** member mutations
(`workspaces members invite/add/set-role/remove`, `workspaces invitations revoke`) and billing
changes (`billing checkout/portal/addons`) are interactive-session-only: every CLI credential — a `cpt_`
personal access token and the browser OAuth login alike — fails with `403 interactive_session_required`;
perform these actions in the Console web UI (see Agent Best Practices #6).

**Destructive commands are confirm-guarded** (`features delete`, `columns delete`, `versions delete`,
`changelog delete`, `workspaces members remove`, `imports cancel`): the server hard-deletes these objects with
no undo, so the CLI refuses them on a non-interactive stdin unless `--yes`/`-y` is passed — the refusal fires
BEFORE any id resolution or HTTP request, so a wrong id costs nothing. On an interactive terminal the command
prints `About to … Continue? [yN]` on stderr instead (an answer other than `y`/`yes` aborts with nothing sent).
Scripts and agents must pass `--yes` explicitly (see Agent Best Practices #7).

### Apps Management
```sh
cupthread apps list                        # List apps in current workspace
cupthread apps use <app-id>                # Set active app context
cupthread apps get <app-id>                # Show app details and configuration
cupthread apps create --name "My App"      # Create a new app
cupthread apps update <app-id> --icon ./icon.png   # Upload an app icon (PNG/JPEG/WebP/GIF, or screened SVG;
                                           # requires workspace admin/owner). A declared type that does not
                                           # match the file content fails with 415 "unsupported image type".
                                           # Size cap (SEC-36): files over 10 MB are rejected client-side
                                           # (nothing is sent); the server answers larger payloads with
                                           # 413 payload_too_large.
                                           # Update order: metadata flags are PUT first, icon uploaded last;
                                           # name/slug/URL/platform values are validated locally first and a
                                           # failed icon upload after an applied PUT reports
                                           # "partially applied" (JSON errors carry applied/failed lists).
cupthread apps public-config <app-key>     # Show the public portal config (no login required);
                                           # also accepts --workspace-slug <slug> --app-slug <slug>;
                                           # private apps fail with 404 like unknown keys (fail-closed)
cupthread apps public-changelog <app-key>  # Fetch the public changelog feed (no login required);
                                           # cursor-paginated: follow --cursor <nextCursor> until hasMore=false
cupthread apps public-feature-requests <app-key>  # Fetch the public feature-request feed (no login required);
                                           # keyset-cursor-paginated (DATA-01): follow --cursor <nextCursor> until
                                           # hasMore=false; --offset is ignored when --cursor is set; --q filters
```
The management commands above are `[token-safe]`; `apps create`/`update`
need the workspace `app.configure` capability (admin/owner).

### App Settings (anonymous access & SDK appearance)
```sh
cupthread apps settings                    # Show settings of every app in the workspace
cupthread apps settings show <app-id-or-slug>              # Show one app's settings
cupthread apps settings set <app-id-or-slug> --anon-vote=false  # Toggle anonymous voting
                                           # (also --anon-roadmap/--anon-feedback/--anon-changelog)
cupthread apps settings set <app-id-or-slug> --input ./settings.json  # Raw JSON body, e.g. {"sdk":{"theme":"dark"}}
```
`[token-safe]` but needs `app.configure` (workspace admin/owner). These flags
drive what anonymous visitors can do on the public portal (roadmap view,
voting, feedback, changelog) and the SDK appearance (`sdk.theme`).

### Feedback Inbox Triage
```sh
cupthread inbox list                       # List recent feedback submissions
cupthread inbox list --triage-status open --json  # Filter: open | in_progress | resolved | archived | active
cupthread inbox list --assigned-to unassigned --q "crash"  # Assignee filter + title search
                                           # --q is a literal substring: %, _ and \ are escaped
                                           # server-side, so "100%" matches "100%" only (QUAL-05)
cupthread inbox get <feedback-id>          # Triage detail: attachments, delivery attempts, activity log
cupthread inbox priority <feedback-id> !!! # Raise priority (! / !! / !!!)
cupthread inbox triage <feedback-id> in_progress   # Set triage status: open | in_progress | resolved | archived
cupthread inbox assign <feedback-id> <clerk-user-id>  # Assign to a workspace member (omit ID to unassign)
cupthread inbox bulk-triage <id1> <id2> --triage-status resolved  # Bulk update 1-50 submissions (also --assignee / --unassign)
cupthread inbox retry <feedback-id>        # Retry GitHub forwarding for a failed delivery
cupthread inbox deliveries                 # Show the GitHub delivery queue (status, attempts, last error)
```
The whole `inbox` group is `[token-safe]` (workspace.read / content.manage).

> Delivery `status` (`received` / `forwarded` / `forward_failed`) is managed by
> the platform and is separate from the triage lifecycle — use `inbox triage`
> (never a `--status` flag) to change triage state.

### Notifications
```sh
cupthread notifications list               # List notifications, newest first (table ends with the unread count)
cupthread notifications read <notification-id>  # Mark one notification as read
cupthread notifications read-all           # Mark every notification as read
cupthread notifications prefs show         # Show per-channel (inbox/email) notification preferences
cupthread notifications prefs set --channel inbox --all-events  # Enable every event type on a channel
cupthread notifications prefs set --channel email --events "delivery.failed,import.failed" --enable  # Fine-grained event mask
```
The whole `notifications` group is `[token-safe]` (workspace.read). Event
types accepted by `--events` are the ones shown by `prefs show`
(`feedback.received`, `feature_request.approved`, `delivery.failed`, …).

### Feature Requests & Roadmap
```sh
cupthread features list                    # List feature requests
cupthread features list --sort revenue     # Sort by user ARR/MRR (Pro plan)
cupthread features get <request-id>        # View feature request details (requester info, commenters)
cupthread features create --title "Dark mode" --description "Add dark theme support"
cupthread features update <request-id> --title "New title" --column-slug planned  # Edit / move / re-version
                                           # (also --description, --version-id, --approved)
cupthread features approve <request-id>    # Approve a pending request
cupthread features forward <request-id> --target issue --labels bug,confirmed  # Forward to GitHub
                                           # (--target discussion|issue; repo defaults to the app's GitHub config)
cupthread features delete <request-id> --yes  # Permanently delete incl. votes & comments (confirm-guarded)
cupthread columns list                     # List public roadmap columns
cupthread versions list                    # List release milestones / versions
```
All of the above are `[token-safe]` (triage / content.manage capabilities).
`features list` reads the console (workspace-scoped) listing. The ID-taking
commands (`features get/update/approve/delete/forward`) resolve
`<request-id>` within the **resolved app** — the `--app` flag, else the saved
default from `apps use` — so an ID from another app in the same workspace
fails with "not found" instead of being mutated; with no app resolved the
lookup stays workspace-wide. Resolution pages through the whole workspace
listing (200 per page), so a request past the newest page still resolves;
prefix ambiguity is judged across every page, and resolution gives up after
50 pages (≈10k requests) with a clear error. To walk the
**public** feed an end user would see, use
`cupthread apps public-feature-requests <app-key>` — keyset-cursor-paginated
(DATA-01): start without `--cursor`, then echo each page's `nextCursor` back
until the table reports no more pages. `hasMore`/`nextCursor` are always
present in the `--json` output (`nextCursor` is `null` on the last page), and
`--offset` is ignored whenever `--cursor` is set.

### Imports (GitHub / Linear / Notion / Slack)
```sh
cupthread imports create --source github_issues --mode preview --owner acme --repo app  # Preview (default);
                                           # --mode commit actually creates the requests
cupthread imports list                     # List import jobs of the current app
cupthread imports history                  # List every import job in the workspace
cupthread imports get <job-id>             # Poll a job; --json includes preview candidates (.job.candidates)
cupthread imports rerun <job-id>           # Re-run a job (--mode preview|commit, --include-duplicates)
cupthread imports cancel <job-id> --yes    # Cancel a queued/running job (confirm-guarded; cannot be resumed)
```
The whole `imports` group is `[token-safe]`. Preview jobs complete
synchronously; commit jobs run on the queue — poll with `imports get` until
the status leaves `queued`/`running`. Source availability is tier-gated
(GitHub issues/discussions: Pro; Linear/Notion/Slack: Business).

### Integrations (GitHub / Linear / Notion / Slack)
```sh
cupthread integrations status              # Connection status of every integration (account, connected)
cupthread integrations github repos        # GitHub repositories accessible to the integration
cupthread integrations github categories --owner acme --repo app  # Discussion categories of a repository
cupthread integrations github config <app-id-or-slug> --owner acme --repo app  # Per-app repo + sync options
                                           # (also --category-slug, --sync-enabled, --status-sync, --comments-sync)
cupthread integrations linear status       # Per-provider status (also notion, slack)
```
The reads and the per-app GitHub config above are `[token-safe]` (workspace
read; `github config` needs `app.configure`, admin/owner).

**Console-only (interactive session required — AUTH-01):**
`integrations github|linear|notion|slack auth-url/connect/disconnect` and
`integrations github sync` reject `cpt_` tokens with
`403 interactive_session_required` — connect or sync integrations from an
interactive login or the Console web UI.

### Comments & @Replies
```sh
cupthread comments list <featureRequestId> # List comments on a feature request
cupthread comments create <featureRequestId> --body "Great idea!" [--reply-to <clerkId>] [--parent-id <commentId>]
```

Both list commands walk the thread's keyset pagination (PROD-31: 200
comments per request, `limit`/`cursor`) to the end, so threads longer than
one page still list completely, and the trailing count comes from the
server's authoritative `total`.

### Comment Moderation (workspace)
```sh
cupthread comments moderation list <featureRequestId>  # All comments incl. hidden ones (404 if not in workspace)
cupthread comments moderation hide <commentId>         # Hide a comment from public portals
cupthread comments moderation unhide <commentId>       # Restore a hidden comment
cupthread comments moderation delete <commentId>       # Permanently delete a comment (404 if not in workspace)
```
The moderation group is `[token-safe]` (content.manage) and works from any
workspace role whose capabilities include it.

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
cupthread changelog update <entry-id> --title "v1.2.0"   # Edit a draft (also --body-file, --version-id,
                                           # --version-label, --link-request-ids, --schedule-at)
cupthread changelog unpublish <entry-id>   # Revert a published entry to draft
cupthread changelog delete <entry-id> --yes  # Permanently delete a draft (confirm-guarded)
```
Draft editing, `unpublish`, and `delete` are `[token-safe]` (content.manage;
clearing a schedule with `--schedule-at ""` stays token-safe too).
`changelog list` (console) is offset/limit-paginated and reports `total` +
`hasMore`; the table view prints the next `--offset` when more pages remain.
The public feed (`apps public-changelog`) instead uses opaque keyset cursors.

Drafts (create/edit/delete without publishing) work for every workspace role
and with `cpt_` API tokens. Publishing, `--publish-now`, and `--schedule-at`
require the admin/owner `changelog.publish` capability (SEC-40): every CLI
credential — a `cpt_` API token and the browser OAuth login alike — fails
with `403 interactive_session_required` (publish in the Console web UI
instead), and for member-role callers with `403 capability_required` — the
CLI appends both hints to the error.

### Search
```sh
cupthread search "crash on login"          # Global fuzzy search across apps, feedback, and roadmap
```

`search` covers workspaces, apps, feature requests, roadmap versions, changelog entries, and **feedback submissions** (QUAL-06): a `feedback_submission` result matches the submission title/description/reporter name and carries its triage status — `--json` output passes the type through verbatim, so parse `type` as an open string.

### Raw API Passthrough
Agents can invoke any API endpoint directly:
```sh
cupthread api request GET /api/v1/console/me --json
```

Every CLI request carries an `X-Request-Id` correlation header (`cli-<uuid>`; the API echoes it on every response). CLI errors quote the server-echoed value as `request-id=…`, and `api request` prints it on success lines — include that value verbatim in bug reports and support requests so the exact request can be found server-side.

Validation failures (HTTP 400) carry the server's field-level `details` (zod flatten: `{formErrors: [...], fieldErrors: {field: [reasons]}}`). Every command's error line names the offending fields and reasons (e.g. `Validation failed (HTTP 400): title: Title must be at least 3 characters`), and in `--json` mode `api request` includes the raw `details` object in the error payload — read it instead of guessing which input to fix.

`--input @file` (or `"-"`/`"@"` for stdin) sends the body as JSON and is strict: the file must contain exactly one JSON value. A second value or stray text after it fails the command with `parse input JSON: unexpected trailing data` before anything is sent — fix the file rather than retrying. Request bodies are size-capped server-side (SEC-36): 1 MB on console routes, 256 KB on public routes — over-limit bodies answer `413 {"error": "Payload exceeds size limit", "code": "payload_too_large"}`.

Exit codes match the typed commands: on any API error (4xx/5xx) the `--json`/`-o yaml` payload (`{error, code, status, hint}`) still reaches stdout, but the command exits 1 and prints the `Error: …` line on stderr — branch on `$?` first, then parse the payload.

### Repository & Skills Management
```sh
# Inspect git status of local CupThread repositories
bin/cupthread status --json

# Symlink skills into target project (.agents, .claude, .zcode)
# Works from any installed binary: a verified CupThreadAgenticCoding checkout
# is symlinked; otherwise the skills embedded in the binary are copied.
bin/cupthread skills list
bin/cupthread skills link /path/to/target/project
```

### SDK Payment-Attribute Signing Helper
Bodies sent to `PUT /api/v1/public/apps/{appKey}/user` that report payment attributes (`isPaying`, `mrr`, `plan` — an explicit `null` counts) must carry an HMAC-SHA256 `signature` + `timestamp` (contract: API skill, "SDK Payment-Attribute Signing (DATA-03)"). The CLI computes reference signatures so coding agents can cross-check platform implementations:

```sh
# Recommended: keep the signing secret off the command line (shell history / ps)
printf %s "$CUP_SDK_SECRET" | cupthread api sign-user-attrs --app-key app_demo12345 \
  --user-token 3fa85f64-5717-4562-b3fc-2c963f66afa6 --secret - --input ./user-attrs.json
# or export CUPTHREAD_SDK_SIGNING_SECRET=cpt_sk_... once, then omit --secret entirely
```

`--secret` takes the SDK signing secret inline, as `-`/`@` (read from stdin, trailing whitespace trimmed), or falls back to `$CUPTHREAD_SDK_SIGNING_SECRET` (also trimmed) when omitted; precedence is flag > env, and the no-source error names all three forms. Prefer stdin/env — inline values land in shell history and are visible via `ps`. `--secret` and `--input` cannot both read stdin in one invocation. `--input` takes the **exact JSON body you plan to send** (`"-"` or `"@"` reads stdin); `--user-token` is the `X-User-Token` header value used only when the body carries no `userToken`. Output: the canonical string, the lowercase-hex signature, and the epoch timestamp (pin with `--timestamp <epoch>` for reproducible vectors; `--json` emits `{appKey, userToken, timestamp, canonical, signature}`). Add the returned `signature` and `timestamp` fields to the body without changing the signed values, and send within ±300 seconds of the timestamp. A body with no payment attributes prints a note that it may be sent unsigned.

---

## 5. Agent Best Practices

1. **Always use `--json`**: When calling CLI commands from automated tools, subagents, or scripts, append `--json` for predictable, parseable output.
2. **Set context once**: Use `cupthread workspaces use <id>` and `cupthread apps use <id>` to avoid repeating `-w` and `-a` on every command.
3. **Use `$CUPTHREAD_TOKEN` in CI**: Inject credentials via environment variable rather than storing them in config files.
4. **Handle `402 Payment Required`**: Writes are rejected by two kinds of quotas. Submission endpoints (`features create`, `inbox`-fed feedback) reject when the workspace hits its plan limits (`tier_limit_submissions` → upgrade the plan in Console → Billing; `subscription_inactive` → renew the subscription). `cupthread workspaces create` rejects with `workspace_limit_reached` when the developer account already owns the maximum number of workspaces (see `maxWorkspaces` on `cupthread me`; only owner-role memberships count) — delete or transfer ownership of one you own, then retry. In `--json` mode, the `api request` escape hatch returns the same guidance as `{error, code, status, hint}`. Treat 402 as a deterministic business rule — do not retry automatically.
5. **Handle `429 Too Many Requests`**: Public write endpoints are rate limited per client IP (changelog subscribe/confirm and GETs: 10 req/min; token-bearing one-click unsubscribe POSTs: 300 req/min on a dedicated budget, PRIV-08; `PUT /user` attribute upsert: 60 req/min) and respond with `{"error": "Too many requests. Please try again shortly."}`. Unlike 402, a 429 is transient: wait and retry with exponential backoff. The CLI renders the guidance as `rate limited: Too many requests. Please try again shortly. (HTTP 429) — <hint>` and, in `--json` mode, as `{error, status, hint}`.
6. **Handle `403 Forbidden` (AUTH-01 workspace RBAC)**: Every console workspace route declares a capability checked against the caller's workspace role, and the high-impact ones (`members.manage`, `billing.manage`, `integration.manage`) additionally reject `cpt_` API tokens regardless of role. Two structured codes come back with HTTP 403: `capability_required` — the role lacks the capability; ask a workspace admin/owner to perform the action or have an owner upgrade the role (Console → Members) — and `interactive_session_required` — no CLI credential can do this: the OAuth login also issues a `cpt_` token, so perform the action in the Console web UI. Affected commands: `workspaces members invite/add/set-role/remove`, `workspaces invitations revoke`, `billing checkout/portal/addons`, and integration auth-url/connect/disconnect/sync — reads like `workspaces members list`, `billing show`, and `integrations status` are unaffected. The checks are ordered role-first-then-token-type, so a member-role token on a members route reports `capability_required` while an admin/owner token reports `interactive_session_required`. The CLI renders the guidance as `forbidden: <error> (HTTP 403, code=…) — <hint>` and, in `--json` mode via `api request`, as `{error, code, status, hint}`.
7. **Pass `--yes` to destructive commands after checking the target**: `features delete`, `columns delete`, `versions delete`, `changelog delete`, `workspaces members remove`, and `imports cancel` are hard, server-side, unrestorable deletes. On a non-interactive stdin they refuse with `… re-run with --yes to confirm` BEFORE resolving ids or sending any request (also in `--json` mode); on an interactive terminal they prompt `Continue? [yN]` on stderr. When automating, resolve the id first (`features get`, `changelog list`, …), verify it is the record you mean, and only then pass `--yes` — never loop these commands over an unverified generated id list.
9. **Read `details` on `400 Validation failed` before retrying**: every zod rejection carries field-level reasons, and the CLI surfaces them — the error line names each offending field (`Validation failed (HTTP 400): title: Title must be at least 3 characters; versionId: Invalid version`), and `api request --json` includes the raw `details` object (`{formErrors, fieldErrors}`) in the error payload. Fix the named inputs and retry once; do not brute-force varying inputs against a 400.
8. **Keep provider connection tokens off the command line**: `integrations github|linear|notion|slack connect --token <value>` puts a GitHub PAT / Linear / Notion / Slack API token into shell history, `ps` output, and CI logs. Pass `--token -` (or `@`) to read the token from stdin (`printf %s "$GITHUB_PAT" | cupthread integrations github connect --token -`, trailing whitespace trimmed) or export the per-provider variable `CUPTHREAD_GITHUB_TOKEN` / `CUPTHREAD_LINEAR_TOKEN` / `CUPTHREAD_NOTION_TOKEN` / `CUPTHREAD_SLACK_TOKEN` and omit the flag entirely. Precedence is flag > env, and the no-source error names all three forms. The inline value still works but leaks the secret.
