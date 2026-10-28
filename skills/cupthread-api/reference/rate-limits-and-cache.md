# Rate Limits, Shared Cache, Body Limits & Anonymous-Access Gates

Reference detail for the `cupthread-api` skill, split out of `SKILL.md` so the
skill stays under the agent-skill size budget (issue #318). The live OpenAPI 3.1
spec (<https://api.cupthread.com/api/v1/openapi.json>) remains the authoritative
contract; these files mirror the shipped behavior behind it.

## Rate Limiting (`429 Too Many Requests`)

Public write endpoints, the unauthenticated profile read (SEC-34), and the public reads (showcase, board listing, public config, comment threads, changelog feed, columns, versions) are budgeted **per client IP** (keyed on `CF-Connecting-IP`). Throttled requests get `429 {"error": "Too many requests. Please try again shortly."}` — except the feature-request vote endpoints, which return a vote-specific body: `429 {"error": "Too many votes. Please try again shortly."}`, searches, which return `429 {"error": "Too many searches. Please try again shortly."}`, and OAuth grant/consent endpoints, which return RFC 6749 `invalid_request` at `429`. Ordering: most intake routes check the declared body size **before** spending a rate-limit slot (SEC-36), so an oversized `Content-Length` is `413` (see [Request Body Limits (SEC-36)](#request-body-limits-sec-36)) even where a `429` would otherwise come first. Public config GETs, the showcase, the board listings, the five public board reads (comment threads, single-request detail, changelog feed, columns, versions), changelog POSTs, digest unsubscribe POSTs, and the OAuth consent POSTs spend their budget first, so an over-budget caller sees `429` before any body parse or lookup. The board-read GETs have no body:

| Endpoints | Budget | Why |
|---|---|---|
| `POST .../changelog/subscribe`, `GET`/`POST .../changelog/confirm`, `GET .../changelog/unsubscribe`, `GET .../public/digest/unsubscribe` | 10 requests / 60 s | Subscribe emails third parties and confirm/unsubscribe mutate subscription state, so the budget is tight. These endpoints **share one per-IP bucket** (`PUBLIC_WRITE_RATE_LIMITER`). |
| `POST .../changelog/unsubscribe` and `POST .../public/digest/unsubscribe` **with a query token** | 300 requests / 60 s | RFC 8058 one-click unsubscribes (PRIV-08): mailbox providers POST from a handful of shared egress IPs, so a POST whose query `?token=` is a whitespace-trimmed non-empty string gets its own per-IP budget (`DIGEST_UNSUBSCRIBE_RATE_LIMITER`) — 30× the interactive bucket above. The budget is selected **before the body is read** (SEC-27, unchanged by #635), so a POST with no query token — including one whose token arrives only in the JSON or form body — stays on the 10/min public-write bucket. Same generic `429` body. |
| `PUT /api/v1/public/apps/{appKey}/user`, `GET /api/v1/users/{userId}/profile` | 60 requests / 60 s | Every never-seen `userToken` mints an end-user row, and the unauthenticated profile GET resolves `u_*` ids; both share one per-IP bucket (`PUBLIC_USER_RATE_LIMITER`, SEC-14/SEC-34), so a profile-heavy hovercard can exhaust the attribute-sync budget and vice versa. |
| `POST`/`DELETE /api/v1/feature-requests/{id}/vote` | 20 requests / 60 s | The anonymous voter identity is a client-minted `userToken` UUID, so votes get their own per-IP cap (SEC-09) — one IP minting fresh tokens must not be able to inflate vote counts. |
| `GET /api/v1/public/showcase`, `GET /api/v1/public/apps`, `GET /api/v1/feature-requests/{id}`, `GET /api/v1/feature-requests/{id}/comments`, `GET /api/v1/public/apps/{appKey}/changelog`, `GET /api/v1/public/columns/{appKey}`, `GET /api/v1/public/versions/{appKey}` | 60 requests / 60 s | One shared per-IP bucket (`PUBLIC_READ_RATE_LIMITER`). Showcase `offset` above 5000 is clamped to 5000. A board page is a handful of the comment-thread, changelog, columns, and versions calls; a deep link adds one detail read; automated hammering is what spends the budget. Anonymous `200`s on those five may come from a 30-second shared cache. See [Public Board Read Limits and Shared Cache](#public-board-read-limits-and-shared-cache). |
| `GET /api/v1/public/config/{appKey}` and the slug config variant | 60 requests / 60 s | Dedicated `PUBLIC_CONFIG_RATE_LIMITER`. The spend is the first statement, before cache or D1, on both the `200` and the fail-closed `404`. |
| `GET /api/v1/feature-requests` (no `q`) | 60 requests / 60 s | Plain board listing (`BOARD_LIST_RATE_LIMITER`), separate from search so one page view does not share a bucket with typing. `offset` above 10000 is clamped to 10000. |
| `GET /api/v1/feature-requests?q=` | 30 requests / 60 s | Search stays on `SEARCH_RATE_LIMITER` and answers `429 {"error": "Too many searches. Please try again shortly."}`. |
| `GET`/`POST /api/v1/oauth/authorize`, `GET /api/v1/oauth/consent-info`, `GET /api/v1/oauth/device/lookup`, `POST /api/v1/oauth/device/decide` (+ the first per-IP spend on `POST /api/v1/oauth/token` and `POST /api/v1/oauth/revoke`) | 60 requests / 60 s | One shared per-IP authorize bucket (`OAUTH_TOKEN_RATE_LIMITER`, SEC-61): the key deliberately omits the presented `client_id`, so unknown-client probes cannot rotate ids for a fresh budget. `token` and `revoke` additionally spend a per-(client id, IP) key once the client row resolves (SEC-57). An over-budget `authorize` answer is the RFC 6749 §5.2 shape `429 {"error": "invalid_request", "error_description": "Too many requests"}`; the Console helpers answer `429` with a plain `{"error": …}` body. A legitimate interactive consent journey (GET authorize → consent-info → POST authorize) spends ~3 requests, so normal logins never hit the budget — automated hammering does. |

Budget-before-parse (SEC-27): `POST .../changelog/subscribe`, `POST .../changelog/confirm`, `POST .../changelog/unsubscribe`, `POST .../public/digest/unsubscribe`, `POST /api/v1/oauth/authorize`, and `POST /api/v1/oauth/device/decide` spend the rate-limit budget before reading the body. An over-budget caller on those routes sees `429` even when the body would also have been a `413`.

Retry guidance: treat `429` as transient — wait and retry with exponential backoff and jitter, never in a tight loop. SDKs syncing attributes for many users behind one shared IP (office NAT, CI farm) are the typical source of `429`s; batch or spread those syncs. Profile/hovercard rendering draws from the same 60/minute bucket: cache profile responses client-side instead of refetching per render. For votes, `429` is a recoverable user-facing condition: surface a friendly "you're voting too fast, try again in a minute" message instead of auto-retrying; normal tapping across a roadmap stays well under the 20/minute budget. For the five public board reads, back off for the rest of the minute and retry; their anonymous `200`s can be up to 30 seconds stale, so polling faster does not make a fresh write appear. On the OAuth consent journey (GET authorize → consent-info → POST authorize), a `429` is transient within the per-minute window — wait a few seconds and retry rather than treating it as a fatal protocol error. The CLI itself never calls consent-info or POST authorize (the browser-driven Console consent page does) and only opens the browser at the authorize URL, so interactive logins are unaffected; scripted consent automation — and `api request` GETs against these routes, which the CLI retries as idempotent body-less requests — needs the back-off.

---

## Public Board Read Limits and Shared Cache

Five public GETs document `429` and a short shared cache. No request or response body schema changed, and no existing status code was removed.

| Endpoint | Cache |
|---|---|
| `GET /api/v1/feature-requests/{id}/comments` | The thread body is the same for every caller who passed the gates, so every `200` may be cached. Each `cursor` page is its own entry. |
| `GET /api/v1/feature-requests/{id}` | Only an **anonymous** `200` is cached: with no token and no session the record is identity-independent (`hasVoted`/`isOwnRequest` constant false). A token- or session-bearing caller personalizes the record and skips the cache in **both** directions (SEC-15: the Cache API keys by URL and ignores `Vary`, so a personalized body must never share the anonymous URL's entry), and 404s are never cached — each hidden id (missing, unapproved, private, deactivated) pays its own lookup. |
| `GET /api/v1/public/columns/{appKey}` | The column list is identity-independent, so every `200` may be cached. |
| `GET /api/v1/public/versions/{appKey}` | The released-version list is identity-independent, so every `200` may be cached. |
| `GET /api/v1/public/apps/{appKey}/changelog` | Only an anonymous `200` is cached. A Clerk session skips the cache on both read and write, and that response sets `Vary: Authorization`. Linked feature-request titles stay session-dependent (`roadmapVisible`): an anonymous caller sees them only when `allowAnonymousRoadmap` is on. |

- **Budget.** The five routes share `PUBLIC_READ_RATE_LIMITER`: **60 requests / 60 s** per client IP (`CF-Connecting-IP`). `GET /api/v1/public/showcase` and its alias `GET /api/v1/public/apps` draw from the same bucket. Over budget the body is `429 {"error": "Too many requests. Please try again shortly."}` with no `code`. The limiter runs before the lookup.
- **Retry.** Treat that `429` as transient and back off for the rest of the minute. SDKs that already handle non-2xx need no new status mapping. The CLI retries body-less GETs, including `comments list` and `apps public-changelog`; once the minute is spent, the command still exits with the rate-limited hint.
- **Cache TTL.** An anonymous `200` may be served from the shared cache for up to **30 seconds**. A response served from that cache carries `Cache-Control: public, max-age=30`. The cache key is the full URL, so `limit` and `cursor` select different entries. A comment just posted, a column just edited, a version just marked released, a changelog just published, or an approval just flipped (a request becoming resolvable by deep link) can take that long to show up on an anonymous read.
- **Gates run before the cache read.** A sign-in-required board still answers `401` `authentication_required` and an unknown app or an unapproved comment thread still answers `404`, even while an older `200` sits in the cache. Since SaaS #1165/#1171 the columns and versions reads answer the same uniform `404 {"error": "App not found"}` (short-TTL negative-cached, byte-identical to the unknown-key body) for a known app with public access disabled — `403` there no longer means "the app exists but is disabled". The changelog feed is the exception: it still answers `403 {"error": "Public surfaces are disabled for this app"}` for a private app.
- **Session split is the changelog feed only.** Columns, versions, and comment threads have one body, so a `200` filled by any caller can be reused. A signed-in changelog payload is never stored and never returned to an anonymous caller. A developer `cpt_` token is not a Clerk end-user session, so it takes the anonymous cache path on the changelog feed.
- **Console listings are different routes.** `columns list`, `versions list`, `changelog list`, and `comments moderation list` are authenticated console reads. They are not this limiter and not this cache.
- **Console listing offsets are capped at 10,000.** The authenticated offset-paginated console listings — `GET /api/v1/console/workspaces/:wsId/feature-requests`, `/submissions`, `/notifications`, and `/changelog` — clamp `?offset=` at the shared `CONSOLE_LIST_MAX_OFFSET` of 10,000 (`apps/api/src/lib/pagination.ts`) exactly like the public board's legacy offset: an offset above 10,000 degrades to 10,000 and still returns `200`, and an unparseable or negative one degrades to `0`. A walk past the cap therefore keeps re-serving the same final page instead of erroring, so deep paging belongs on a keyset cursor where a surface offers one (the public feature-request feed's DATA-01 `cursor`); the CLI rewrites an out-of-range `--offset` locally and prints a `⚠` notice (see the `cupthread-cli` skill).

---

## Request Body Limits (SEC-36)

Intake routes that still follow SEC-36 parse JSON, form, and multipart bodies through bounded readers **before** rate limiting, signature checks, or validation — a request over the limit is rejected with no rate-limit counter tick and no database work. Changelog POSTs, digest unsubscribe POSTs, and the OAuth consent POSTs are the exception: they spend the limiter first (see [Rate Limiting](#rate-limiting-429-too-many-requests)). On `POST .../changelog/unsubscribe` that order is unchanged by #635: an over-budget one-click POST is `429` and the body is never parsed. The 256 KB cap still applies once that budget allows the read:

| Surface | Limit |
|---|---|
| Webhooks (GitHub / Polar / Clerk) and public token endpoints | 1 MB |
| Public JSON intake — feature requests, votes, comments, `PUT .../user`, `POST /feedback`, upload sessions, changelog subscribe/confirm/unsubscribe, digest unsubscribe | 256 KB |
| Console JSON (authenticated `cupthread` surfaces) | 1 MB |
| Console image multipart (app icon upload) | 11 MB (10 MB image + multipart envelope) |

- Over-limit → `413 {"error": "Payload exceeds size limit", "code": "payload_too_large"}`.
- Malformed `Content-Length` header (negative or non-integer) → `400 {"error": "Invalid Content-Length header", "code": "invalid_content_length"}`.

The ordering is wire-observable: `DELETE /api/v1/feature-requests/:id/vote` with an oversized body returns `413` **before** the vote rate limit can return `429`. Client guidance: keep end-user-generated JSON bodies well under the 256 KB public budget — truncate or drop oversized free-text fields before submitting rather than discovering the limit via `413`. The CLI shares the same surfaces: `cupthread apps update --icon` pre-checks the 10 MB image cap client-side (inside the server's 11 MB multipart envelope), and every file/stdin body the CLI sends (`api request --input`, `apps settings set --input`, `imports create --options`, `changelog create/update --body-file`, …) is read through a bounded reader against the route's cap first — over-limit input fails locally with `input_too_large` and nothing is sent, so a live `413` on those commands means the local check and the server disagreed.

---

## Per-App Anonymous-Access Enforcement (PRIV-12)

The per-app `PublicAppConfig` anonymous-access flags (`allowAnonymousRoadmap`, `allowAnonymousChangelog`) are enforced **server-side on every surface of the family**, not just the primary listing GET. When a tenant disables a flag, the other previously-public reads and writes of that family go behind sign-in immediately; response schemas are unchanged — only new error conditions appear (source: SaaS PR #330, issue #324).

| Endpoint | Condition | Response |
|---|---|---|
| `GET /api/v1/feature-requests` | anonymous read, `allowAnonymousRoadmap` off | `401` `authentication_required` (pre-existing) |
| `GET /api/v1/public/columns/:appKey` | anonymous read, `allowAnonymousRoadmap` off | `401` `authentication_required` |
| `GET /api/v1/public/versions/:appKey` | anonymous read, `allowAnonymousRoadmap` off | `401` `authentication_required` |
| `GET /api/v1/feature-requests/:id` | anonymous read, `allowAnonymousRoadmap` off | `401` `authentication_required` |
| `GET /api/v1/feature-requests/:id` | request exists but is **unapproved** | `404` `{"error": "Feature request not found"}` — existence hiding, indistinguishable from a missing id; the unapproved requester's own token does not un-hide it |
| `GET /api/v1/feature-requests/:id/comments` | anonymous read, `allowAnonymousRoadmap` off | `401` `authentication_required` |
| `GET /api/v1/feature-requests/:id/comments` | request exists but is **unapproved** | `404` `{"error": "Feature request not found"}` — existence hiding, indistinguishable from a missing id |
| `POST /api/v1/feature-requests/:id/vote` | anonymous write, `allowAnonymousRoadmap` off | `401` `authentication_required` — uniform body, no `voteCount`; the gate runs ahead of the feature-request existence lookup, so missing and approved ids are indistinguishable, and it fires even when `allowAnonymousVote` is on |
| `DELETE /api/v1/feature-requests/:id/vote` | anonymous write, `allowAnonymousRoadmap` off | `401` `authentication_required` — same uniform body, same ordering ahead of the existence lookup, same independence from `allowAnonymousVote` |
| `POST /api/v1/public/apps/:appKey/changelog/subscribe` | anonymous call, `allowAnonymousChangelog` off | `401` `authentication_required` |
| `POST /api/v1/public/apps/:appKey/changelog/subscribe` | signed-in, body `email` is not the session's verified address | `403` `{"error": "Subscriptions on this changelog are bound to your signed-in email address", "code": "email_not_verified"}` |

The `401` body is uniform across the roadmap surfaces: `{"error": "Sign in is required to view this roadmap" | "Sign in is required to view this changelog", "code": "authentication_required"}`. On the comments-thread and single-request-detail reads the `401` gate runs **after** the missing/private existence `404` but **before** the approved `404`: with the flag off, a missing id still answers `404` while any existing id — approved or pending — answers `401`, so anonymous probers cannot separate approved from pending ids, and a `404` still reliably means "no such id". The two vote-write gates are the strictest of the family: they run **ahead of** the feature-request existence lookup, so a gated board also keeps its missing-vs-approved oracle shut on the write paths — missing and approved ids both answer the `401`, never a `404`, and no `voteCount` is served.

Client guidance:

- Treat `401` + `code: "authentication_required"` on these previously-public reads as **"sign-in required"** and surface the host app's sign-in flow instead of a hard error — the same handling the feature-request list has always needed.
- Treat `401` + `code: "authentication_required"` on a vote `POST`/`DELETE` the same way: it is **sign-in required**, not a credentials bug — surface the host app's sign-in flow instead of retrying, and do not loop the request; a signed-in voter keeps voting on the same board under both `allowAnonymousVote` states.
- Treat `404` on a comments thread or a single-request detail read as **"not available"**: the request may be gone, private, or simply not approved yet. Never tell the user the request never existed.
- On `403` + `code: "email_not_verified"` from changelog subscribe, prompt for the **signed-in account's own email** — third-party addresses are not accepted on sign-in-only changelogs.

---
