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

## Feature Request Vote Rate Limits (429 Too Many Requests)

The public vote endpoints — `POST` / `DELETE /api/v1/feature-requests/{id}/vote` — are rate limited **per client IP** to **20 requests per minute**. Throttled calls fail with `429 {"error": "Too many votes. Please try again shortly."}` (a vote-specific body, distinct from the generic `Too many requests…` text). When building custom voting UI on top of the client:

- **Treat `429` as a recoverable, user-facing condition** — surface a friendly "you're voting too fast, try again in a minute" message rather than a generic error.
- **Never auto-retry `429` in a tight loop** — if you retry at all, back off for the remainder of the 60-second rate-limit window.
- **The built-in roadmap/voting screens need no changes** — normal usage (voting on a handful of feature requests) stays well under the limit.

For complete method signatures, customization options, and advanced architecture, consult the [DocC API Documentation](https://cupthread.github.io/CupThreadSwiftSDK/).
