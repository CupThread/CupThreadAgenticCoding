---
name: cupthread-api
description: Reference for CupThread backend API endpoints, authentication flows, OpenAPI 3.1 schema, and interactive API documentation.
---

# CupThread API Reference & OpenAPI Specification

Complete developer reference for CupThread's backend API endpoints, authentication flows, public SDK APIs, and OpenAPI 3.1 specifications.

## 🌐 Official API Documentation & OpenAPI Schema
- **Interactive API Documentation (Web)**: [https://cupthread.com/api](https://cupthread.com/api) (or [https://api.cupthread.com/reference](https://api.cupthread.com/reference))
- **OpenAPI 3.1 Schema (JSON)**: [https://api.cupthread.com/api/v1/openapi.json](https://api.cupthread.com/api/v1/openapi.json)
- **OpenAPI 3.1 Schema (YAML)**: [https://api.cupthread.com/api/v1/openapi.yaml](https://api.cupthread.com/api/v1/openapi.yaml)
- **Official Platform Website**: [https://cupthread.com](https://cupthread.com)

---

## 🤖 Quick Prompt for Coding Agents

To ask your AI agent to build a custom API client or webhook integration using the official OpenAPI spec, copy and paste this prompt:

```text
Please read the CupThread OpenAPI 3.1 specification at https://api.cupthread.com/api/v1/openapi.json and implement a type-safe client for submitting user feedback, querying public feature requests, and syncing user attributes.
```

---

## Base URL & Environment
- **Production API Base**: `https://api.cupthread.com`
- **Interactive Reference UI**: `https://cupthread.com/api`

---

## Roles & Authentication
- **Developer / Console Access**: Developer API token / Bearer token (`cpt_...`) or Clerk session header (`/api/v1/console/*`). Console workspace routes additionally enforce role-based capabilities (AUTH-01), and high-impact ones reject `cpt_` tokens outright — see [Workspace Capability RBAC (AUTH-01)](#workspace-capability-rbac-auth-01).
- **End-User / Public SDK Access**: Identified by `appKey` in path/query/body, optional `X-User-Token` header (UUID) for anonymous user voting, comment tracking, roadmap personalization, and upload session creation (`/api/v1/public/*`, `/api/v1/feedback`, `/api/v1/feature-requests`, `/api/v1/uploads/*`). On `POST /api/v1/uploads/sessions` the header is **required** for anonymous callers — see [Uploader Identity Binding (SEC-28)](#uploader-identity-binding-on-upload-sessions-sec-28). On `GET /api/v1/feature-requests` the header replaces the deprecated `?userToken=` query parameter, and `POST /api/v1/me/link` combines it with a Clerk session to confirm identity linking — see [End-User Token Header & Identity Linking (SEC-12)](#end-user-token-header--identity-linking-sec-12). `POST /api/v1/me/erase` uses it for self-service data erasure — see [Self-Service Data Erasure (PRIV-01)](#self-service-data-erasure-priv-01).

---

## Key Public & SDK Endpoints

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/public/config/:appKey` | `GET` | Fetches the `PublicAppConfig`: app metadata, store/website links (`websiteUrl`), branding flags (`hideSiteBranding`), enabled platforms, and anonymous-access settings. Private apps and unknown keys both fail closed with `404 {"error": "App not found"}` (SEC-37). |
| `/api/v1/public/workspaces/:workspaceSlug/apps/:appSlug/config` | `GET` | Same `PublicAppConfig` resolved by workspace and app slugs instead of app key; the same fail-closed `404` applies to private apps. |
| `/api/v1/public/columns/:appKey` | `GET` | Roadmap Kanban columns sorted by position. |
| `/api/v1/public/versions/:appKey` | `GET` | Release versions sorted by position. |
| `/api/v1/public/apps/:appKey/changelog` | `GET` | Published release notes and changelog items. Cursor-paginated: `limit` (integer, 1–100, default 100) and opaque `cursor` (from the previous `nextCursor`); returns `{entries, hasMore, nextCursor}`. Malformed cursors fail with `400` `{"error": "Invalid cursor"}`. |
| `/api/v1/public/apps/:appKey/changelog/subscribe` | `POST` | Subscribe an email to changelog updates (double opt-in). Always `201 {"subscribed": true}` — the former `alreadySubscribed` field was removed, so the response never reveals prior state. The address starts *pending* and a single-use confirmation email is sent (resends within a 15-minute cooldown return `201` without dispatching a duplicate email). Rate limited per client IP (`429`). |
| `/api/v1/public/apps/:appKey/changelog/confirm` | `GET` | **Non-destructive** confirmation interstitial (SEC-35): validates the emailed token (read-only) and renders an HTML page whose form POSTs the token; a GET **never modifies** the subscription, so email security gateways (SafeLinks/Proofpoint/Mimecast URL detonation) and link prefetchers cannot consume the single-use token. JSON-only clients (`Accept: application/json` without `text/html`) get `405 {"error": "GET does not confirm subscription. POST the token to this endpoint to confirm."}` with `Allow: POST`. Missing token → `400 {"error": "Missing confirmation token"}`; unknown/expired/already-used tokens → `400 {"error": "Invalid or expired confirmation token"}` (uniform, no oracle). Rate limited per client IP (`429`). |
| `/api/v1/public/apps/:appKey/changelog/confirm` | `POST` | **The only mutating double-opt-in confirmation** (SEC-35): consumes the single-use emailed token and flips the subscription to *confirmed*. Token accepted via query string (`?token=...`), JSON body `{"token": "..."}`, or the interstitial form's `token` field (urlencoded/multipart) — query wins when several are present. Replays and expired tokens fail uniformly with `400` (single-use, no oracle). Browser form submissions (`Accept: text/html`) get the HTML confirmation landing page; everything else gets `{"confirmed": true}`. Rate limited per client IP (`429`). |
| `/api/v1/public/apps/:appKey/changelog/unsubscribe` | `GET` | **Non-destructive** confirmation interstitial (PROD-20): renders an HTML form that POSTs the token; the subscription is never modified. Email security gateways and link prefetchers that GET this URL cause no side effect. JSON-only clients (`Accept: application/json`) get `405` with `Allow: POST`. Missing/invalid tokens fail uniformly with `400`. Rate limited per client IP (`429`). |
| `/api/v1/public/apps/:appKey/changelog/unsubscribe` | `POST` | The only destructive path (also serves RFC 8058 `List-Unsubscribe=One-Click`): token via query string, JSON body `{"token": "..."}`, or form field. The bare-email unsubscribe (`{"email": "..."}`) was **removed**. Always `{"unsubscribed": true}` whether or not the subscription existed; `400 {"error": "An unsubscribe token is required"}` without a token; `400 {"error": "Invalid or expired unsubscribe token"}` for bad ones. Browser form submissions (`Accept: text/html`) get an HTML landing page. Rate limited per client IP (`429`). |
| `/api/v1/public/digest/unsubscribe` | `GET` | **Non-destructive** weekly-digest unsubscribe confirmation interstitial (PRIV-07): validates the signed token and renders an HTML form that POSTs it back; notification preferences are never modified by a GET. JSON-only clients (`Accept: application/json` without `text/html`) get `405` with `Allow: POST`. Missing token → `400 {"error": "Missing unsubscribe token"}`; invalid/expired → `400 {"error": "Invalid or expired unsubscribe token"}`. Rate limited per client IP (`429`, shared with the changelog unsubscribe budget). See [Weekly Digest Unsubscribe (PRIV-07)](#weekly-digest-unsubscribe-priv-07). |
| `/api/v1/public/digest/unsubscribe` | `POST` | The only destructive digest path (serves RFC 8058 `List-Unsubscribe=One-Click`): token via query string (takes precedence), JSON body `{"token": "..."}`, or a `token` form field. Success → `{"unsubscribed": true}` (HTML landing page when `Accept` includes `text/html`); replay is idempotent. `400 {"error": "An unsubscribe token is required"}` without a token; `400 {"error": "Invalid or expired unsubscribe token"}` for bad ones. Clears only the workspace `weekly.digest` **email** event — inbox notifications are unchanged. Rate limited per client IP (`429`). See [Weekly Digest Unsubscribe (PRIV-07)](#weekly-digest-unsubscribe-priv-07). |
| `/api/v1/public/apps/:appKey/user` | `PUT` | Update host app user attributes (paying, MRR, currency). Bodies reporting any payment attribute (`isPaying`, `mrr`, `plan` — an explicit `null` counts) must carry an HMAC-signed `signature` + `timestamp`; see [SDK Payment-Attribute Signing (DATA-03)](#sdk-payment-attribute-signing-data-03). Rate limited per client IP: 60 requests/minute, `429` on bursts — retry with exponential backoff when syncing many users behind one shared IP. |
| `/api/v1/feature-requests` | `GET` | List/search feature requests (`limit` 1–200, default 50; legacy `offset`; `versionId`; `q`). Cursor-paginated (DATA-01): opaque `cursor` from the previous `nextCursor`; returns `{requests, total, hasMore, nextCursor}` with `nextCursor` `null` on the last page; malformed cursors fail with `400` `{"error": "Invalid cursor"}`. Boards with anonymous roadmap view disabled answer `401` `authentication_required` to unauthenticated reads. Personalize with the **`X-User-Token` header**; the legacy `?userToken=` query parameter is deprecated (see [End-User Token Header & Identity Linking (SEC-12)](#end-user-token-header--identity-linking-sec-12)). |
| `/api/v1/feature-requests` | `POST` | Submit a new feature request. |
| `/api/v1/feature-requests/:id/vote` | `POST` | Toggle (upvote / un-upvote) the caller's vote; body `{appKey, userToken}` (or an authenticated Clerk session), returns `{voted, voteCount}`. Rate limited per client IP — 20 requests/minute with a vote-specific `429` body (see [Rate Limiting](#rate-limiting-429-too-many-requests)). |
| `/api/v1/feature-requests/:id/vote` | `DELETE` | Explicitly remove the caller's vote, returns `{voted: false, voteCount}`. Same per-client-IP vote rate limit applies (`429`). |
| `/api/v1/feature-requests/:id/comments` | `GET` | List comments and @replies on a feature request. |
| `/api/v1/feature-requests/:id/comments` | `POST` | Post a comment or @reply on a feature request. |
| `/api/v1/me/link` | `POST` | Explicitly link an anonymous end-user profile to the signed-in Clerk identity (SEC-12). Requires the `X-User-Token` header plus a Clerk session; see [End-User Token Header & Identity Linking (SEC-12)](#end-user-token-header--identity-linking-sec-12). |
| `/api/v1/me/erase` | `POST` | Self-service data erasure (PRIV-01): erases the caller's own end-user profile for one app — rotates the anonymous token, clears stored PII, and anonymizes feature requests/votes/comments. Authenticates with the `X-User-Token` header or a Clerk session; see [Self-Service Data Erasure (PRIV-01)](#self-service-data-erasure-priv-01). |
| `/api/v1/users/:userId/profile` | `GET` | Public user profile, apps, and recent comments. `userId` may be an app-scoped pseudonym (`u_*`); pass `?appKey=` to resolve those. Unknown `u_*` ids return `404` without a reverse-lookup scan (SEC-34). Rate limited per client IP: 60 requests/minute in the same bucket as the `PUT .../user` upsert, `429` on bursts. |
| `/api/v1/feedback` | `POST` | Submit feedback draft with optional attachments. Every referenced `uploadId` must have passed content scan; a rejected attachment fails the whole submission with `422` `scan_rejected`. Every referenced `uploadId` must also come from a session created by the **same identity** (see [Uploader Identity Binding (SEC-28)](#uploader-identity-binding-on-upload-sessions-sec-28)). Free-form `metadata` is sanitized server-side before persistence — shrunk, never rejected (see [Feedback Metadata Redaction (PRIV-01)](#feedback-metadata-redaction-priv-01)). |
| `/api/v1/uploads/sessions` | `POST` | Create an upload session (session token + reserved per-file upload slots) after Turnstile, app-policy, and byte-quota validation. Anonymous callers **must** send `X-User-Token`; unbound sessions are rejected (SEC-28). See [Feedback Attachment Upload Lifecycle](#feedback-attachment-upload-lifecycle-upload-sessions). |
| `/api/v1/uploads/:uploadId` | `PUT` | Upload one reserved session slot's bytes. `Authorization: Bearer <sessionToken>` (or `X-Upload-Session-Token`); raw binary body or `multipart/form-data` with a `file` field. `POST` is accepted as an alias. |
| `/api/v1/uploads/images` | `POST` | Legacy multipart image upload — **now requires an upload session** (`401` `upload_session_required` without one; `Authorization` header or `sessionToken`/`sessionId` form field). PNG/JPEG/WebP/GIF only — SVG and declared-vs-content mismatches fail with `415` (see the media-type policy below). |
| `/api/v1/feedback/attachments/:id/download` | `GET` | Private attachment download (authorized). Signed `?token=` link or workspace-member auth; anonymous callers without a valid token get `403`. |
| `/api/v1/uploads/r2` | `POST` | Removed tombstone: always responds `410 Gone` (create an upload session at `/api/v1/uploads/sessions` instead). |

> **Changelog double opt-in flow (SEC-14 + SEC-35, as shipped):** `POST /changelog/subscribe` stores the address as *pending* and emails a single-use confirmation link. That link is a `GET /changelog/confirm?token=...` URL, but since SEC-35 **GET is strictly non-destructive** — it validates the token and renders an HTML interstitial whose form POSTs the token back; only `POST /changelog/confirm` consumes the token and flips the subscription to *confirmed* (browser form submissions with `Accept: text/html` get the HTML landing page; other clients get `{"confirmed": true}`). Email-scanner URL detonation (SafeLinks/Proofpoint) is therefore harmless for confirmation links: fetching the link leaves the subscription *pending* with the token intact, so links no longer need to be treated as single-shot or protected from prefetchers. The same interstitial pattern applies to unsubscribe (`GET .../unsubscribe?token=` is safe, only `POST` unsubscribes) and the weekly digest (PRIV-07). Responses across all three flows are uniform (no membership oracle), and all of these public writes share the per-IP `PUBLIC_WRITE_RATE_LIMITER` budget — retry `429`s with exponential backoff.

> **Private-app config fail-closed (SEC-37):** both config routes return `404 {"error": "App not found"}` for private apps (`allowPublic = false`) — identical to the unknown-key response, so callers cannot distinguish the two and must treat 404 as "not found or not public". Do not expect a `200` body with `allowPublic: false`; only public apps get a `200 PublicAppConfig` (with `allowPublic: true`, schema unchanged). Other public data endpoints (columns, versions, feature requests, changelog, feedback, uploads) already reject private apps with `403`.

> **Public changelog pagination (PROD-28, `ListPublicChangelogResponse`):** `GET /api/v1/public/apps/:appKey/changelog` is keyset-cursor-paginated. Read `hasMore` / `nextCursor` from the response and pass `nextCursor` back as the `cursor` query parameter until `hasMore` is `false` (`nextCursor` is `null` on the last page). Treat the cursor as opaque — never parse, construct, or persist one beyond a forwarding step; malformed cursors return `400` `{"error": "Invalid cursor"}`. Entries are ordered newest-first, `limit` is clamped server-side to 1–100 (default 100), and keyset semantics keep an in-progress walk stable even if new entries are published mid-walk. Clients that only read `.entries` remain fully compatible.

> **Public feature-request list pagination (DATA-01, `PaginatedFeatureRequests`):** `GET /api/v1/feature-requests` accepts an opaque keyset `cursor` over the `(createdAt DESC, id DESC)` order alongside the legacy `offset` parameter. The response always includes `hasMore` and `nextCursor` next to `requests`/`total` — `hasMore` is exact (the server fetches one extra row) and `nextCursor` is `null` on the last page. Walk large boards by starting without a cursor and echoing `nextCursor` back as `?cursor=` until it is `null`; a request that supplies `cursor` ignores `offset` entirely. Treat the cursor as opaque — never parse, construct, or persist one beyond a forwarding step; malformed cursors return `400` `{"error": "Invalid cursor"}`. `total` is computed for the current filters independently of the cursor, so it stays constant across pages; `limit` stays clamped to 1–200 (default 50), and `q`/`versionId` filters compose with cursor paging. Keyset semantics keep an in-progress walk stable even when new requests are submitted mid-walk, and clients that only read `requests`/`total` remain fully compatible.

---

## Submission Quota Errors (`402 Payment Required`)

`POST /api/v1/feature-requests` (and, with the same contract, `POST /api/v1/feedback`) responds with `402 Payment Required` and an `ErrorResponse` body (`{"error": string, "code"?: string}`) when the app's workspace cannot accept new submissions:

| `code` | Meaning | Actionable guidance |
|---|---|---|
| `tier_limit_submissions` | The workspace reached its monthly submission quota. | Do not retry automatically. Tell the user to upgrade the workspace plan (Console → Billing) or wait for the quota to reset, then resubmit. |
| `subscription_inactive` | The workspace subscription is inactive or canceled. | Do not retry automatically. Tell the user to renew/reactivate the subscription (Console → Billing); submissions keep failing until then. |

Agents and SDK clients should parse the `code` field, treat `402` as a deterministic business rule (never a transient error), and surface the guidance above to the end user.

---

## Rate Limiting (`429 Too Many Requests`)

Public write endpoints — and, since SEC-34, the unauthenticated profile read — are budgeted **per client IP** (keyed on `CF-Connecting-IP`). Throttled requests get `429 {"error": "Too many requests. Please try again shortly."}` — except the feature-request vote endpoints, which return a vote-specific body: `429 {"error": "Too many votes. Please try again shortly."}`:

| Endpoints | Budget | Why |
|---|---|---|
| `POST .../changelog/subscribe`, `GET`/`POST .../changelog/confirm`, `GET`/`POST .../changelog/unsubscribe`, `GET`/`POST .../public/digest/unsubscribe` | 10 requests / 60 s | Subscribe emails third parties and confirm/unsubscribe mutate subscription state, so the budget is tight. The changelog subscribe/confirm/unsubscribe and digest unsubscribe flows **share one per-IP bucket** (`PUBLIC_WRITE_RATE_LIMITER`). |
| `PUT /api/v1/public/apps/{appKey}/user`, `GET /api/v1/users/{userId}/profile` | 60 requests / 60 s | Every never-seen `userToken` mints an end-user row, and the unauthenticated profile GET resolves `u_*` ids; both share one per-IP bucket (`PUBLIC_USER_RATE_LIMITER`, SEC-14/SEC-34), so a profile-heavy hovercard can exhaust the attribute-sync budget and vice versa. |
| `POST`/`DELETE /api/v1/feature-requests/{id}/vote` | 20 requests / 60 s | The anonymous voter identity is a client-minted `userToken` UUID, so votes get their own per-IP cap (SEC-09) — one IP minting fresh tokens must not be able to inflate vote counts. |

Retry guidance: treat `429` as transient — wait and retry with exponential backoff and jitter, never in a tight loop. SDKs syncing attributes for many users behind one shared IP (office NAT, CI farm) are the typical source of `429`s; batch or spread those syncs. Profile/hovercard rendering draws from the same 60/minute bucket: cache profile responses client-side instead of refetching per render. For votes, `429` is a recoverable user-facing condition: surface a friendly "you're voting too fast, try again in a minute" message instead of auto-retrying; normal tapping across a roadmap stays well under the 20/minute budget.

---

## Attachment Content-Scan Rejections (`422 Unprocessable Entity`)

`POST /api/v1/feedback` requires every referenced `uploadId` to have **passed content inspection** at submission time (SEC-25). If an attachment was rejected during upload inspection (prohibited file type, malware signature, …), the whole submission fails with `422 Unprocessable Entity` instead of binding the invalid file:

```json
{
  "error": "Upload object <uploadId> was rejected by content scan: <reason>",
  "code": "scan_rejected"
}
```

Contract:

- The `: <reason>` suffix is present only when the scanner recorded a reason. Parse the `code` field, never the message text.
- `422` is reserved for `scan_rejected`; every other attachment-validation failure (unknown `uploadId`, object from another workspace or app, non-`uploaded` state, already finalized into another submission, expired session, uploader mismatch, more than 8 attachments) remains `400` with the same `{"error": string, "code"?: string}` shape.
- All attachments are validated before any is bound, so a scan-rejected submission binds none of its attachments — the scan-passing `uploadId`s stay unbound and reusable for a corrected resubmission.
- Rejection is deterministic — the same file fails again on retry, so never retry automatically. Tell the end user that the referenced attachment could not be uploaded because content inspection rejected it, and let them remove or replace the file before resubmitting.

The OpenAPI document exposes this `422` response on `POST /api/v1/feedback`.

---

## Uploader Identity Binding on Upload Sessions (SEC-28)

Every upload session is bound to an uploader identity at creation time, and feedback submission must present the **same identity**. Unbound sessions can no longer be created (fail-closed).

**Session creation — `POST /api/v1/uploads/sessions`:**

- **Anonymous callers** must send a valid `X-User-Token` UUID header (any UUID version, case-insensitive, surrounding whitespace ignored). Missing or malformed tokens are rejected with `400`:

```json
{ "error": "A valid X-User-Token UUID is required to create an upload session when not signed in", "code": "uploader_identity_required" }
```

- **Signed-in Clerk callers** are bound to their Clerk user id; `X-User-Token` is optional for them and ignored for identity resolution.

**Feedback submission — `POST /api/v1/feedback`:**

- Requests referencing `uploadId`s must present the identity that created the session: the same `X-User-Token`, or the same Clerk session. The check is **fail-closed** — a session that stored an identity plus a submitter with no identity is also a mismatch. Failures return `400`:

```json
{ "error": "Upload session was created by a different uploader", "code": "uploader_mismatch" }
```

- The referenced file is **not** attached and is left unfinalized, so a corrected resubmission (with the matching identity) can succeed without re-uploading.

Contract for clients and SDKs:

1. When creating an upload session for an anonymous end user, send the **same** `X-User-Token` already used for feedback submit and voting — never create sessions without an identity.
2. Never submit feedback with a different token (or a Clerk session) than the one used at session create.
3. Parse the `code` field and surface `uploader_identity_required` and `uploader_mismatch` as deterministic client errors (fix the identity, then retry), never as generic retryable `400`s.
4. The OpenAPI document exposes the `X-User-Token` header parameter on `POST /api/v1/uploads/sessions`.

---

## Feedback Attachment Upload Lifecycle (Upload Sessions)
Feedback attachments are uploaded through **pre-allocated upload sessions**; direct external URLs and raw storage keys are rejected at submission time. The flow is always: **create session → upload each reserved slot → submit feedback referencing the `uploadId`s**. Agents and automated tools must create the session first — there is no un-sessioned upload path left on the API.

**Step 1 — create the session: `POST /api/v1/uploads/sessions`** (JSON body):

```json
{
  "appKey": "app_xxx",
  "files": [
    { "clientFileId": "local-file-1", "filename": "screenshot.png", "mimeType": "image/png", "size": 102400 }
  ]
}
```

- 1–8 `files` per session. Each entry takes an optional `clientFileId` (echoed back for client-side correlation), a `filename`, and MIME/size in either the `mimeType`/`size` or the legacy `contentType`/`sizeBytes` spelling. An optional `purpose` field selects `feedback_attachment` (default) or `branding`; `turnstileToken` is optional and only checked when the app has Turnstile configured.
- `201` response: `{ "sessionId", "sessionToken", "expiresAt", "uploads": [...] }`. The `sessionToken` (prefix `cpt_up_`) authorizes the uploads and expires in about **1 hour**; each `uploads[]` item is `{ "clientFileId", "uploadId", "uploadUrl", "filename", "mimeType", "maxBytes" }` with `uploadId`s shaped `upl_<32hex>` and `uploadUrl` the relative `PUT` path. Treat `maxBytes` as the per-file cap.
- Per-file policy failures surface at creation: `413` `file_too_large` (over the app's configured attachment limit), `400` `executable_extension_prohibited`, `400` `unsupported_mime_type`, and `429` `daily_storage_quota_exceeded` (workspace daily upload bytes). Shared submission-endpoint errors also apply (`402` quotas, `403` Turnstile, `404` unknown app, `401` when the app disables anonymous feedback).

**Step 2 — fill each reserved slot: `PUT /api/v1/uploads/{uploadId}`** (`POST` works too). Send the raw bytes with `Content-Type: <file mime>` and a binary body, or `multipart/form-data` with a `file` field. Authorize with `Authorization: Bearer <sessionToken>` (an `X-Upload-Session-Token` header is also accepted).

- Success `200`: `{ "uploadId", "status": "uploaded", "stored": true, "filename", "mimeType", "size", "sha256" }`.
- Uploads are bounded streams: an over-cap body fails with `413` `file_too_large` (the `Content-Length` header is checked before a byte is read).
- Content inspection (magic bytes, malware sniffing) runs on the stored bytes: a rejected file is marked scan-rejected and answers `415` with the inspection reason. Deterministic — never retry; drop or replace the file.
- Session/slot errors: `401` `unauthorized` (no token), `401` `session_invalid_or_expired`, `401` `session_expired`, `404` `upload_not_found` (that `uploadId` is not in this session), `409` `already_uploaded` (slots are single-shot), `409` `session_not_pending` (the session was already finalized into a submission). The item write is metered per client IP like session creation, so `429` can occur.

**Step 3 — submit feedback with the finalized ids: `POST /api/v1/feedback`**:

```json
{
  "appKey": "app_xxx",
  "title": "Issue title",
  "description": "Issue description",
  "platform": "universal",
  "uploadIds": ["upl_xxx"]
}
```

- Up to 8 `uploadIds`. The legacy `attachments` array is still accepted when every entry carries an `uploadId`; an attachment supplying a raw `url` or storage `key` instead is rejected with `400` `{"error": "Direct attachment URLs and keys are forbidden. Attachments must reference verified upload sessions via uploadId.", "code": "direct_attachment_forbidden"}`.
- Every referenced upload must be in `uploaded` state, unbound to another submission, belong to the same app/workspace, have passed content scan (`422` `scan_rejected`, SEC-25), and come from a session created by the **same identity** (`400` `uploader_mismatch`, SEC-28). All validation happens before anything binds, so a failed submission leaves its good `uploadId`s reusable for a corrected resubmission.
- A successful submission finalizes the uploads (they can never be re-bound) and answers `201` (or `202` when GitHub delivery is queued, `200` when forwarded inline) with `{ "submissionId", "forwardedToGithub", … }` — no attachment payloads. Download links are issued separately through the workspace Console; when the workspace lacks signing configured the API logs a warning and omits the links.

**Private attachment download — `GET /api/v1/feedback/attachments/{id}/download`** (`GET …/attachments/{id}` is an alias). Authorized by either a signed download token (`?token=<HMAC>`; tokens are bounded, at most 30 days) or workspace-member auth (`workspace.read`, e.g. a `cpt_` token in the owning workspace). Without either: `403` `{"error": "Authentication or valid download token required", "code": "unauthorized"}`. Unknown attachment → `404` `attachment_not_found`; missing storage object → `404` `file_not_found`. Responses are `Content-Disposition: attachment` with `private, no-store` caching — do not proxy or pre-fetch these URLs.

Client guidance:

1. Create the session **before** uploading anything, and reuse one session for all files of a single composer submission (1–8 slots).
2. Present the **same identity** on session creation and feedback submission — for anonymous users that means the same `X-User-Token` on both calls (SEC-28).
3. Send an honest `Content-Type` and stay under the slot's `maxBytes`; treat `413`/`415` as deterministic client errors and surface them to the user.
4. Never fabricate, guess, or pre-assign `uploadId`s, and never send raw attachment URLs or storage keys — `direct_attachment_forbidden` is a hard rejection.

---

## End-User Token Header & Identity Linking (SEC-12)

### `X-User-Token` replaces the `userToken` query parameter

`GET /api/v1/feature-requests` personalizes responses (own submissions, vote state) when an end-user token is presented. Send it as a request **header**:

```
X-User-Token: <uuid>
```

- The legacy `?userToken=<uuid>` query parameter still works but is **deprecated** — tokens in URLs leak into access logs, referrers, and network traces. Requests that rely on it succeed, but the response carries `Warning: 299 - "userToken query parameter is deprecated; send X-User-Token header instead"` and `X-Deprecated-Query-Token: deprecated`.
- When both are sent, the **header takes precedence** and no deprecation headers are returned.
- The token is honored only when it is a valid UUID (any version, case-insensitive, surrounding whitespace trimmed); anything else is treated as an anonymous request.
- Search responses set `Vary: X-User-Token`; only fully anonymous searches are cache-shared, so never route token-authenticated searches through a shared URL-keyed cache.

### `POST /api/v1/me/link` — confirm identity linking

Explicitly binds an authenticated end-user identity to an anonymous profile (OpenAPI tag `Privacy`, "Link End-User Identity (Self-Service)"). Rate limited per client IP like other public writes: throttled calls get `429 {"error": "Too many submissions. Please try again shortly."}` — retry with exponential backoff.

- **Headers**: `X-User-Token: <uuid>` (**required**, must be a valid UUID) and `Authorization: Bearer <Clerk session JWT>` (**required**). Developer `cpt_` API tokens are **not** accepted — the caller must be the signed-in end user.
- **Body**: `{"appKey": "<8-128 chars>"}` — the app the profile belongs to.

| Status | When | Body |
|---|---|---|
| `200` | Profile linked; also idempotent when already bound to the same identity | `{"linked": true, "endUserId": "…", "clerkUserId": "…"}` |
| `400` | Missing/malformed `X-User-Token` (`{"error": "A valid X-User-Token header is required"}`), malformed body, or link failure | `{"error": string, "code"?: string}` |
| `401` | No valid Clerk session | `{"error": "Authentication required", "code": "authentication_required"}` |
| `404` | Unknown `appKey` | `{"error": "App not found"}` |
| `409` | Profile already confirmed to a **different** account | `{"error": "End-user profile is already linked and confirmed to another account", "code": "already_linked"}` |

Linking semantics:

- No profile exists for (`appKey`, `X-User-Token`) yet → one is created already bound to the authenticated identity and marked confirmed. There is **no** `404` for a missing profile — `404` is reserved for an unknown `appKey`.
- An **anonymous** row (created earlier by voting/feedback with only `X-User-Token`, no Clerk binding) is claimed by the authenticated user and marked confirmed — this is the recovery path for unclaimed profiles.
- A row already bound to the **same** Clerk user resolves idempotently with `200` (the binding is re-confirmed).
- A row already bound to a **different** Clerk user is never hijacked: `409 already_linked`, deterministic — never retry automatically.

Client guidance: call this once when the end user signs in to the host app (at that moment the client holds both the anonymous `X-User-Token` and a Clerk session), then keep using the same `X-User-Token` for voting, feedback, comments, and upload sessions.

---

## User Identifiers on Public Endpoints (PRIV-06)

Public board and comment payloads identify users with **deterministic app-scoped pseudonyms**, never raw identity-provider ids:

- `GET /api/v1/feature-requests`: `requesterClerkId`, `recentCommenters[].clerkUserId`
- `GET` / `POST /api/v1/feature-requests/:id/comments`: `authorClerkId`, `replyToClerkId`

Contract:

- Values are `u_` followed by 32 lowercase hex chars (e.g. `u_9f2c…`). Field names are unchanged — only the value format changed. Legacy `user_*` Clerk ids may still appear in old cached payloads, so **never validate or assume a `user_` prefix** on user id strings.
- Ids are stable for a given (app, user) pair, so reply threading (`replyToClerkId`) and author attribution keep working **within one app**. They are **unlinkable across apps**: never join, deduplicate, or correlate user ids between two different `appKey`s.
- `GET /api/v1/users/:userId/profile` accepts `u_*` ids **only together with the `appKey` query parameter** (the id can only be reversed within its app). Without `appKey`, a `u_*` request returns `404`, and an unknown (unmapped) `u_*` id returns the same `404 {"error": "User profile not found"}` — since SEC-34 the endpoint never runs the full-app candidate scan (unique unknown ids are cache-busting), so an indexed miss is terminal and retrying cannot change the answer. Legacy `user_*` ids remain accepted without `appKey` for existing `/u/` links, and the endpoint is additionally rate limited per client IP (see [Rate Limiting](#rate-limiting-429-too-many-requests)).
- Public profiles are opt-in. A user who never created a public profile resolves to a placeholder — the requested id echoed back with `displayName: null` and empty `publicApps` / `recentComments` — and there is no existence oracle for raw ids. `publicApps` lists only apps of workspaces where the user is an **owner**; the response has no top-level `hideComments` (comment visibility is applied server-side).

---

## SDK Payment-Attribute Signing (DATA-03)

`PUT /api/v1/public/apps/:appKey/user` is reachable with nothing but the public `appKey`, so self-declared payment attributes cannot be trusted: whenever the body contains any of `isPaying`, `mrr`, or `plan` (**an explicit JSON `null` counts too**), the request must also carry two extra body fields, `signature` (64-char hex HMAC-SHA256, case-insensitive) and `timestamp` (epoch-seconds integer). Both are transport fields — they are never persisted. Verification runs before any `end_users` row is minted, so rejected requests leave no trace. Identity-only and `currency`-only writes keep working unchanged.

The HMAC key is the app's **SDK signing secret**, generated by the developer in the console (*App Access → App Credentials → SDK signing secret*). Unsigned payment attributes fail as follows:

| Status | `code` | Trigger |
|---|---|---|
| `422` | `payment_attributes_require_signature` | Payment attribute in the body without `signature` + `timestamp`. |
| `422` | `sdk_signing_secret_not_configured` | The app has no SDK signing secret yet. |
| `401` | `stale_signature` | `timestamp` more than ±300 seconds from the server clock. |
| `401` | `invalid_signature` | Signature mismatch — wrong key or tampered values. |

### Canonical string

HMAC-SHA256-sign the exact bytes of this newline-joined string (no trailing newline) and hex-encode the digest:

```
cpt-user-attrs-v1
<appKey>
<userToken>
<isPaying: true|false|unset>
<plan: value|null|unset>
<mrr: canonicalNumber|null|unset>
<currency: valueAsSent|unset>
<timestamp: epochSeconds>
```

- Absent fields sign as `unset`; explicit JSON `null` signs as `null`. Omitting `plan` and sending `"plan": null` produce different signatures.
- `canonicalNumber`: render the raw JSON number with two-fraction-digit `toFixed(2)` semantics, then strip trailing `0`s and a trailing `.` (`1200.00 → "1200"`, `99.50 → "99.5"`). Ties pick the larger n on the exact binary value: `10.125 → "10.13"`, and `1200.005 → "1200.01"` because its binary double sits *above* the decimal value while `99.995` carries to `"100"`.
- Values are signed **as sent**: `currency` before the server's uppercase normalization, `plan` verbatim including Unicode.
- `userToken` is the resolved token — the body `userToken` when present, else the `X-User-Token` header value; sign whichever identifies the user in this request.
- Sign immediately before sending: a `timestamp` older or newer than ±300 s from server time fails with `stale_signature`.

Coding agents can generate reference signatures to cross-check platform implementations with `cupthread api sign-user-attrs` (see the `cupthread-cli` skill).

---

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
| `workspace.delete` | ❌ | ❌ | ✅ (declared; no route uses it yet) |

Two structured 403 codes can come back from these routes (both with HTTP 403):

- `capability_required` — the caller's role does not include the route's capability:
  `{"error": "Access denied: your workspace role does not include the '<capability>' capability", "code": "capability_required"}`
- `interactive_session_required` — the route's capability is in the interactive-only set (`changelog.publish`, `members.manage`, `billing.manage`, `integration.manage`, `workspace.delete`) and the caller authenticated with a `cpt_` API token, regardless of role (mirroring the `/api/v1/console/tokens` management rule):
  `{"error": "This action requires an interactive session; API tokens are not permitted", "code": "interactive_session_required"}`

The checks are ordered: **role first, then token type**. A member-role `cpt_` token on a `members.manage` route therefore gets `capability_required` (role too low), while an admin/owner `cpt_` token gets `interactive_session_required` (role sufficient, token type rejected). Interactive Clerk web sessions (Console web UI) never see `interactive_session_required` — note that the CLI's `auth login` OAuth flow issues a `cpt_` access token too, so from the CLI every interactive-only capability is unreachable.

Unaffected for `cpt_` tokens: all `workspace.read` lookups (including `GET .../members`, `GET .../invitations`, `GET .../billing`, and integration status reads), `triage`, `content.manage` (changelog **drafts and edits** included — only publishing/scheduling moved out into `changelog.publish`, see the next section), `app.configure`, and imports. Two GET exceptions are interactive-session-only despite being reads: `GET .../billing/portal` (billing portal redirect, `billing.manage`) and `GET .../integrations/:provider/authorize` (OAuth authorize URL, `integration.manage`). Public feedback/SDK endpoints are unchanged.

---

## Changelog Publishing Capability (SEC-40)

Publishing or scheduling a changelog entry emails every confirmed subscriber (a blast), so it carries its own `changelog.publish` capability — admin/owner only — on top of the `content.manage` access the routes already require (upstream CupThread/SaaS#286, merge `0bc77d6`). Gate per endpoint:

| Endpoint | Gated request shape | Requirement |
|---|---|---|
| `POST .../changelog` | body has `publishNow: true` or non-null `scheduledAt` | `changelog.publish` (checked **in addition to** the route's `content.manage` access) |
| `PUT .../changelog/:entryId` | body has a string `scheduledAt` (`""`/null clears it) | `changelog.publish` |
| `POST .../changelog/:entryId/publish` | always | `changelog.publish` (route-wide; **changed** from `content.manage`) |

Not gated: creating/updating/deleting **drafts** without publish/schedule intent, listing, and unpublishing stay under plain `content.manage` (members and `cpt_` tokens keep full draft workflow access).

Denials follow the AUTH-01 order (role first, then token type):

- member (any auth type) on a gated path → `403 capability_required` naming `'changelog.publish'`
- admin/owner with a `cpt_` API token on a gated path → `403 interactive_session_required` — publish from the Console web UI instead (the CLI's OAuth login also issues a `cpt_` token, so no CLI credential can publish)

Server side, every denial emits a structured `authz_denied` audit event (`reason: missing_capability` or `interactive_session_required`), and a successful publish records the publishing Clerk user and enqueues the blast job. The console publish routes are not declared in the OpenAPI document (only the public read/subscribe paths are), so there is no spec surface to regenerate.

---

## Feedback Image Media-Type Policy (415 Unsupported Media Type)

`POST /api/v1/uploads/images` (end-user feedback image upload) enforces a strict media-type policy:

- **Accepted types**: `image/png`, `image/jpeg`, `image/webp`, `image/gif` only. Any other declared type fails earlier with `400 {"error": "Only PNG, JPEG, WebP, and GIF images are supported."}`; empty files also fail with `400`, and files over 10 MB with `413`.
- **SVG is rejected with `415`** — when the upload declares `image/svg+xml`, or when a `.svg` filename falls through the server's extension fallback (clients that send no per-part content type). SVG executes script in browsers and is never stored from end-user uploads: `415 {"error": "SVG images are not supported. Upload a PNG, JPEG, WebP, or GIF image."}`
- **Declared MIME must match the file content.** The server sniffs magic bytes (PNG, JPEG, GIF87a/GIF89a, WebP/RIFF). An unrecognized signature, or a mismatch with the declared type (e.g. HTML or SVG bytes named `.png`), fails with `415 {"error": "File content does not match declared image type (<declared>)."}`

The `415` bodies carry no `code` field — match on the status, not on an error code.

Client guidance:

- Restrict client-side image pickers / file-type allowlists to PNG, JPEG, WebP, and GIF (drop SVG for feedback screenshots).
- Treat `415` as a deterministic client error: surface a user-facing "unsupported image type" message and let the user pick a different file; never retry automatically.

**Console-configured app icons are exempt.** `POST /api/v1/console/workspaces/{wsId}/apps/{appId}/icon` (capability `app.configure`, so workspace admin/owner; `cpt_` API tokens are accepted) still accepts SVG icons, but screens them for active content — markers like `<script`, `javascript:`, `onload`/`onerror`/`onclick` handlers, `<!entity`, and `<foreignObject` fail with `415 {"error": "SVG contains prohibited active scripts or external entity references"}` — and applies the same magic-byte check. The endpoint stores the image and updates the app record (`iconUrl`) in the same request, returning the updated `AppRecord`.

---

## Request Correlation IDs (`X-Request-Id`, OPS-01)

**Every response — all endpoints, all status codes — carries an `X-Request-Id` response header** (OpenAPI shared component `XRequestId`): the caller's ID when the request supplied a format-valid one, otherwise a server-generated UUID. It is a correlation handle for server logs, telemetry, and support flows — never an authentication or authorization primitive.

Contract:

- Callers MAY send an `X-Request-Id` request header on any endpoint. It is honored only when it matches `^[A-Za-z0-9._-]{8,64}$` (a UUID qualifies); any other value is ignored and replaced server-side, and the response always echoes the effective value.
- CORS exposes the header to cross-origin browsers on every route class (`Access-Control-Expose-Headers: X-Request-Id`) and allowlists it as a request header on console and public preflights.
- Quote the response's `X-Request-Id` verbatim in bug reports and support requests so the exact request can be found server-side. The `cupthread` CLI does this for you: it sends `cli-<uuid>` per request and quotes the echoed value as `request-id=…` on errors (and on `api request` success lines).

---

## Weekly Digest Unsubscribe (PRIV-07)

Weekly digest emails now carry RFC 8058 one-click unsubscribe: a `List-Unsubscribe: <https://api.cupthread.com/api/v1/public/digest/unsubscribe?token=…>` header, a `List-Unsubscribe-Post: List-Unsubscribe=One-Click` header, and an in-body footer link to the same URL. The path is driven from the email footer / mail client, **not** from in-app SDK session calls — OpenAPI-generated clients pick up the new route, but runtime SDK methods are not required unless a client wants to drive the flow itself.

Both verbs live on `POST`/`GET /api/v1/public/digest/unsubscribe` (OpenAPI tag `Privacy`).

### `GET` is strictly non-destructive (PROD-20)

A browser GET validates the token and renders an interstitial confirmation page whose form POSTs the token to the same path. Email-security gateways and link prefetchers that GET the footer URL therefore cause **no side effect** — fetching never changes notification preferences.

- JSON-only clients (`Accept: application/json` **without** `text/html`) get `405 {"error": "GET does not unsubscribe. POST the token to this endpoint to unsubscribe."}` with an `Allow: POST` header.
- Missing token → `400 {"error": "Missing unsubscribe token"}`; invalid/expired token → `400 {"error": "Invalid or expired unsubscribe token"}` (uniform — token errors never reveal which case failed).

### `POST` is the only destructive path

Accepts the token from, in precedence order: query string (`?token=…`, what RFC 8058 one-click mail clients hit), JSON body `{"token": "…"}`, or a `token` field of an `application/x-www-form-urlencoded`/`multipart/form-data` body (what the GET confirmation form submits).

- Success → `200 {"unsubscribed": true}` (JSON), or an HTML landing page when `Accept` includes `text/html` (browser form submissions).
- Replay is idempotent — re-POSTing a consumed token still returns the same `200`.
- Missing token → `400 {"error": "An unsubscribe token is required"}`; invalid/expired → `400 {"error": "Invalid or expired unsubscribe token"}`.
- Effect: removes only `weekly.digest` from the workspace **email** channel's event mask (an empty mask is materialized as "all events except `weekly.digest`"). The email channel, all other events, and **inbox notifications are unchanged** — this is not a global notification kill switch.

Both verbs are rate limited per client IP with the same 10 requests / 60 s budget as the changelog subscribe/unsubscribe flow (`429 {"error": "Too many requests. Please try again shortly."}`).

### Token contract

- Shape: base64url(`payload`).base64url(`HMAC-SHA256 signature`) — exactly two dot-separated parts; the signature covers the purpose prefix `digest.unsubscribe.v1.` plus the payload, so tokens are domain-separated from changelog-unsubscribe and attachment-download tokens.
- Payload: `{"workspaceId", "email", "exp"}` — the owner email is lowercased at mint time and `exp` is a Unix millisecond timestamp ~**90 days** out, so a delayed click on a recent weekly mail still works.
- A token verifies only while the workspace still exists **and** its owner email still matches the payload — rotating the workspace owner invalidates outstanding tokens.
- Signing secret resolution is fail-closed: `DIGEST_EMAIL_TOKEN_SECRET`, then `CHANGELOG_EMAIL_TOKEN_SECRET`, then `JWT_SECRET`. With no usable secret, verification fails (`400`) **and** the digest cron skips the email entirely — a digest whose unsubscribe token cannot be minted is never sent.

Client guidance: never pre-fetch or "validate" footer URLs with a GET from automation (safe, but pointless — only POST mutates); don't fabricate, cache beyond 90 days, or attempt to parse tokens (they are opaque to clients); retry `429`s with exponential backoff.

---

## Self-Service Data Erasure (PRIV-01)

`POST /api/v1/me/erase` (OpenAPI tag `Privacy`, "Erase My Data (Self-Service)") lets the data subject erase their **own** end-user profile for one app. Anyone holding the secret anonymous token — or the matching signed-in Clerk user — may call it; developer `cpt_` API tokens are **not** an identity here.

**Request**: JSON body `{"appKey": "<8-128 chars>"}` plus identity — `X-User-Token: <uuid>` header (any UUID version; a malformed value is treated as absent) or a Clerk session (header optional for signed-in callers).

**Effect** (aggregates survive, attribution does not):

- The anonymous token is **rotated immediately** to an `erased-…` value: the old token stops working at once, and replaying the erase with it returns `404`.
- Stored PII is cleared: display name, email, the Clerk link, end-user attributes, and IP/user agent on feedback submissions matching the profile's email.
- Feature requests, votes, and comment attribution are anonymized; changelog subscriptions of the identity are removed. Feedback content and vote counts are preserved without personal attribution.

| Status | When | Body |
|---|---|---|
| `200` | Profile erased | `{"erased": true, "endUserId": "…"}` |
| `400` | Missing/empty `appKey` in the body | `{"error": "appKey is required"}` |
| `401` | Neither a valid `X-User-Token` header nor a Clerk session was presented | `{"error": "Authentication required"}` |
| `404` | Unknown `appKey`, no profile for this identity, or already erased (replay) | `{"erased": false, "error": "No profile found for this identity"}` |
| `429` | Rate limited (per-IP or per-token) | see below |

Rate limiting is two-layered. First the shared **per-IP** public-submit budget (10 requests / 60 s): `429 {"error": "Too many submissions. Please try again shortly."}` — note this check runs **before** authentication, so even unauthenticated probes consume the IP budget. Then a **per-token** erasure budget for anonymous callers (the same limiter keyed by the token): `429 {"error": "Too many erasure requests for this token. Please try again shortly."}`.

Client guidance for SDKs (expose an "erase my data" entry point, e.g. a settings action):

1. Confirm with the user before calling — erasure is immediate and **irreversible** for that profile.
2. On `200`, discard the stored `X-User-Token` locally and mint a fresh token for any further activity; the old one is dead.
3. Treat `404` as "nothing left to erase" (the expected replay/no-profile response) — never retry it. Retry `429` with exponential backoff; treat `400`/`401` as deterministic client errors.

---

## Feedback Metadata Redaction (PRIV-01)

The free-form `metadata` field on feedback submissions (`POST /api/v1/feedback` and every other feedback write path) is **sanitized server-side before persistence**. Sanitization never throws and never rejects the submission — oversized or suspicious payloads are shrunk — so the contract is non-breaking for existing SDKs. Pre-sanitizing client-side with the same rules is encouraged so users get local feedback:

1. **Key allowlist**: only keys matching `[A-Za-z0-9_.:-]{1,64}` are kept, at most **24** keys; non-conforming or surplus keys are silently dropped. Nested objects and arrays are also capped at 24 entries per level.
2. **Credential redaction**: values under credential-looking keys are replaced with the literal string `"[redacted]"` — regardless of the value's type. The match runs on a normalized key (camelCase/PascalCase split, lowercased) against a broad pattern covering `password`, `secret`, `token`, `apiKey`, `accessKey`, `clientSecret`, `credential`, `authorization`, `cookie`, `session`, `bearer`, `privateKey`, `signature`, `jwt`, `otp`, `ssn`, and credit-card-like keys — so `github_token`, `api-key`, `sessionCookie`, and `SECRET` all hit.
3. **String truncation**: string values longer than 512 chars are cut to 512 and suffixed with `…[truncated]`.
4. **Depth cap**: the top-level metadata object is depth 0; object/array values at depth 4 or deeper collapse to `null`.
5. **8 KB budget**: the total serialized object is capped at 8192 bytes. Whole keys are dropped — never sliced mid-value — in deterministic **sorted-key order**: a key that does not fit is skipped, and a smaller later key can still be kept.

Sanitization is deterministic (same input always yields the same output) and runs **before any other validation**, so even submissions that later fail (`402` quota, `403` public feedback disabled, `401` anonymous feedback disabled, `404` unknown app, `422` `scan_rejected`) never persist unsanitized metadata. Console triage reads back only the sanitized result: pre-rendered string entries carrying `redacted` / `truncated` flags.
