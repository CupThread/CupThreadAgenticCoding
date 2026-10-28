# Email Subscriptions: Changelog & Weekly Digest

Reference detail for the `cupthread-api` skill, split out of `SKILL.md` so the
skill stays under the agent-skill size budget (issue #318). The live OpenAPI 3.1
spec (<https://api.cupthread.com/api/v1/openapi.json>) remains the authoritative
contract; these files mirror the shipped behavior behind it.

## Changelog Unsubscribe on Private Apps and After Expiry (#635)

`GET` and `POST /api/v1/public/apps/{appKey}/changelog/unsubscribe` (OpenAPI tag `Changelog`, SaaS #635). No request or response field was added or removed. The success JSON is still `{ "unsubscribed": true }`.

A token whose signature matches this app unsubscribes even when public surfaces are disabled for the app, and even after `exp`. POST returns HTTP 200 `{ "unsubscribed": true }`. An HTML landing page is returned only when `Accept` includes `text/html`. A successful POST also revokes that address's ship-notification consent; the JSON body does not change.

GET stays non-destructive. A browser GET renders the confirmation page. `Accept: application/json` without `text/html` still returns `405 {"error": "GET does not unsubscribe. POST the token to this endpoint to unsubscribe."}` with `Allow: POST`. Nothing is removed until POST.

| Situation | Status | Body |
|---|---|---|
| Signature matches this app, including after `exp`, on a public or private app | GET: `200` HTML, or `405` for JSON-only clients. POST: `200` | POST JSON `{ "unsubscribed": true }` (HTML only when `Accept` includes `text/html`) |
| Private app, token missing, malformed, or signed for a different app | `403` | `{ "error": "Public surfaces are disabled for this app" }` — that response does not say whether the app exists |
| Public app, token missing | `400` | GET `{ "error": "Missing unsubscribe token" }`; POST `{ "error": "An unsubscribe token is required" }` |
| Public app, token does not verify | `400` | `{ "error": "Invalid or expired unsubscribe token" }` |

An authentic signature for this app is not that `400`, including after `exp`. Unknown `appKey` values are still `404 {"error": "App not found"}` from the app lookup, before the token is considered.

New links are minted with a 90-day `exp`. Links already in inboxes keep working after that date when the signature still matches.

Changelog confirm (`GET`/`POST .../changelog/confirm`) and ship-notification confirm (`GET`/`POST .../ship-notifications/confirm`) are unchanged: expired or invalid confirmation tokens stay `400 {"error": "Invalid or expired confirmation token"}`, and those routes still return `403 {"error": "Public surfaces are disabled for this app"}` when public surfaces are disabled.

Rate limiting is unchanged and still runs before the POST body is read. One-click POSTs that carry the token in the query string stay on the dedicated unsubscribe budget (300 requests / 60 s). A POST with no query token stays on the shared 10/min public-write bucket.

Clients that treated HTTP 403 or HTTP 400 as the final result for a token copied from a changelog email should treat HTTP 200 as unsubscribed in the two cases above (private app with a matching signature, and an authentic token past `exp`). When public surfaces are disabled, describe `403` on this route as "token missing or not valid for this app". Do not drop a signed link solely because `exp` has passed or because the board is no longer public. No new field needs decoding. The SDK skills do not map this error; follow the OpenAPI descriptions.

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

Both verbs are rate limited per client IP (`429 {"error": "Too many requests. Please try again shortly."}`). The GET stays on the shared 10 requests / 60 s public-write budget; since PRIV-08 a POST carrying the token in the query string (whitespace-trimmed non-empty) draws from the dedicated `DIGEST_UNSUBSCRIBE_RATE_LIMITER` at 300 requests / 60 s — sized for mailbox providers one-click-unsubscribing from shared egress IPs. The budget is chosen before the body is read, so a POST whose token arrives only in the body stays on the 10/min public-write bucket.

### Token contract

- Shape: base64url(`payload`).base64url(`HMAC-SHA256 signature`) — exactly two dot-separated parts; the signature covers the purpose prefix `digest.unsubscribe.v1.` plus the payload, so tokens are domain-separated from changelog-unsubscribe and attachment-download tokens.
- Payload: `{"workspaceId", "email", "exp"}` — the owner email is lowercased at mint time and `exp` is a Unix millisecond timestamp ~**90 days** out, so a delayed click on a recent weekly mail still works.
- A token verifies only while the workspace still exists **and** its owner email still matches the payload — rotating the workspace owner invalidates outstanding tokens.
- Signing secret resolution is fail-closed: `DIGEST_EMAIL_TOKEN_SECRET`, then `CHANGELOG_EMAIL_TOKEN_SECRET`, then `JWT_SECRET`. With no usable secret, verification fails (`400`) **and** the digest cron skips the email entirely — a digest whose unsubscribe token cannot be minted is never sent.

Client guidance: never pre-fetch or "validate" footer URLs with a GET from automation (safe, but pointless — only POST mutates); don't fabricate, cache beyond 90 days, or attempt to parse tokens (they are opaque to clients); retry `429`s with exponential backoff.

---
