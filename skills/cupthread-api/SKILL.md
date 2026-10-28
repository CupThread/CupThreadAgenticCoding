---
name: cupthread-api
description: Reference for CupThread backend API endpoints, authentication flows, OpenAPI 3.1 schema, and interactive API documentation.
---

# CupThread API Reference & OpenAPI Specification

Compact index for CupThread's backend API. This file intentionally stays small — it maps the surface and the cross-cutting rules. Two places carry the detail:

1. The **live OpenAPI 3.1 spec** — the always-current, authoritative contract for every endpoint (request/response schemas, status codes, error bodies).
2. The **`reference/*.md` files in this skill directory** — the shipped behavioral detail per topic (gate orderings, error-code tables, pagination semantics). Read the file listed for a topic when you need that level of detail.

Never inline per-endpoint contract detail back into this file (see the maintainer note at the bottom).

## 🌐 Official API Documentation & OpenAPI Schema

- **OpenAPI 3.1 Schema (JSON)**: [https://api.cupthread.com/api/v1/openapi.json](https://api.cupthread.com/api/v1/openapi.json)
- **OpenAPI 3.1 Schema (YAML)**: [https://api.cupthread.com/api/v1/openapi.yaml](https://api.cupthread.com/api/v1/openapi.yaml)
- **Interactive API Documentation (Web)**: [https://cupthread.com/api](https://cupthread.com/api) (or [https://api.cupthread.com/reference](https://api.cupthread.com/reference))
- **Official Platform Website**: [https://cupthread.com](https://cupthread.com)

## 🤖 Quick Prompt for Coding Agents

```text
Please read the CupThread OpenAPI 3.1 specification at https://api.cupthread.com/api/v1/openapi.json and implement a type-safe client for submitting user feedback, querying public feature requests, and syncing user attributes.
```

## Base URL & Environment

- **Production API Base**: `https://api.cupthread.com`
- **Interactive Reference UI**: `https://cupthread.com/api`

## Roles & Authentication

- **Developer / Console Access**: developer API token / Bearer token (`cpt_...`) or Clerk session header on `/api/v1/console/*`. Console workspace routes enforce role-based capabilities (AUTH-01) and the interactive-only set (`changelog.publish`, `members.manage`, `billing.manage`, `integration.manage`, `privacy.manage`, `workspace.delete`) rejects `cpt_` tokens outright with `403 interactive_session_required`. Check order: role first (`403 capability_required`), then token type.
- **End-User / Public SDK Access**: identified by `appKey`, plus the optional `X-User-Token: <uuid>` header for anonymous voting, comments, roadmap personalization, and upload sessions (`/api/v1/public/*`, `/api/v1/feedback`, `/api/v1/feature-requests`, `/api/v1/uploads/*`). The header replaces the deprecated `?userToken=` query parameter, and the feature-request **vote and propose writes** accept the end-user token in either the JSON body field (`userToken`/`requesterToken`) or the `X-User-Token` header, with the body field winning when both are present. On `POST /api/v1/uploads/sessions` the header is **required** for anonymous callers (SEC-28). `POST /api/v1/me/link` confirms identity linking (needs Clerk session too); `POST /api/v1/me/erase` is self-service data erasure.
- **CLI / OAuth Access**: `cupthread auth login` runs Authorization Code + PKCE in a browser (or the RFC 8628 device flow headless) and stores a rotating 14-day `cpt_` pair (refresh tokens live 180 days, rotate on every use). RFC 8414 metadata at `GET /.well-known/oauth-authorization-server`; full surface under OpenAPI tag `OAuth`. Redirect URIs allow only absolute `https:` or loopback `http:` (SEC-46).

## Reference Files (in this skill)

| File | Topic |
|---|---|
| [`reference/endpoints.md`](reference/endpoints.md) | Per-endpoint contract tables for every public & SDK route |
| [`reference/oauth.md`](reference/oauth.md) | OAuth authorization server (QUAL-04) and the redirect-URI scheme policy (SEC-46) |
| [`reference/email-subscriptions.md`](reference/email-subscriptions.md) | Changelog subscribe/confirm/unsubscribe (#635, SEC-35) and weekly-digest unsubscribe (PRIV-07) |
| [`reference/rate-limits-and-cache.md`](reference/rate-limits-and-cache.md) | 429 budgets, shared cache, body limits (SEC-36), anonymous-access gates (PRIV-12) |
| [`reference/uploads-and-attachments.md`](reference/uploads-and-attachments.md) | Upload sessions, content scan (SEC-25), uploader binding (SEC-28), image media-type policy |
| [`reference/user-identity-and-privacy.md`](reference/user-identity-and-privacy.md) | `X-User-Token` & identity linking (SEC-12), pseudonyms (PRIV-06), avatars (PRIV-19), erasure & redaction (PRIV-01) |
| [`reference/voting-and-submissions.md`](reference/voting-and-submissions.md) | Votes & ship-notification opt-in, Turnstile gates (SEC-846, #242), 402 quotas, tiers |
| [`reference/console-and-rbac.md`](reference/console-and-rbac.md) | Console moderation, capability RBAC (AUTH-01), changelog publishing (SEC-40), `X-Request-Id` (OPS-01) |
| [`reference/payment-attribute-signing.md`](reference/payment-attribute-signing.md) | The DATA-03/DATA-07 HMAC canonical string and full signing contract |

## Key Public & SDK Endpoints

One line each; schemas and error contracts live in the OpenAPI spec.

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/public/config/:appKey` (and the workspace/app-slug variant) | `GET` | `PublicAppConfig`: branding, platforms, anonymous-access flags, `requireAnonymousVoteTurnstile`, optional `allowedEmbedOrigins`, and the always-served `sdk` object (`theme`, feature toggles, changelog-overlay copy — SaaS #1235). Private apps and unknown keys both fail closed with the same `404` (SEC-37). |
| `/api/v1/public/showcase` (alias `/api/v1/public/apps`) | `GET` | Public app showcase, newest first; `offset` clamps at 5000. |
| `/api/v1/public/columns/:appKey`, `/api/v1/public/versions/:appKey` | `GET` | Roadmap columns and released versions; `401 authentication_required` on sign-in-only roadmaps (PRIV-12). |
| `/api/v1/public/apps/:appKey/changelog` | `GET` | Published changelog feed, cursor-paginated; corrected entry schema in [reference/endpoints.md](reference/endpoints.md) (SaaS #1226). |
| `/api/v1/public/apps/:appKey/changelog/subscribe` | `POST` | Email double-opt-in subscribe; supports optional `turnstileToken`. |
| `/api/v1/public/apps/:appKey/changelog/confirm` | `GET`/`POST` | SEC-35 interstitial: `GET` never mutates; only `POST` consumes the single-use token. |
| `/api/v1/public/apps/:appKey/changelog/unsubscribe` | `GET`/`POST` | Same interstitial pattern; `POST` serves RFC 8058 one-click. Unsubscribe is the exception to token expiry and the private-app `403` (#635). |
| `/api/v1/public/digest/unsubscribe` | `GET`/`POST` | Weekly-digest one-click unsubscribe (PRIV-07); `GET` non-destructive, only `POST` mutates. |
| `/api/v1/feature-requests` | `GET` | List/search the board; keyset cursor pagination (DATA-01), `offset` clamps at 10000. |
| `/api/v1/feature-requests` | `POST` | Propose a request; every public submission starts unapproved (`201 {featureRequestId, pending: true}`). |
| `/api/v1/feature-requests/:id` | `GET` | Deep-link read of one approved request; unapproved/missing/private all answer the same uniform `404`. |
| `/api/v1/feature-requests/:id/vote` | `POST`/`DELETE` | Insert-only idempotent vote; `DELETE` (same JSON body as the POST) is the only un-vote. Optional `shipNotifyEmail` + `turnstileToken`. |
| `/api/v1/feature-requests/:id/comments` | `GET` | Keyset-paginated thread; unapproved requests `404` (existence hiding). |
| `/api/v1/feature-requests/:id/comments` | `POST` | Clerk-session comment/@reply; `parentId` must reference a visible comment on the same request (SEC-50). |
| `/api/v1/me/link` | `POST` | Bind an anonymous `X-User-Token` profile to the signed-in Clerk identity (SEC-12); `409 already_linked` is never hijacked. |
| `/api/v1/me/erase` | `POST` | Self-service erasure (PRIV-01): rotates the token immediately, anonymizes attribution, keeps aggregates. |
| `/api/v1/users/:userId/profile` | `GET` | Public profile `{profile, publicApps, recentComments}`; resolves app-scoped `u_*` pseudonyms only, and those require `?appKey=` (PRIV-06) — any other id, incl. raw Clerk `user_*`, answers the anonymous placeholder (SaaS #1322). |
| `/api/v1/public/apps/:appKey/user` | `PUT` | Sync end-user attributes; payment attributes (`isPaying`, `mrr`, `plan`, `currency`) require HMAC `signature` + `timestamp` (DATA-03). |
| `/api/v1/uploads/sessions` | `POST` | Create an upload session (1–8 reserved slots); anonymous callers must send `X-User-Token` (SEC-28). |
| `/api/v1/uploads/:uploadId` | `PUT`/`POST` | Fill one reserved slot with raw bytes or multipart; `Authorization: Bearer <sessionToken>`. |
| `/api/v1/uploads/images` | `POST` | Session-metered feedback image upload; PNG/JPEG/WebP/GIF only, stored as a private attachment (PRIV-02). |
| `/api/v1/feedback` | `POST` | Submit feedback referencing finalized `uploadId`s (≤ 8); `422 scan_rejected` fails the whole submission (SEC-25). |
| `/api/v1/feedback/attachments/:id/download` (alias `GET …/attachments/:id`) | `GET` | Private attachment download via signed `?token=` or workspace-member auth. |
| `/api/v1/files/:key` | `GET` | Public serving of console image assets only; optional fixed `width` thumbnail set. Private feedback attachments answer `403 private_attachment_forbidden`. |
| `/api/v1/uploads/r2` | `POST` | Removed tombstone — always `410 Gone`. |
| `/api/v1/console/workspaces/:wsId/...` | various | Console surface (feature requests, submissions, columns, versions, changelog, moderation, members, billing, integrations, exports). Moderation: list/hide/delete comments. |

## Cross-Cutting Concerns

### Rate limiting (`429 Too Many Requests`)

All public endpoints are budgeted **per client IP** (`CF-Connecting-IP`). Throttled bodies are generic `429 {"error": "Too many requests. Please try again shortly."}` except: votes (`Too many votes…`), searches (`Too many searches…`), and OAuth grant/consent endpoints (RFC 6749 `invalid_request` at `429`).

| Bucket | Budget | Shares |
|---|---|---|
| `PUBLIC_WRITE_RATE_LIMITER` | 10 / 60 s | Changelog subscribe/confirm/unsubscribe GETs, digest-unsubscribe GET, most public writes |
| `DIGEST_UNSUBSCRIBE_RATE_LIMITER` (PRIV-08) | 300 / 60 s | One-click unsubscribe POSTs **with a query token only** (RFC 8058 mailbox providers); budget chosen before the body is read (SEC-27) |
| `PUBLIC_USER_RATE_LIMITER` | 60 / 60 s | `PUT …/user` attribute sync + `GET /users/:userId/profile` |
| Vote limiter (SEC-09) | 20 / 60 s | Vote `POST`/`DELETE` |
| `PUBLIC_READ_RATE_LIMITER` | 60 / 60 s | Showcase, board detail/comments, changelog feed, columns, versions |
| `BOARD_LIST_RATE_LIMITER` / `SEARCH_RATE_LIMITER` | 60 / 60 s / 30 / 60 s | Board listing (no `q`) / board search |
| `PUBLIC_CONFIG_RATE_LIMITER` | 60 / 60 s | Both config GETs (spend is the first statement) |
| `OAUTH_TOKEN_RATE_LIMITER` (SEC-61) | 60 / 60 s | Shared per-IP authorize bucket (key omits `client_id`, so id rotation buys nothing) |

Retry guidance: treat `429` as transient — exponential backoff with jitter, never a tight loop. Batch attribute syncs behind shared IPs; surface a friendly message for vote `429`s; poll no faster than the 30-second cache TTL.

### Request body limits (SEC-36)

| Surface | Limit |
|---|---|
| Webhooks and public token endpoints | 1 MB |
| Public JSON intake (feature requests, votes, comments, `PUT …/user`, `/feedback`, uploads, changelog/digest unsubscribe) | 256 KB |
| Console JSON | 1 MB |
| Console image multipart (app icon) | 11 MB |

Over-limit → `413 {"error": "Payload exceeds size limit", "code": "payload_too_large"}`; malformed `Content-Length` → `400 invalid_content_length`. Most intake routes check the declared body size **before** spending a rate-limit slot; the exceptions (changelog POSTs, digest-unsubscribe POSTs, OAuth consent POSTs) spend the limiter first (SEC-27).

### Public board reads: shared cache + gates

Five public GETs (board detail, comment threads, changelog feed, columns, versions) may serve anonymous `200`s from a **30-second shared cache** (`Cache-Control: public, max-age=30`). Token- or session-bearing callers skip the cache in both directions — a personalized body is never served stale or cross-user. Gates run before the cache read: sign-in-only boards still answer `401`, private apps `403`, hidden ids `404`. A just-posted comment or just-published entry can take up to 30 s to appear on anonymous reads.

### Per-app anonymous-access enforcement (PRIV-12)

`PublicAppConfig` flags (`allowAnonymousRoadmap`, `allowAnonymousChangelog`, `allowAnonymousVote`, `allowAnonymousFeedback`) are enforced server-side on **every** surface of the family. A disabled flag turns the family's reads/writes into `401 {"error": …, "code": "authentication_required"}` for anonymous callers — surface the host app's sign-in flow, do not retry. The vote gates run **ahead of** the existence lookup, so missing and approved ids are indistinguishable. `404` on a detail read or comment thread means "not available" (missing, private, or not yet approved) — never tell the user the request never existed (existence hiding).

### User identifiers (PRIV-06)

Public payloads identify users with app-scoped pseudonyms: `u_` + 32 hex chars. Stable within one app, unlinkable across apps — never join or correlate ids between `appKey`s. `GET /api/v1/users/:userId/profile` resolves `u_*` ids only with `?appKey=`; unknown ids answer `404` with no reverse-lookup oracle (SEC-34). Never assume a legacy `user_` prefix on user ids: since SaaS #1322 the profile endpoint is pseudonym-only — any raw Clerk `user_*` id answers the anonymous placeholder (`displayName: null`, empty `publicApps`/`recentComments`) even for opted-in users.

### Payment-attribute signing (DATA-03 / DATA-07)

`PUT /api/v1/public/apps/:appKey/user` requires HMAC-SHA256 `signature` (64-hex) + `timestamp` (epoch seconds) whenever the body carries any of `isPaying`, `mrr`, `plan`, `currency` (explicit `null` counts). The key is the app's SDK signing secret; timestamp window ±300 s (`401 stale_signature`), mismatch → `401 invalid_signature`, missing signature/secret → `422`. Sign the exact newline-joined canonical string documented in the OpenAPI spec (`cpt-user-attrs-v1` prefix; absent fields sign as `unset`, JSON null as `null`; the literal plan strings `"unset"`/`"null"` are reserved and rejected). The HMAC is verified over the `userToken` **exactly as presented** — sign and send the same spelling (mixed-case UUIDs included); the server folds it to lowercase only for identity storage, so signing one spelling and sending another fails `401 invalid_signature`. A signed update that omits `currency` preserves the stored value (DATA-07). Cross-check implementations with `cupthread api sign-user-attrs` (see the `cupthread-cli` skill).

### Pagination conventions

List endpoints are keyset-cursor-paginated: read `hasMore`/`nextCursor`, pass `nextCursor` back as `?cursor=` until `null`. Treat cursors as opaque — never parse, construct, or persist beyond forwarding; malformed cursors → `400 {"error": "Invalid cursor"}`. A supplied `cursor` ignores `offset`; legacy `offset` clamps (board 10000, showcase 5000) still return `200`. `total` is computed per current filters, independent of the cursor.

### Upload sessions lifecycle

Always: **create session → fill each reserved slot → submit feedback referencing the `uploadId`s**. There is no un-sessioned upload path. Sessions bind to the creator's identity (SEC-28): anonymous callers must present the **same `X-User-Token`** on session create and feedback submit, or the submission fails `400 uploader_mismatch`. Slots are single-shot (`409 already_uploaded`); content scan runs on stored bytes (`415` on rejection); a scan-rejected referenced attachment fails the whole submission with `422 scan_rejected` (SEC-25); a lost concurrent submit gets `409 already_finalized` and must not be retried. Direct attachment URLs/keys are forbidden (`400 direct_attachment_forbidden`).

### Feedback intake details

- Submission quota (`402 Payment Required`): parse `code` — `tier_limit_submissions` (upgrade or wait) and `subscription_inactive` (renew). Deterministic business rules, never transient errors; do not auto-retry.
- Free-form `metadata` is sanitized server-side before persistence, never rejected (PRIV-01): key allowlist (24 keys), credential redaction (`"[redacted]"`), 512-char string truncation, depth cap 4, 8 KB budget. Pre-sanitize client-side for immediate feedback.
- Feedback images: `POST /api/v1/uploads/images` accepts PNG/JPEG/WebP/GIF only (SVG and declared-vs-content mismatches → `415`), stores a **private attachment**, and answers a stable provider-agnostic `500 image_upload_failed` on storage failure (SaaS PR #793). Console app-icon uploads accept screened SVG instead.
- The 402 tier enum is `free` | `pro` only (BILL-553) — never model `business`. All import sources require Pro.

### Request correlation IDs (OPS-01)

Every response carries `X-Request-Id` (caller-supplied if it matches `^[A-Za-z0-9._-]{8,64}$`, else server-generated). Quote it verbatim in bug reports. The CLI sends `cli-<uuid>` and echoes `request-id=…` on errors.

### Error-handling conventions (all endpoints)

- Parse the machine-readable `code` field of `{"error": string, "code"?: string}` bodies — never the message text.
- Deterministic client errors (`402`, `409`, `422`, `413`, validation `400`s): fix the request or surface to the user; never auto-retry. Transient (`429`, `500 image_upload_failed`): retry with exponential backoff.
- Uniform `404`s are deliberate existence hiding (private apps, unapproved requests, unknown ids) — no client may infer existence from a `404` vs `403` distinction.

## Maintainer note — size budget

Agent-skill hosts refuse to load a `SKILL.md` at or above **100 KB**, rejecting the whole skill (issue #318). This file must stay under the soft budget of **80 KB** — enforced by `TestSkillMDSizeBudgets` in `skills/skill_size_test.go` on every CI run. The other five skills live at 21–43 KB; aim for the same ballpark. **Per-endpoint contract detail belongs in `reference/*.md` next to this file or in the live OpenAPI spec (SaaS repo), not here** — appending contract tables to `SKILL.md` is what pushed it over the limit. Keep this file an index: surface map, cross-cutting invariants, pointers to the reference files and the authoritative references. Content pinned by the docs-sync tests in `internal/cmd/skill_docs_test.go` may live in either `SKILL.md` or a `reference/*.md` file — the tests read both.
