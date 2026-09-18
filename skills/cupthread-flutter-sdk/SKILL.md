---
name: cupthread-flutter-sdk
description: Guide for integrating and using the CupThread Flutter SDK in Dart applications.
---

# CupThread Flutter SDK Guide

The **CupThread Flutter SDK** (`cupthread_feedback`) provides native Flutter widgets and client APIs for integrating user feedback, feature requests, roadmaps, and changelogs into cross-platform Flutter applications (iOS, Android, macOS, Windows, Linux, Web).

## Documentation & Repository
- **GitHub Repository**: [https://github.com/CupThread/CupThreadFlutterSDK](https://github.com/CupThread/CupThreadFlutterSDK)
- **Official Website**: [https://cupthread.com](https://cupthread.com)

---

## 🤖 Quick Prompt for Coding Agents

```text
Integrate the CupThread SDK (feedback, roadmap, and changelog screens) into this Flutter app. Scaffold a dedicated configuration helper with a placeholder for the App Key, and at the end, remind me with step-by-step instructions on how to set my App Key safely (e.g. via --dart-define or .env).
```

---

## Installation

Add `cupthread_feedback` to your `pubspec.yaml`:

```yaml
dependencies:
  cupthread_feedback: ^0.1.0
```

Or run:

```sh
flutter pub add cupthread_feedback
```

---

## Setup & Initialization

```dart
import 'package:flutter/material.dart';
import 'package:cupthread_feedback/cupthread_feedback.dart';

void main() {
  // 1. Initialize client with your App Key from the CupThread Console
  final client = FeedbackClient(
    FeedbackClientConfig(
      baseUrl: 'https://api.cupthread.com',
      appKey: 'app_xxx',
    ),
  );

  // 2. Wrap your app in CupThreadTheme
  runApp(
    CupThreadTheme(
      client: client,
      child: const MaterialApp(
        home: RoadmapBoardScreen(),
      ),
    ),
  );
}
```

---

## Ready-Made Flutter Widgets

Wrap your widget hierarchy in `CupThreadTheme(client: client)` to automatically inherit developer console skin and theme settings.

- **`RoadmapBoardScreen()`**: Kanban roadmap board with column tabs, cards, and stage chips.
- **`FeatureRequestsScreen()`**: Searchable feature requests list with optimistic upvoting, version filter chips, and creation dialog.
- **`WhatsNewScreen()`**: Changelog release notes list with Markdown rendering and email subscription.
- **`ChangelogOverlay.show(context)`**: In-app modal dialog announcing new releases.
- **`FeedbackComposer()` / `FeedbackComposer.showModal(context)`**: Structured feedback form with attachment uploads.
- **`UserProfileView(userId: ...)`**: Public user and developer profile page.

### Example: Presenting Latest Changelog on Launch

```dart
@override
void initState() {
  super.initState();
  WidgetsBinding.instance.addPostFrameCallback((_) {
    ChangelogOverlay.show(context);
  });
}
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

## Feature Request Vote Rate Limits (429 Too Many Requests)

The public vote endpoints — `POST` / `DELETE /api/v1/feature-requests/{id}/vote` — are rate limited **per client IP** to **20 requests per minute**. Throttled calls fail with `429 {"error": "Too many votes. Please try again shortly."}` (a vote-specific body, distinct from the generic `Too many requests…` text). When building custom voting UI on top of the client:

- **Treat `429` as a recoverable, user-facing condition** — surface a friendly "you're voting too fast, try again in a minute" message rather than a generic error.
- **Never auto-retry `429` in a tight loop** — if you retry at all, back off for the remainder of the 60-second rate-limit window.
- **The built-in roadmap/voting screens need no changes** — normal usage (voting on a handful of feature requests) stays well under the limit.

For complete method signatures, customization options, and architecture details, consult the [GitHub Repository](https://github.com/CupThread/CupThreadFlutterSDK).
