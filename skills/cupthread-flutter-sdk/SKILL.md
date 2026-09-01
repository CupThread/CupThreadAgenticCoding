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
Integrate the CupThread feedback roadmap and changelog screens into this Flutter app using appKey app_xxx.
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
5. **User Attributes Sync**: Sync user payment status, plan name, MRR, and currency via `client.updateUserAttributes(...)`.

For complete method signatures, customization options, and architecture details, consult the [GitHub Repository](https://github.com/CupThread/CupThreadFlutterSDK).
