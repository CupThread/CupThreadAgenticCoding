---
name: cupthread-swift-sdk
description: Guide for integrating and using the CupThread Swift SDK in iOS, macOS, visionOS, and tvOS applications.
---

# CupThread Swift SDK Guide

The **CupThread Swift SDK** (`CupThreadFeedback`) provides native SwiftUI components and client APIs for integrating user feedback, feature requests, roadmaps, and changelogs into Apple platform apps (iOS 17+, macOS 14+, visionOS 1.0+, tvOS 17+).

## Documentation & Repository
- **Full API Documentation (DocC)**: [https://cupthread.github.io/CupThreadSwiftSDK/](https://cupthread.github.io/CupThreadSwiftSDK/)
- **GitHub Repository**: [https://github.com/CupThread/CupThreadSwiftSDK](https://github.com/CupThread/CupThreadSwiftSDK)

---

## 🤖 Quick Prompt for Coding Agents

```text
Integrate the CupThread SwiftUI SDK (roadmap board, changelog overlay, and feedback composer) into this app. Scaffold a dedicated configuration helper with a placeholder for the App Key, and at the end, remind me with step-by-step instructions on how to set my App Key safely (e.g. via xcconfig or local config).
```

---

## Installation

### Swift Package Manager (SPM)
Add the package dependency in your `Package.swift` or via Xcode (**File > Add Package Dependencies...**):

```swift
dependencies: [
    .package(url: "https://github.com/CupThread/CupThreadSwiftSDK.git", from: "0.1.0")
]
```

### Prebuilt XCFramework Binary
Prebuilt XCFramework archives are distributed via GitHub Releases and the CupThread CDN:

```swift
targets: [
    .binaryTarget(
        name: "CupThreadFeedback",
        url: "https://cdn.cupthread.com/sdks/apple/CupThreadFeedback-0.1.0.xcframework.zip",
        checksum: "<sha256-checksum>"
    )
]
```

---

## Setup & Initialization

```swift
import SwiftUI
import CupThreadFeedback

// Initialize the feedback client with your App Key from CupThread Console
let client = FeedbackClient(
    configuration: FeedbackClientConfiguration(
        baseURL: URL(string: "https://api.cupthread.com")!,
        appKey: "app_xxx"
    )
)

// Obtain a persistent anonymous user token
let userToken = UserTokenStore.shared.token
```

---

## Ready-Made SwiftUI Views

Wrap your views with `CupThreadTheme(client:)` to automatically apply console appearance settings and theme styling.

- **`RoadmapBoardView(client:userToken:)`**: Kanban roadmap board grouped by public columns with vote counts and stage badges.
- **`FeatureRequestsView(client:userToken:)`**: Paged feature requests list with search, version filtering, optimistic voting, and submission flow.
- **`WhatsNewView(client:userToken:)`**: Interactive release notes / changelog with Markdown formatting and email subscription.
- **`ChangelogOverlayView` / `.changelogOverlay(client:isPresented:)`**: In-app modal sheet announcing new release notes.
- **`FeedbackComposerView(client:userToken:onSubmit:)`**: Structured feedback form supporting text details, device metadata, and image/log attachment uploads.

### Example Usage

```swift
struct MyFeedbackView: View {
    let client: FeedbackClient
    let userToken: String
    @State private var showWhatsNew = false

    var body: some View {
        CupThreadTheme(client: client) {
            NavigationStack {
                RoadmapBoardView(client: client, userToken: userToken)
            }
            .changelogOverlay(client: client, isPresented: $showWhatsNew)
        }
    }
}
```

---

## Payment-Attribute Signing (HMAC-SHA256, DATA-03)

`PUT /api/v1/public/apps/{appKey}/user` only persists paying status, plan, and MRR when the request is **signed with the app's SDK signing secret** (developer console: *App Access → App Credentials → SDK signing secret*). Whenever the body contains any of `isPaying`, `mrr`, or `plan` (an explicit JSON `null` counts), it must also carry `signature` (64-char hex HMAC-SHA256, case-insensitive) and `timestamp` (epoch seconds) — both plain body fields. Identity-only and currency-only writes stay unsigned. Rejections happen before any profile row is created:

- `422 payment_attributes_require_signature` — payment fields without `signature` + `timestamp`
- `422 sdk_signing_secret_not_configured` — the app has no signing secret yet
- `401 stale_signature` — `timestamp` more than ±300 s from server time
- `401 invalid_signature` — wrong key or tampered values

**Canonical string** — HMAC-SHA256-sign the exact bytes of this newline-joined string (no trailing newline) and hex-encode the digest:

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

- Absent fields sign as `unset`, explicit JSON `null` as `null` (omitting `plan` ≠ sending `"plan": null`). `userToken` is the body value when present, else the `X-User-Token` header value.
- `canonicalNumber` follows JS `Number.prototype.toFixed(2)` with trailing zeros and a trailing `.` stripped (`1200.00 → "1200"`, `99.50 → "99.5"`); exact binary ties pick the larger n (`10.125 → "10.13"`). Compute it from the raw JSON value as sent.
- Sign immediately before sending (±300 s freshness window) and don't mutate signed values when appending `signature`/`timestamp` to the body.

```swift
import CryptoKit

func userAttrsSignature(canonical: String, secret: String) -> String {
    let mac = HMAC<SHA256>.authenticationCode(
        Data(canonical.utf8),
        using: SymmetricKey(data: Data(secret.utf8)))
    return mac.map { String(format: "%02x", $0) }.joined()
}
```

On targets without CryptoKit, CommonCrypto's `CCHmac(kCCHmacAlgSHA256, key, keyLength, data, dataLength, out)` yields the same digest.

**Cross-check your port** against this reference vector before shipping: secret `cpt_sk_test_secret_0123456789abcdef`, appKey `app_demo12345`, userToken `3fa85f64-5717-4562-b3fc-2c963f66afa6`, timestamp `1758000000`, body `{"isPaying":true,"plan":"pro","mrr":299.5,"currency":"USD"}` → canonical `cpt-user-attrs-v1\napp_demo12345\n3fa85f64-5717-4562-b3fc-2c963f66afa6\ntrue\npro\n299.5\nUSD\n1758000000` → signature `59bc1177751f4c36f9caeed8c763cde9ee18835196acd1b6d4ce3b1ea2dc0273`. `cupthread api sign-user-attrs` generates more vectors.

## Key Features Overview

1. **Feedback & Attachment Uploads**: Submit categorized feedback with automatic device info and screenshot/log attachments.
2. **Feature Request Voting**: Real-time optimistic upvoting, search, and submission for user-driven features.
3. **Roadmap & Kanban**: Visualize planned, in-progress, and completed roadmap milestones.
4. **Changelog & "What's New"**: Display rich Markdown release notes with email update subscription.
5. **User Attributes Sync**: Synchronize paying status, plan, MRR, and currency via `client.updateUserAttributes(...)`. The underlying `PUT /api/v1/public/apps/{appKey}/user` endpoint is rate limited per client IP (60 requests/minute, HTTP `429`) — syncs of many users behind one shared IP must retry with exponential backoff.

## Request Correlation IDs (X-Request-Id)

Every CupThread API response carries an `X-Request-Id` header (OPS-01): your ID when the request supplied a format-valid one (`^[A-Za-z0-9._-]{8,64}$`, e.g. a UUID), otherwise a server-generated UUID. Quote it verbatim in bug reports and support requests — it lets CupThread locate the exact request in server logs. When driving the API with a raw `URLSession` call instead of the SDK, send your own `X-Request-Id` per request; values outside that charset/length budget are silently ignored and replaced. CORS exposes the header, so browser-based integrations can read it too.

## Feedback Image Restrictions (415 Unsupported Media Type)

The feedback image upload endpoint accepts only **PNG, JPEG, WebP, and GIF**. When integrating the attachment picker:

- **Drop SVG from image pickers / file-type allowlists.** `image/svg+xml` is rejected with `415` — declared via MIME type, or via a `.svg` filename when no content type is sent. SVG executes script in browsers and is never stored from end-user uploads.
- **The declared MIME type must match the file content.** The server sniffs magic bytes; mismatched or unrecognized files (e.g. HTML bytes named `.png`) fail with `415 {"error": "File content does not match declared image type (...)"}`.
- **Map HTTP `415` to a user-facing "unsupported image type" message** and let the user pick a different file. It is a deterministic client error — never retry automatically.

Console-configured app icons (developer-facing) are unaffected and may still use screened SVG.

## Feedback Attachment Upload Flow (Upload Sessions)

Attachment uploads go through **pre-allocated upload sessions** — feedback submissions referencing raw attachment URLs or storage keys are rejected (`400` `direct_attachment_forbidden`). For custom integrations outside the SDK composer, drive the three-step lifecycle directly:

1. **Create a session first**: `POST /api/v1/uploads/sessions` with `{ "appKey": "…", "files": [{ "clientFileId": "…", "filename": "…", "mimeType": "…", "size": 123 }] }` (1–8 files). Anonymous users must send the **same `X-User-Token`** they will use for feedback submit — the session is identity-bound (SEC-28). The `201` response provides `sessionToken` (`cpt_up_…`, ~1-hour TTL), `expiresAt`, and `uploads[]` slots with `uploadId` (`upl_…`), `uploadUrl`, and `maxBytes`.
2. **Upload each reserved slot**: `PUT /api/v1/uploads/{uploadId}` with `Authorization: Bearer <sessionToken>` and the raw file bytes (`Content-Type` set to the file's real MIME type; `multipart/form-data` with a `file` field also works). Slots are single-shot (`409` `already_uploaded`), over-cap bodies fail with `413` `file_too_large`, and failed content inspection fails with `415` — deterministic, never retry.
3. **Submit feedback with the ids**: pass the finalized `uploadId`s (max 8) in the `uploadIds` array of `POST /api/v1/feedback`. A scan-rejected attachment fails the whole submission with `422` `scan_rejected` — remove or replace that file and resubmit; the passing uploads stay reusable.

Use the per-file `maxBytes` from the session response to pre-validate file sizes client-side, and reuse one session for all files of a single composer submission.

## Feature-Request List Pagination (Cursor Keyset)

The public feature-request feed — `GET /api/v1/feature-requests` — is keyset-cursor-paginated (DATA-01). Every response carries `requests`, `total`, `hasMore`, and `nextCursor`; walk large boards by echoing `nextCursor` back as the `cursor` query parameter until it returns `null`:

- **Prefer `cursor` over incrementing `offset`** — offset pages scan and discard rows server-side, while the cursor jumps straight to the next key. A request that sends `cursor` ignores `offset`.
- **Treat the cursor as opaque** — never parse, construct, or persist one beyond forwarding it back; malformed cursors fail with `400 {"error": "Invalid cursor"}`.
- **`hasMore` is exact** (the server fetches one extra row) and `total` stays constant across pages; `limit` is clamped to 1–200 (default 50), and `q`/`versionId` filters compose with the cursor.
- **The built-in roadmap/feedback screens need no changes** — the fields are additive; clients that only read `requests` keep working.

## Feature Request Vote Rate Limits (429 Too Many Requests)

The public vote endpoints — `POST` / `DELETE /api/v1/feature-requests/{id}/vote` — are rate limited **per client IP** to **20 requests per minute**. Throttled calls fail with `429 {"error": "Too many votes. Please try again shortly."}` (a vote-specific body, distinct from the generic `Too many requests…` text). When building custom voting UI on top of the client:

- **Treat `429` as a recoverable, user-facing condition** — surface a friendly "you're voting too fast, try again in a minute" message rather than a generic error.
- **Never auto-retry `429` in a tight loop** — if you retry at all, back off for the remainder of the 60-second rate-limit window.
- **The built-in roadmap/voting screens need no changes** — normal usage (voting on a handful of feature requests) stays well under the limit.

## Public Profile Rate Limits & Unknown-User 404 (SEC-34)

The public profile page / hovercard reads `GET /api/v1/users/{userId}/profile`, which is rate limited **per client IP** to **60 requests per minute** — one bucket shared with the `PUT /api/v1/public/apps/{appKey}/user` attribute sync. Throttled calls fail with the generic `429 {"error": "Too many requests. Please try again shortly."}`. An unknown app-scoped `u_*` id (or a `u_*` id sent without `appKey`) returns `404 {"error": "User profile not found"}` instead of a placeholder:

- **Cache profile lookups client-side** and reuse them across renders. A card list resolving many users behind one shared IP (office NAT, CI farm) can exhaust the 60/minute budget — which also throttles attribute syncs from the same IP.
- **Handle `429` with exponential backoff** and render a friendly "try again shortly" state; never retry in a tight loop.
- **Treat `404` on a `u_*` id as "no public profile for this id"** and fall back to the placeholder UI — the server no longer scans for unmatched ids, so retrying cannot change the answer. Raw `user_*` ids from old `/u/` links keep the opt-in placeholder behavior.

For complete method signatures, customization options, and advanced architecture, consult the [DocC API Documentation](https://cupthread.github.io/CupThreadSwiftSDK/).
