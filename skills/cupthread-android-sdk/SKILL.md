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

For complete method signatures, customization options, and advanced architecture, consult the [KDoc API Documentation](https://cupthread.github.io/CupThreadAndroidSDK/).
