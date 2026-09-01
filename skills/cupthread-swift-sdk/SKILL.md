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
5. **User Attributes Sync**: Synchronize paying status, plan, MRR, and currency via `client.updateUserAttributes(...)`.

For complete method signatures, customization options, and advanced architecture, consult the [DocC API Documentation](https://cupthread.github.io/CupThreadSwiftSDK/).
