---
name: cupthread-sdk-sync
description: Guide for synchronizing API schemas, models, and release history between CupThread.com and the native SDKs (Swift & Android).
---

# CupThread SDK Synchronization Guide

## 1. Syncing API Changes to SDKs
When modifying public API contracts:
1. Check impacted models (e.g. `PublicAppConfig`, `FeatureRequest`, `FeedbackItem`, `ChangelogItem`).
2. Update Swift SDK:
   - `Sources/CupThreadFeedback/PublicAPI.swift`
   - `Sources/CupThreadFeedback/FeedbackModels.swift`
   - `Sources/CupThreadFeedback/FeatureRequestModels.swift`
   - Run `swift test` in `CupThreadSwiftSDK`.
3. Update Android SDK:
   - `feedback/src/main/java/dev/cupthread/feedback/Models.kt`
   - `feedback/src/main/java/dev/cupthread/feedback/FeedbackClient.kt`
   - Run `./gradlew :feedback:testDebugUnitTest` in `CupThreadAndroidSDK`.

## 2. SDK Distribution
- Native artifacts publish directly to `https://cdn.cupthread.com`.
- Apple SDK zip & checksums publish to GitHub Releases and SPM binary targets.
- Android SDK POM & AAR publish to CDN Maven repository.
