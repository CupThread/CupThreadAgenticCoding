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
Integrate the CupThread feedback roadmap and changelog screens into this Android app using appKey app_xxx.
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
5. **User Attributes Sync**: Sync user payment status, plan name, MRR, and currency via `client.updateUserAttributes(...)`.

For complete method signatures, customization options, and advanced architecture, consult the [KDoc API Documentation](https://cupthread.github.io/CupThreadAndroidSDK/).
