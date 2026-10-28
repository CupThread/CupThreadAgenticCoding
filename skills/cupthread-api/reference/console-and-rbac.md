# Console Surface, RBAC & Correlation IDs

Reference detail for the `cupthread-api` skill, split out of `SKILL.md` so the
skill stays under the agent-skill size budget (issue #318). The live OpenAPI 3.1
spec (<https://api.cupthread.com/api/v1/openapi.json>) remains the authoritative
contract; these files mirror the shipped behavior behind it.

## Console Moderation Endpoints

Workspace-scoped comment moderation (OpenAPI tag `Console Moderation`), authenticated with a developer `cpt_` token or Clerk session:

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/console/workspaces/:wsId/feature-requests/:frId/comments` | `GET` | List every comment on a feature request (including hidden) for moderation. Returns `404` when the feature request is not in the workspace, otherwise `{"comments": [...]}`. |
| `/api/v1/console/workspaces/:wsId/comments/:commentId/hide` | `PATCH` | Toggle visibility with body `{"isHidden": bool}`. Returns `404` when the comment is not in the workspace, `200 {"success": true}` on success. |
| `/api/v1/console/workspaces/:wsId/comments/:commentId` | `DELETE` | Permanently delete a comment. Returns `404` when the comment is not in the workspace, `200 {"success": true}` on success. |

**Workspace id semantics**: on all `/api/v1/console/workspaces/{wsId}/...` endpoints the path id is authoritative. The `X-Workspace-Id` header is optional; when sent it must match the path id, otherwise the API answers `400`. Prefer omitting the header on these routes.

---

## Workspace Capability RBAC (AUTH-01)

Every `/api/v1/console/workspaces/{wsId}/...` route declares one capability (upstream CupThread/SaaS#55, `apps/api/src/lib/capabilities.ts`), checked server-side against the caller's workspace role. Unknown or corrupted roles fail closed with no capabilities:

| Capability | member | admin | owner |
|---|---|---|---|
| `workspace.read` (all read-only GET/list endpoints) | ✅ | ✅ | ✅ |
| `triage` (submissions, feature requests, forwarding) | ✅ | ✅ | ✅ |
| `content.manage` (changelog drafts & edits, columns, versions, imports, comment moderation, deletions) | ✅ | ✅ | ✅ |
| `changelog.publish` (publish/schedule changelog entries → subscriber email blast, SEC-40) | ❌ | ✅ | ✅ |
| `app.configure` (app create/update, icon, SDK settings, per-app repo link) | ❌ | ✅ | ✅ |
| `integration.manage` (connect/disconnect integrations, manual tokens, sync) | ❌ | ✅ | ✅ |
| `billing.manage` (checkout, portal, add-ons) | ❌ | ✅ | ✅ |
| `members.manage` (member add/set-role/remove, invitation revoke) | ❌ | ✅ | ✅ |
| `privacy.manage` (end-user erase/anonymize, attachment delete, export create / download-token / download — PRIV-01) | ❌ | ✅ | ✅ |
| `workspace.delete` | ❌ | ❌ | ✅ (declared; no route uses it yet) |

Two structured 403 codes can come back from these routes (both with HTTP 403):

- `capability_required` — the caller's role does not include the route's capability:
  `{"error": "Access denied: your workspace role does not include the '<capability>' capability", "code": "capability_required"}`
- `interactive_session_required` — the route's capability is in the interactive-only set (`changelog.publish`, `members.manage`, `billing.manage`, `integration.manage`, `privacy.manage`, `workspace.delete`) and the caller authenticated with a `cpt_` API token, regardless of role (mirroring the `/api/v1/console/tokens` management rule):
  `{"error": "This action requires an interactive session; API tokens are not permitted", "code": "interactive_session_required"}`

The checks are ordered: **role first, then token type**. A member-role `cpt_` token on a `members.manage` route therefore gets `capability_required` (role too low), while an admin/owner `cpt_` token gets `interactive_session_required` (role sufficient, token type rejected). Interactive Clerk web sessions (Console web UI) never see `interactive_session_required` — note that the CLI's `auth login` OAuth flow issues a `cpt_` access token too, so from the CLI every interactive-only capability is unreachable.

Unaffected for `cpt_` tokens: all `workspace.read` lookups (including `GET .../members`, `GET .../invitations`, `GET .../billing`, and integration status reads), `triage`, `content.manage` (changelog **drafts and edits** included — only publishing/scheduling moved out into `changelog.publish`, see the next section), `app.configure`, and imports. Three GET exceptions are interactive-session-only despite being reads: `GET .../billing/portal` (billing portal redirect, `billing.manage`), `GET .../integrations/:provider/authorize` (OAuth authorize URL, `integration.manage`), and `GET .../export/:jobId/download` (export artifact download, `privacy.manage`). Public feedback/SDK endpoints are unchanged.

---

## Changelog Publishing Capability (SEC-40)

Publishing or scheduling a changelog entry emails every confirmed subscriber (a blast), so it carries its own `changelog.publish` capability — admin/owner only — on top of the `content.manage` access the routes already require (upstream CupThread/SaaS#286, merge `0bc77d6`). Gate per endpoint:

| Endpoint | Gated request shape | Requirement |
|---|---|---|
| `POST .../changelog` | body has `publishNow: true` or non-null `scheduledAt` | `changelog.publish` (checked **in addition to** the route's `content.manage` access) |
| `PUT .../changelog/:entryId` | body has a string `scheduledAt` (setting a schedule only) | `changelog.publish` |
| `POST .../changelog/:entryId/publish` | always | `changelog.publish` (route-wide; **changed** from `content.manage`) |

Not gated: creating/updating/deleting **drafts** without publish/schedule intent, listing, unpublishing, and **clearing** a schedule stay under plain `content.manage` (members and `cpt_` tokens keep full draft workflow access). The clear semantics on `PUT`: `scheduledAt: null` clears the schedule and is **not** gated — the gate checks `typeof scheduledAt === "string"`, so a JSON `null` skips it entirely and plain `content.manage` suffices (a member or `cpt_` token **can unschedule**); `""` never reaches the route — the schema is `z.string().datetime().nullable().optional()`, so an empty string fails validation with `400 {"error": "Validation failed"}`. The CLI's `changelog update <id> --schedule-at ""` sends exactly `{"scheduledAt": null}`.

Denials follow the AUTH-01 order (role first, then token type):

- member (any auth type) on a gated path → `403 capability_required` naming `'changelog.publish'`
- admin/owner with a `cpt_` API token on a gated path → `403 interactive_session_required` — publish from the Console web UI instead (the CLI's OAuth login also issues a `cpt_` token, so no CLI credential can publish)

Server side, every denial emits a structured `authz_denied` audit event (`reason: missing_capability` or `interactive_session_required`), and a successful publish records the publishing Clerk user and enqueues the blast job. The console publish routes are not declared in the OpenAPI document (only the public read/subscribe paths are), so there is no spec surface to regenerate.

---

## Request Correlation IDs (`X-Request-Id`, OPS-01)

**Every response — all endpoints, all status codes — carries an `X-Request-Id` response header** (OpenAPI shared component `XRequestId`): the caller's ID when the request supplied a format-valid one, otherwise a server-generated UUID. It is a correlation handle for server logs, telemetry, and support flows — never an authentication or authorization primitive.

Contract:

- Callers MAY send an `X-Request-Id` request header on any endpoint. It is honored only when it matches `^[A-Za-z0-9._-]{8,64}$` (a UUID qualifies); any other value is ignored and replaced server-side, and the response always echoes the effective value.
- CORS exposes the header to cross-origin browsers on every route class (`Access-Control-Expose-Headers: X-Request-Id`) and allowlists it as a request header on console and public preflights.
- Quote the response's `X-Request-Id` verbatim in bug reports and support requests so the exact request can be found server-side. The `cupthread` CLI does this for you: it sends `cli-<uuid>` per request and quotes the echoed value as `request-id=…` on errors (and on `api request` success lines).

---
