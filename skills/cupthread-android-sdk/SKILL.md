---
name: cupthread-android-sdk
description: Guide for integrating and using the CupThread Android SDK in Kotlin and Jetpack Compose applications.
---

# CupThread Android SDK Guide

The **CupThread Android SDK** (`dev.cupthread:feedback`) provides native Jetpack Compose UI components and client APIs for integrating user feedback, feature requests, roadmaps, and changelogs into Android applications (Android 8+ / API 26+, Material 3).

## Documentation & Repository
- **Full API Documentation (KDoc)**: [https://cupthread.github.io/CupThreadAndroidSDK/](https://cupthread.github.io/CupThreadAndroidSDK/)
- **GitHub Repository**: [https://github.com/CupThread/CupThreadAndroidSDK](https://github.com/CupThread/CupThreadAndroidSDK)

---

## 🤖 Quick Prompt for Coding Agents

```text
Integrate the CupThread Android SDK (roadmap, changelog, and feedback screens) into this Compose app. Scaffold a dedicated configuration helper with a placeholder for the App Key, and at the end, remind me with step-by-step instructions on how to set my App Key safely (e.g. via local.properties).
```

---

## Installation

### 1. Add Maven Repository
Add the CupThread CDN Maven repository in your `settings.gradle.kts` (or root `build.gradle.kts`):

```kotlin
dependencyResolutionManagement {
    repositories {
        google()
        mavenCentral()
        maven {
            name = "CupThreadCDN"
            url = uri("https://cdn.cupthread.com/maven")
        }
    }
}
```

### 2. Add Dependency
Add the feedback SDK dependency to your `app/build.gradle.kts`:

```kotlin
dependencies {
    implementation("dev.cupthread:feedback:0.1.0")
}
```

---

## Setup & Initialization

```kotlin
import dev.cupthread.feedback.FeedbackClient
import dev.cupthread.feedback.FeedbackClientConfig
import dev.cupthread.feedback.UserTokenStore

// Initialize the client with your App Key from the CupThread Console
val client = FeedbackClient(
    FeedbackClientConfig(
        baseUrl = "https://api.cupthread.com",
        appKey = "app_xxx"
    )
)

// Obtain a persistent anonymous user token
val userToken = UserTokenStore.create(context).token
```

---

## Ready-Made Jetpack Compose Screens

Wrap your Compose UI hierarchy with `CupThreadTheme(client)` to inherit developer console skin and theme settings.

- **`RoadmapBoardScreen(client, userToken)`**: Kanban roadmap board with column chips and paged feedback cards.
- **`FeatureRequestsScreen(client, userToken)`**: Searchable feature requests list with optimistic voting, filtering, and creation dialog.
- **`WhatsNewScreen(client, userToken)`**: Changelog list with Markdown rendering and email updates subscription.
- **`ChangelogOverlay(client, visible, onDismiss)` / `client.presentLatestChangelog(activity)`**: In-app modal dialog announcing new releases.
- **`FeedbackComposer(client, userToken, onSubmit)`**: Structured feedback form with category selection, device metadata, and attachment uploads.

### Example Usage

```kotlin
@Composable
fun MyFeedbackScreen(client: FeedbackClient, userToken: String) {
    CupThreadTheme(client = client) {
        RoadmapBoardScreen(
            client = client,
            userToken = userToken
        )
    }
}

// Or present latest changelog modal on app launch:
client.presentLatestChangelog(activity)
```

---

## Key Features Overview

1. **Structured Feedback Submission**: Gather bug reports and feedback with automatic device/package metadata and image/log attachments.
2. **Feature Request Voting & Ideation**: Community voting board with real-time optimistic state updates.
3. **Roadmap & Kanban Visibility**: Keep users engaged with public status columns and release targets.
4. **In-App Changelog & Announcements**: Present "What's New" release notes and let users subscribe for updates.
5. **User Attributes Sync**: Sync user payment status, plan name, MRR, and currency via `client.updateUserAttributes(...)`. The underlying `PUT /api/v1/public/apps/{appKey}/user` endpoint is rate limited per client IP (60 requests/minute, HTTP `429`) — syncs of many users behind one shared IP must retry with exponential backoff.

## Request Correlation IDs (X-Request-Id)

Every CupThread API response carries an `X-Request-Id` header (OPS-01): your ID when the request supplied a format-valid one (`^[A-Za-z0-9._-]{8,64}$`, e.g. a UUID), otherwise a server-generated UUID. Quote it verbatim in bug reports and support requests — it lets CupThread locate the exact request in server logs. When driving the API with a raw `OkHttp` call instead of the SDK, send your own `X-Request-Id` per request; values outside that charset/length budget are silently ignored and replaced. CORS exposes the header, so browser-based integrations can read it too.

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

For complete method signatures, customization options, and advanced architecture, consult the [KDoc API Documentation](https://cupthread.github.io/CupThreadAndroidSDK/).
