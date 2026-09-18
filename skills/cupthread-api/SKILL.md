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
| `/api/v1/public/config/:appKey` | `GET` | Fetches the `PublicAppConfig`: app metadata, store/website links (`websiteUrl`), branding flags (`hideSiteBranding`), enabled platforms, and anonymous-access settings. Private apps and unknown keys both fail closed with `404 {"error": "App not found"}` (SEC-37). |
| `/api/v1/public/workspaces/:workspaceSlug/apps/:appSlug/config` | `GET` | Same `PublicAppConfig` resolved by workspace and app slugs instead of app key; the same fail-closed `404` applies to private apps. |
| `/api/v1/public/columns/:appKey` | `GET` | Roadmap Kanban columns sorted by position. |
| `/api/v1/public/versions/:appKey` | `GET` | Release versions sorted by position. |
| `/api/v1/public/apps/:appKey/changelog` | `GET` | Published release notes and changelog items. |
| `/api/v1/public/apps/:appKey/changelog/subscribe` | `POST` | Subscribe an email to changelog updates (double opt-in). Always `201 {"subscribed": true}` — the former `alreadySubscribed` field was removed, so the response never reveals prior state. The address starts *pending* and a single-use confirmation email is sent (resends within a 15-minute cooldown return `201` without dispatching a duplicate email). Rate limited per client IP (`429`). |
| `/api/v1/public/apps/:appKey/changelog/confirm` | `GET` | **The mutating double-opt-in confirmation:** consumes the single-use emailed token (`?token=...`) and flips the subscription to *confirmed*. Browsers get an HTML confirmation page; JSON clients (`Accept: application/json` without `text/html`) get `{"confirmed": true}`. Missing token → `400 {"error": "Missing confirmation token"}`; unknown/expired/already-used tokens → `400 {"error": "Invalid or expired confirmation token"}` (uniform, no oracle). Upstream SaaS#250 (SEC-35) plans to move mutation to a POST interstitial, but that change is **not shipped yet** — today GET itself confirms. |
| `/api/v1/public/apps/:appKey/changelog/unsubscribe` | `GET` | **Non-destructive** confirmation interstitial (PROD-20): renders an HTML form that POSTs the token; the subscription is never modified. Email security gateways and link prefetchers that GET this URL cause no side effect. JSON-only clients (`Accept: application/json`) get `405` with `Allow: POST`. Missing/invalid tokens fail uniformly with `400`. Rate limited per client IP (`429`). |
| `/api/v1/public/apps/:appKey/changelog/unsubscribe` | `POST` | The only destructive path (also serves RFC 8058 `List-Unsubscribe=One-Click`): token via query string, JSON body `{"token": "..."}`, or form field. The bare-email unsubscribe (`{"email": "..."}`) was **removed**. Always `{"unsubscribed": true}` whether or not the subscription existed; `400 {"error": "An unsubscribe token is required"}` without a token; `400 {"error": "Invalid or expired unsubscribe token"}` for bad ones. Browser form submissions (`Accept: text/html`) get an HTML landing page. Rate limited per client IP (`429`). |
| `/api/v1/public/apps/:appKey/user` | `PUT` | Update host app user attributes (paying, MRR, currency). Rate limited per client IP: 60 requests/minute, `429` on bursts — retry with exponential backoff when syncing many users behind one shared IP. |
| `/api/v1/feature-requests` | `GET` | List/search feature requests (`limit`, `offset`, `versionId`, `q`). |
| `/api/v1/feature-requests` | `POST` | Submit a new feature request. |
| `/api/v1/feature-requests/:id/vote` | `POST` | Upvote / remove vote on a feature request. |
| `/api/v1/feature-requests/:id/comments` | `GET` | List comments and @replies on a feature request. |
| `/api/v1/feature-requests/:id/comments` | `POST` | Post a comment or @reply on a feature request. |
| `/api/v1/users/:userId/profile` | `GET` | Public user profile, apps, and recent comments. `userId` may be an app-scoped pseudonym (`u_*`); pass `?appKey=` to resolve those. |
| `/api/v1/feedback` | `POST` | Submit feedback draft with optional attachments. |
| `/api/v1/uploads/images` | `POST` | Multipart upload for images to Cloudflare Images. |
| `/api/v1/uploads/r2` | `POST` | Multipart upload for logs / non-image attachments to Cloudflare R2. |

> **Changelog double opt-in flow (SEC-14, as shipped):** `POST /changelog/subscribe` stores the address as *pending* and emails a single-use confirmation link. That link is a `GET /changelog/confirm?token=...` URL, and on the current API **GET itself performs the confirmation** — it consumes the token and flips the subscription to *confirmed* (browsers see an HTML page; JSON clients get `{"confirmed": true}`). Because a plain GET mutates state, email-scanner URL detonation (SafeLinks/Proofpoint) can consume confirmation tokens: treat confirmation links as single-shot and never pre-fetch them to "validate". Unsubscribe is the opposite pattern: `GET .../unsubscribe?token=` is a safe interstitial, and only `POST` with the token unsubscribes. Responses on both flows are uniform (no membership oracle), and these public writes are rate limited per client IP — retry `429`s with exponential backoff.

> **Private-app config fail-closed (SEC-37):** both config routes return `404 {"error": "App not found"}` for private apps (`allowPublic = false`) — identical to the unknown-key response, so callers cannot distinguish the two and must treat 404 as "not found or not public". Do not expect a `200` body with `allowPublic: false`; only public apps get a `200 PublicAppConfig` (with `allowPublic: true`, schema unchanged). Other public data endpoints (columns, versions, feature requests, changelog, feedback, uploads) already reject private apps with `403`.

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

Public write endpoints are budgeted **per client IP** (keyed on `CF-Connecting-IP`). Throttled requests get `429 {"error": "Too many requests. Please try again shortly."}`:

| Endpoints | Budget | Why |
|---|---|---|
| `POST .../changelog/subscribe`, `GET`/`POST .../changelog/unsubscribe` | 10 requests / 60 s | Subscribe emails third parties and unsubscribe deletes subscriber rows, so the budget is tight. |
| `PUT /api/v1/public/apps/{appKey}/user` | 60 requests / 60 s | Every never-seen `userToken` mints an end-user row; rotating-token bursts are the throttled case. |

Retry guidance: treat `429` as transient — wait and retry with exponential backoff and jitter. SDKs syncing attributes for many users behind one shared IP (office NAT, CI farm) are the typical source of `429`s; batch or spread those syncs.

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
