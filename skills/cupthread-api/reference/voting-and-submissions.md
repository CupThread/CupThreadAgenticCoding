# Voting, Ship Notifications & Submission Quotas

Reference detail for the `cupthread-api` skill, split out of `SKILL.md` so the
skill stays under the agent-skill size budget (issue #318). The live OpenAPI 3.1
spec (<https://api.cupthread.com/api/v1/openapi.json>) remains the authoritative
contract; these files mirror the shipped behavior behind it.

## Ship-notification opt-in on vote

`POST /api/v1/feature-requests/:id/vote` accepts an optional `shipNotifyEmail` (an email string, max 254). When it is present, the address is enrolled in double-opt-in ship notifications ("email me when this ships"): a confirmation email with a single-use token is sent, the consent row starts *pending*, and only a confirmed address is ever mailed. The successful vote response stays `{voted, voteCount}` — it does not say whether that address was already enrolled. Omitting the field leaves the request and the response unchanged.

On a board that requires sign-in to vote, the address must be one of the session's verified emails (compared case-insensitively). Any other address still records the vote and adds a warning. No consent row is written and no confirmation email is sent. The warning body is the same whether or not that address already has a consent row:

```json
{
  "voted": true,
  "voteCount": 1,
  "warning": {
    "code": "email_not_verified",
    "error": "Ship notifications on this board are bound to your signed-in email address"
  }
}
```

Boards that allow anonymous voting accept any address and do not return this warning. A value that is not an email, or longer than 254 characters, fails validation with `400 {"error": "Validation failed"}`.

### Turnstile-gated anonymous vote (SEC-846, #243)

`PublicAppConfig` carries a `requireAnonymousVoteTurnstile` flag (boolean, always served, defaults to `false`) alongside the other anonymous-access settings. When a board sets it to `true`, an anonymous voter (no end-user session) must supply a verified `turnstileToken` (string, 10–2048 chars) on `POST /api/v1/feature-requests/:id/vote` — minted for action `vote` with cdata bound to the board's `appKey` (SEC-90: when the same anonymous payload also carries `shipNotifyEmail`, the server verifies the single token exactly once per request and accepts one minted for **either** the `vote` or the `ship-notify` widget action — cdata must still be the board's `appKey`) — or the endpoint rejects the vote outright with `403` and nothing is recorded:

- token missing → `403 {"error": "Human verification (Turnstile) is required", "code": "turnstile_required"}`
- token fails verification (invalid, expired, wrong action/cdata, already spent) → `403 {"error": "Human verification failed. Please try again.", "code": "turnstile_verification_failed"}`

Signed-in voters are exempt: the guard is `requireAnonymousVoteTurnstile && !identity`, so a request carrying a valid end-user session never needs the token for this gate. Read the flag off the public config (`GET /api/v1/public/config/{appKey}` or the workspace/app-slug route) before rendering a ballot and surface the host app's Turnstile challenge up front when it is `true`. When the config also publishes the optional `turnstile.siteKey`, a native host can mint the vote token by loading the hosted challenge page (`https://app.cupthread.com/turnstile/verify?siteKey=…&action=vote&cdata=<appKey>`) in a WebView — the page posts the token to the platform bridge and the token is single-use with a 300-second validity, so mint a fresh one per vote (see [Machine-Readable Turnstile 403 Codes and the Hosted Challenge Page (SaaS #1086)](endpoints.md#machine-readable-turnstile-403-codes-and-the-hosted-challenge-page-saas-1086)). The flag gates only the vote itself; the ship-notification opt-in guard below applies to every anonymous opt-in regardless of the flag.

### Turnstile-gated ship-notification opt-in (#242)

The vote body also accepts an optional `turnstileToken` (string, 10–2048 chars). When the server enforces Turnstile human verification, an anonymous voter that opts in via `shipNotifyEmail` should supply a verified token alongside the address. On boards whose `requireAnonymousVoteTurnstile` flag is `false`, a token that is missing or fails verification does **not** fail the vote — the vote still answers `200 {voted: true, voteCount}` — but the refusal is observable (SaaS #1472): the response carries a `warning` object with `code: "ship_notify_verification_failed"`, so a client must never treat a bare `200` as proof the address was enrolled:

```json
{
  "voted": true,
  "voteCount": 1,
  "warning": {
    "code": "ship_notify_verification_failed",
    "error": "Human verification failed, so the ship-notification opt-in was not recorded. Complete the verification and try again."
  }
}
```

Treat `ship_notify_verification_failed` as "opt-in refused (verification)" — distinct from the signed-in `email_not_verified` mismatch above and from a vote failure: surface the refusal and prompt the end user to complete the verification and vote again. When the flag is `true`, an anonymous opt-in with a missing or failed token never reaches that warning state: the SEC-846 gate rejects the vote outright with the `403`s documented above, so nothing is recorded at all. The flag gates only the vote itself — the opt-in guard applies to every anonymous opt-in, and on the combined vote + ship-notify request the server verifies the single token exactly once per call and accepts one minted with **either** the `vote` or the `ship-notify` widget action (SEC-90; cdata still the board's `appKey` — a token minted for any other intake kind, or bound to a different app's key, is still rejected), while when only this opt-in guard applies (the flag is `false`) the token must still be minted with action `ship-notify` — signed-in voters on sign-in-required boards bypass the check entirely (their address is already bound to a verified session email), and when the server has no Turnstile enforcement the field is ignored (verification silently skips, no warning).

---

## Submission Quota Errors (`402 Payment Required`)

`POST /api/v1/feature-requests` (and, with the same contract, `POST /api/v1/feedback`) responds with `402 Payment Required` and an `ErrorResponse` body (`{"error": string, "code"?: string}` — validation `400`s additionally carry the optional zod-flatten `details` object, never a `402`) when the app's workspace cannot accept new submissions:

| `code` | Meaning | Actionable guidance |
|---|---|---|
| `tier_limit_submissions` | The workspace reached its monthly submission quota. | Do not retry automatically. Tell the user to upgrade the workspace plan (Console → Billing) or wait for the quota to reset, then resubmit. |
| `subscription_inactive` | The workspace subscription is inactive or canceled. | Do not retry automatically. Tell the user to renew/reactivate the subscription (Console → Billing); submissions keep failing until then. |

Agents and SDK clients should parse the `code` field, treat `402` as a deterministic business rule (never a transient error), and surface the guidance above to the end user.

---

## Subscription Tier (`free` | `pro`)

`SubscriptionTier` on billing and workspace subscription payloads is `free` or `pro` only (BILL-553). The unprovisioned `business` value was removed from the shared enum: checkout, dev-set-tier, and platform subscription writes reject `tier: "business"` with `400`, and a legacy `business` row is coerced to `free` (`resolveTier`, migration `0064`). Do not model a third tier, and do not branch on the string `business`.

Every import source — GitHub issues, GitHub discussions, Linear, Notion, and Slack — requires Pro. A free workspace gets `402` `tier_limit_import_pro` (`Import from <source> requires the Pro plan.`). A canceled or past-due subscription gets `402` `subscription_inactive` (`Subscription is <status>. Fix billing to run imports.`). `cupthread billing show` reports the coerced tier.

---
