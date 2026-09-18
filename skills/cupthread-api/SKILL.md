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
| `/api/v1/public/apps/:appKey/changelog/subscribe` | `POST` | Subscribe email to changelog updates. |
| `/api/v1/public/apps/:appKey/changelog/unsubscribe` | `POST` | Unsubscribe email from changelog. |
| `/api/v1/public/apps/:appKey/user` | `PUT` | Update host app user attributes (paying, MRR, currency). |
| `/api/v1/feature-requests` | `GET` | List/search feature requests (`limit`, `offset`, `versionId`, `q`). |
| `/api/v1/feature-requests` | `POST` | Submit a new feature request. |
| `/api/v1/feature-requests/:id/vote` | `POST` | Upvote / remove vote on a feature request. |
| `/api/v1/feature-requests/:id/comments` | `GET` | List comments and @replies on a feature request. |
| `/api/v1/feature-requests/:id/comments` | `POST` | Post a comment or @reply on a feature request. |
| `/api/v1/users/:userId/profile` | `GET` | Public user profile, apps, and recent comments. `userId` may be an app-scoped pseudonym (`u_*`); pass `?appKey=` to resolve those. |
| `/api/v1/feedback` | `POST` | Submit feedback draft with optional attachments. |
| `/api/v1/uploads/images` | `POST` | Multipart upload for images to Cloudflare Images. |
| `/api/v1/uploads/r2` | `POST` | Multipart upload for logs / non-image attachments to Cloudflare R2. |

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
