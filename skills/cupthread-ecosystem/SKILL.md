---
name: cupthread-ecosystem
description: Master navigation and architecture guide for the CupThread ecosystem (CupThread.com platform, Swift SDK, Android SDK, AgenticCoding). Use when working across CupThread projects, coordinating features, or understanding ecosystem architecture.
---

# CupThread Ecosystem Guide

CupThread is a multi-tenant SaaS feedback platform for app developers ([CupThread.com](https://cupthread.com)):

| Project | URL | Scope & Technologies |
|---|---|---|
| **CupThread Platform** | [CupThread.com](https://cupthread.com) | Core SaaS web services: Hono Cloudflare Worker API, React 19 + Vite Console/Landing/Web, Cloudflare D1/R2/CDN. |
| **Apple SDK** | [`CupThread/CupThreadSwiftSDK`](https://github.com/CupThread/CupThreadSwiftSDK) | SwiftUI SDK (iOS 17+, macOS 14+, visionOS 1+, tvOS 17+), SPM package, XCFramework binary distribution. |
| **Android SDK** | [`CupThread/CupThreadAndroidSDK`](https://github.com/CupThread/CupThreadAndroidSDK) | Kotlin + Jetpack Compose SDK (Android 8+ / minSdk 26), Maven artifacts on Cloudflare R2 CDN. |
| **Agentic Coding** | [`CupThread/CupThreadAgenticCoding`](https://github.com/CupThread/CupThreadAgenticCoding) | AI pair-programming skills, `@cupthread/cli` tool, and development utilities. |

---

## Architecture & Synchronization Rules

1. **API Contracts**:
   - Single source of truth for public API is hosted on `https://api.cupthread.com` (endpoints `/api/v1/public/*`, `/api/v1/feature-requests`, `/api/v1/feedback`).
   - SDK implementations:
     - Swift SDK: `Sources/CupThreadFeedback/FeedbackModels.swift`, `PublicAPI.swift`, `FeatureRequestModels.swift`.
     - Android SDK: `feedback/src/main/java/dev/cupthread/feedback/Models.kt`, `FeedbackClient.kt`.

2. **SDK Releases**:
   - Published SDKs are distributed from the high-speed immutable CDN at `https://cdn.cupthread.com`.
   - Apple SDK: `https://cdn.cupthread.com/sdks/apple/CupThreadFeedback-<version>.xcframework.zip`
   - Android SDK: `https://cdn.cupthread.com/maven/dev/cupthread/feedback/<version>/`

3. **Tenant & Privacy Boundaries**:
   - SDK endpoints (`/api/v1/public/*`) require only `appKey` and optional anonymous `userToken`.
   - Never leak developer identity, workspace internals, or cross-tenant records in public API or SDKs.
