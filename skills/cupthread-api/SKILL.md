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
- **Developer / Console Access**: Developer API token / Bearer token (`cpt_...`) or Clerk session header (`/api/v1/console/*`).
- **End-User / Public SDK Access**: Identified by `appKey` in path/query/body, optional `X-User-Token` header (UUID v4) for anonymous user voting and comment tracking (`/api/v1/public/*`, `/api/v1/feedback`, `/api/v1/feature-requests`).

---

## Key Public & SDK Endpoints

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/public/config/:appKey` | `GET` | Fetches the `PublicAppConfig`: app metadata, store/website links (`websiteUrl`), branding flags (`hideSiteBranding`), enabled platforms, and anonymous-access settings. |
| `/api/v1/public/workspaces/:workspaceSlug/apps/:appSlug/config` | `GET` | Same `PublicAppConfig` resolved by workspace and app slugs instead of app key. |
| `/api/v1/public/columns/:appKey` | `GET` | Roadmap Kanban columns sorted by position. |
| `/api/v1/public/versions/:appKey` | `GET` | Release versions sorted by position. |
| `/api/v1/public/apps/:appKey/changelog` | `GET` | Published release notes and changelog items. |
| `/api/v1/public/apps/:appKey/changelog/subscribe` | `POST` | Subscribe email to changelog updates (double opt-in; sends a confirmation email). |
| `/api/v1/public/apps/:appKey/changelog/confirm` | `GET` | **Non-destructive.** Renders the HTML "Confirm subscription" interstitial whose form POSTs the token; JSON-only clients (`Accept: application/json`) get `405` with `Allow: POST`. Never confirms anything — safe for email scanners and link prefetchers. Missing/invalid/expired tokens fail uniformly with `400`. |
| `/api/v1/public/apps/:appKey/changelog/confirm` | `POST` | The only mutating confirmation path: consumes the single-use emailed token (via `token` query parameter, JSON body `{"token": "..."}`, or form field) and confirms the subscription. Returns `{"confirmed": true}`, or an HTML landing page for browser form submissions (`Accept: text/html`). Invalid/expired/already-used tokens fail uniformly with `400` (no oracle). |
| `/api/v1/public/apps/:appKey/changelog/unsubscribe` | `GET` | **Non-destructive.** Renders the HTML "Confirm unsubscribe" interstitial whose form POSTs the token to the same URL; JSON-only clients (`Accept: application/json` without `text/html`) get `405` with `Allow: POST` and `{"error": "GET does not unsubscribe. POST the token to this endpoint to unsubscribe."}`. Never unsubscribes anything — safe for email scanners and link prefetchers (the URL appears in blast email footers and `List-Unsubscribe` headers). Missing/invalid/expired tokens fail uniformly with `400`. |
| `/api/v1/public/apps/:appKey/changelog/unsubscribe` | `POST` | The only destructive unsubscribe path (RFC 8058 one-click lands here): accepts the signed per-subscriber token via `token` query parameter, JSON body `{"token": "..."}`, or form field `token` (`application/x-www-form-urlencoded` / `multipart/form-data`). Returns `{"unsubscribed": true}`, or an HTML landing page for browser form submissions (`Accept: text/html`). Invalid/missing tokens fail uniformly with `400`, and the response is uniform regardless of whether the subscription still existed (no oracle). Bare-email unsubscribe remains unavailable (SEC-14). |
| `/api/v1/public/apps/:appKey/user` | `PUT` | Update host app user attributes (paying, MRR, currency). |
| `/api/v1/feature-requests` | `GET` | List/search feature requests (`limit`, `offset`, `versionId`, `q`). |
| `/api/v1/feature-requests` | `POST` | Submit a new feature request. |
| `/api/v1/feature-requests/:id/vote` | `POST` | Upvote / remove vote on a feature request. |
| `/api/v1/feature-requests/:id/comments` | `GET` | List comments and @replies on a feature request. |
| `/api/v1/feature-requests/:id/comments` | `POST` | Post a comment or @reply on a feature request. |
| `/api/v1/users/:userId/profile` | `GET` | Public user profile, apps, and recent comments. `userId` may be an app-scoped pseudonym (`u_*`); pass `?appKey=` to resolve those. |
| `/api/v1/feedback` | `POST` | Submit feedback draft with optional attachments. |
| `/api/v1/uploads/images` | `POST` | Multipart upload for feedback images. PNG/JPEG/WebP/GIF only — SVG and declared-vs-content mismatches fail with `415` (see the media-type policy below). |
| `/api/v1/uploads/r2` | `POST` | Removed tombstone: always responds `410 Gone` (create an upload session at `/api/v1/uploads/sessions` instead). |

> **Changelog double opt-in flow (SEC-14/SEC-35):** `POST /changelog/subscribe` stores the address as *pending* and emails a single-use confirmation link. That link is a `GET /changelog/confirm?token=...` URL, which is **non-destructive** — email security scanners (SafeLinks/Proofpoint URL detonation) and link prefetchers that fetch it cause no side effect. Actual confirmation happens only when the interstitial form (or any client) **POSTs** the token to `/changelog/confirm`.
>
> The same GET-interstitial/POST-mutates pattern applies to **unsubscribe** (PROD-20): `GET /changelog/unsubscribe?token=...` only renders the confirmation page (or `405` + `Allow: POST` for JSON-only clients) and performs no side effect; the destructive step is **POSTing** the token to `/changelog/unsubscribe`. When building custom clients, never rely on GET to perform the confirmation or the unsubscribe.

---

## Submission Quota Errors (`402 Payment Required`)

`POST /api/v1/feature-requests` (and, with the same contract, `POST /api/v1/feedback`) responds with `402 Payment Required` and an `ErrorResponse` body (`{"error": string, "code"?: string}`) when the app's workspace cannot accept new submissions:

| `code` | Meaning | Actionable guidance |
|---|---|---|
| `tier_limit_submissions` | The workspace reached its monthly submission quota. | Do not retry automatically. Tell the user to upgrade the workspace plan (Console → Billing) or wait for the quota to reset, then resubmit. |
| `subscription_inactive` | The workspace subscription is inactive or canceled. | Do not retry automatically. Tell the user to renew/reactivate the subscription (Console → Billing); submissions keep failing until then. |

Agents and SDK clients should parse the `code` field, treat `402` as a deterministic business rule (never a transient error), and surface the guidance above to the end user.

---

## User Identifiers on Public Endpoints (PRIV-06)

Public board and comment payloads identify users with **deterministic app-scoped pseudonyms**, never raw identity-provider ids:

- `GET /api/v1/feature-requests`: `requesterClerkId`, `recentCommenters[].clerkUserId`
- `GET` / `POST /api/v1/feature-requests/:id/comments`: `authorClerkId`, `replyToClerkId`

Contract:

- Values are `u_` followed by 32 lowercase hex chars (e.g. `u_9f2c…`). Field names are unchanged — only the value format changed. Legacy `user_*` Clerk ids may still appear in old cached payloads, so **never validate or assume a `user_` prefix** on user id strings.
- Ids are stable for a given (app, user) pair, so reply threading (`replyToClerkId`) and author attribution keep working **within one app**. They are **unlinkable across apps**: never join, deduplicate, or correlate user ids between two different `appKey`s.
- `GET /api/v1/users/:userId/profile` accepts `u_*` ids **only together with the `appKey` query parameter** (the id can only be reversed within its app). Without `appKey`, a `u_*` request returns `404`. Legacy `user_*` ids remain accepted without `appKey` for existing `/u/` links.
- Public profiles are opt-in. A user who never created a public profile resolves to a placeholder — the requested id echoed back with `displayName: null` and empty `publicApps` / `recentComments` — and there is no existence oracle for raw ids. `publicApps` lists only apps of workspaces where the user is an **owner**; the response has no top-level `hideComments` (comment visibility is applied server-side).

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
