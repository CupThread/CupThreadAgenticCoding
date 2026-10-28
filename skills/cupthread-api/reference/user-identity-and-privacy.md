# End-User Identity & Privacy

Reference detail for the `cupthread-api` skill, split out of `SKILL.md` so the
skill stays under the agent-skill size budget (issue #318). The live OpenAPI 3.1
spec (<https://api.cupthread.com/api/v1/openapi.json>) remains the authoritative
contract; these files mirror the shipped behavior behind it.

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

### Vote and propose writes accept the header too (SaaS #834)

`POST /api/v1/feature-requests/:id/vote`, `DELETE /api/v1/feature-requests/:id/vote`, and `POST /api/v1/feature-requests` read the end-user token from **either** the JSON body field (`userToken` on the votes, `requesterToken` on the propose) **or** the `X-User-Token` header — the same transport the read paths use:

- The **body field wins** when both are present: only an *absent* body field is replaced with the header value (surrounding whitespace trimmed).
- A body field that is **present but invalid** keeps failing schema validation exactly as before (`400 Validation failed` with the Zod `flatten()` shape) — the header never overrides or repairs it.
- A payload with **neither** source answers the same `400` shape with `details.fieldErrors.<field>: ["Required — send the token in the JSON body or the X-User-Token header"]` (`userToken` on the votes, `requesterToken` on the propose).
- Unlike the read paths — where a malformed header token silently degrades to an anonymous request — a header token merged into a write is validated by the same body schema, so a non-UUID header value is a hard `400`, not a silent fallthrough.
- The vote `DELETE` still requires a JSON body carrying `appKey`: the header can carry the token but not the app key, and the published spec previously documented no body on that operation, so a spec-following client that sent none got the `400` above.

No existing valid request stops working: every body-token payload accepted before is accepted byte-for-byte on the old validation path, and clients that can only attach the header (a `DELETE` with no JSON body support, for example) can now send the token header-only. The corrected OpenAPI components also match the implemented bodies now — `CreateFeatureRequestInput` requires `appKey`/`title`/`description` with optional `requesterName`/`requesterToken`/`turnstileToken` and no `requesterEmail`, `VoteResponse` is `voted` + `voteCount` with an optional `warning` (no `featureRequestId`/`hasVoted`), the vote `DELETE` documents its JSON body, and all three writes document their `400`s — so clients regenerated from `/api/v1/openapi.json` stop sending the dead header and the missing `DELETE` body.

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
- `GET /api/v1/users/:userId/profile` accepts `u_*` ids **only together with the `appKey` query parameter** (the id can only be reversed within its app). Without `appKey`, a `u_*` request returns `404`, and an unknown (unmapped) `u_*` id returns the same `404 {"error": "User profile not found"}` — since SEC-34 the endpoint never runs the full-app candidate scan (unique unknown ids are cache-busting), so an indexed miss is terminal and retrying cannot change the answer. Since SaaS #1322 (issue #261) resolution is **pseudonym-only**: any non-`u_*` identifier — including raw Clerk `user_*` ids from legacy `/u/` links — resolves to the same placeholder as an opted-out user (`200`, `displayName: null`, empty `publicApps` / `recentComments`) even when that user opted in; no request or response schema changed, only which identifiers resolve, so switch any legacy `/u/` flow to the app-scoped `u_*` id carried by board and comment payloads. The endpoint is additionally rate limited per client IP (see [Rate Limiting](rate-limits-and-cache.md#rate-limiting-429-too-many-requests)).
- Public profiles are opt-in. A user who never created a public profile resolves to a placeholder — the requested id echoed back with `displayName: null` and empty `publicApps` / `recentComments` — and there is no existence oracle for raw ids. `publicApps` lists only apps of workspaces where the user is an **owner**; the response has no top-level `hideComments` (comment visibility is applied server-side).
- **Exact `PublicUserProfileResponse` shape (OpenAPI 3.1, SaaS #1190 / PR #1192):** the top-level response is always `{profile, publicApps, recentComments}` — all three keys required (`recentComments` is always emitted across every branch, as an empty array `[]` when comments are hidden or absent; the server never emits `apps` or a top-level `hideComments`). Inner schemas:
  - `profile` (`UserProfile` component): `{clerkUserId, displayName, avatarUrl, bio, websiteUrl, createdAt}` — all six keys required (`displayName`, `avatarUrl`, `bio`, `websiteUrl` nullable; `createdAt` ISO 8601 `date-time`). Neither `hideComments` (the privacy flag gates `recentComments` server-side and is never disclosed) nor `updatedAt` is ever emitted on `profile`.
  - `publicApps` items (`PublicUserProfileApp` component — the workspace-owner member-of shape, **not** the showcase `PublicAppSummary` `{appId, slug, storeUrl, storeKind}` shape): `{id, workspaceSlug, workspaceName, appSlug, name, description, iconUrl}` — all seven keys required (`description`, `iconUrl` nullable).
  - `recentComments` items: `{id, body, createdAt, featureRequestId, featureRequestTitle, workspaceSlug, appSlug, appName}` — all eight keys required (attributed via `workspaceSlug`, `appSlug`, and `appName`; `appId` is never emitted on comment items).
  - Clients generated from `/api/v1/openapi.json` or hand-written strict decoders (`Codable`, `kotlinx.serialization`) must decode `publicApps` (not `apps`) and must not require the removed phantom keys (`apps`, top-level or inner `hideComments`, inner `updatedAt`, or comment `appId`).

---

## Profile Avatar URLs Are Managed-Host-Only (PRIV-19)

A profile avatar renders as `<img src>` on public boards and in the developer console, so every visitor's browser fetches whatever URL the author saved — the same tracking-pixel channel PRIV-11 closed for feature-request descriptions. Avatars are therefore pinned to product-managed image URLs, on both the write and the read paths (source: SaaS PR #397, issue #387).

**Writes are validated** — `PUT /api/v1/console/me/profile` accepts an `avatarUrl` only when it is one of:

- the first-party re-hosting path on the API origin: `<PUBLIC_BASE_URL>/api/v1/files/images…`, including the `%2F`-encoded R2 fallback form; or
- an absolute `https:` URL with **no userinfo** (`user:pass@`) whose hostname is exactly `imagedelivery.net`, or whose href is under the deployment's configured images delivery host (`IMAGES_DELIVERY_URL`).

Everything else — `http:`, `javascript:`, `data:`, relative or protocol-relative URLs, any foreign host, unparseable values — is rejected with `400 {"error": "Validation failed", "code": "avatar_url_not_allowed", "details": …}` (the same validation shape answers `code: "website_url_not_allowed"` for the profile's `websiteUrl`). SEC-60: a profile write is a published-identity change, so it additionally requires an **interactive Clerk session** — a `cpt_` API token is rejected with `403` — while profile reads stay token-safe.

**Reads fail closed.** On every public payload that carries an avatar — `requesterAvatarUrl` on `GET /api/v1/feature-requests`, `authorAvatarUrl`/`recentCommenters[].avatarUrl` on comment payloads, and `profile.avatarUrl` on `GET /api/v1/users/{userId}/profile` — the value is **either `null` or a managed `https:` URL**. Legacy stored values that no longer satisfy the policy are served as `null`, never passed through. Clients must render a placeholder on `null` and must not assume a value's host. Managed URLs on the API origin (`<PUBLIC_BASE_URL>/api/v1/files/…`) may be fetched with an appended `?width=` thumbnail hint — one of the documented sizes only (16, 32, 36, 56, 64, 72, 80, 112, 128, 160), appended to the stored URL rather than reconstructed, and always with a fallback to the original URL since a width-hint response may be the original bytes (see the `/api/v1/files/:key` row above); URLs on other hosts (`imagedelivery.net`, the deployment's images delivery host) must be passed through untouched. Attribution is server-resolved: the `authorName` and `authorAvatarUrl` a client sends on `POST /api/v1/feature-requests/{id}/comments` are ignored — the signed-in user's stored profile display name and avatar (or `null`) are published instead.

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

---
