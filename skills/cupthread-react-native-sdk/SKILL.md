---
name: cupthread-react-native-sdk
description: Guide for integrating and using the CupThread React Native and Expo SDK in TypeScript applications.
---

# CupThread React Native SDK Guide

The **CupThread React Native SDK** (`@cupthread/react-native`) provides cross-platform UI components and client APIs for integrating user feedback, feature requests, roadmaps, and changelogs into React Native and Expo applications (iOS, Android, and Web).

## Documentation & Repository
- **GitHub Repository**: [https://github.com/CupThread/CupThreadReactNativeSDK](https://github.com/CupThread/CupThreadReactNativeSDK)
- **Official Website**: [https://cupthread.com](https://cupthread.com)

---

## 🤖 Quick Prompt for Coding Agents

```text
Integrate the CupThread SDK (feedback, roadmap, and feature requests screens) into this React Native app. Scaffold a dedicated configuration helper with a placeholder for the App Key, and at the end, remind me with step-by-step instructions on how to set my App Key safely (e.g. via .env or EXPO_PUBLIC_CUPTHREAD_APP_KEY).
```

---

## Installation

```sh
# npm
npm install @cupthread/react-native

# yarn
yarn add @cupthread/react-native

# expo
npx expo install @cupthread/react-native
```

---

## Setup & Initialization

```tsx
import React from 'react';
import {
  FeedbackClient,
  CupThreadProvider,
  RoadmapBoardScreen,
  FeatureRequestsScreen,
  WhatsNewScreen,
  ChangelogOverlay,
} from '@cupthread/react-native';

// 1. Initialize client with your App Key from the CupThread Console
const client = new FeedbackClient({
  baseUrl: 'https://api.cupthread.com',
  appKey: 'app_xxx',
});

// 2. Wrap your root component in CupThreadProvider
export default function App() {
  return (
    <CupThreadProvider client={client}>
      <RoadmapBoardScreen />
    </CupThreadProvider>
  );
}
```

---

## Ready-Made React Native Screens

Wrap your component tree in `<CupThreadProvider client={client}>` to automatically inherit developer console skin and theme settings.

- **`<RoadmapBoardScreen />`**: Kanban roadmap board with column tabs, cards, and stage chips.
- **`<FeatureRequestsScreen />`**: Searchable feature requests list with optimistic upvoting, version filter chips, and new request modal.
- **`<WhatsNewScreen />`**: Interactive changelog release notes with Markdown rendering and email updates subscription.
- **`<ChangelogOverlay visible={...} onClose={...} />`**: In-app modal announcing newest release notes.
- **`<FeedbackComposer visible={...} onClose={...} />`**: Structured feedback form with attachment uploads.
- **`<UserProfileScreen userId={...} />`**: Public user and developer profile page.

### Example: Presenting Latest Changelog on Launch

```tsx
import React, { useState } from 'react';
import { View, Button } from 'react-native';
import { CupThreadProvider, ChangelogOverlay, FeedbackClient } from '@cupthread/react-native';

const client = new FeedbackClient({
  baseUrl: 'https://api.cupthread.com',
  appKey: 'app_xxx',
});

export function MainScreen() {
  const [showChangelog, setShowChangelog] = useState(false);

  return (
    <CupThreadProvider client={client}>
      <View style={{ flex: 1, justifyContent: 'center', alignItems: 'center' }}>
        <Button title="What's New" onPress={() => setShowChangelog(true)} />
        <ChangelogOverlay visible={showChangelog} onClose={() => setShowChangelog(false)} />
      </View>
    </CupThreadProvider>
  );
}
```

---

## Key Features Overview

1. **Structured Feedback Submission**: Gather bug reports and feedback with automatic platform metadata and image/log attachments.
2. **Feature Request Voting & Ideation**: Community voting board with real-time optimistic state updates.
3. **Roadmap & Kanban Visibility**: Keep users engaged with public status columns and release targets.
4. **In-App Changelog & Announcements**: Present "What's New" release notes and let users subscribe for updates.
5. **User Attributes Sync**: Sync user payment status, plan name, MRR, and currency via `client.updateUserAttributes(...)`.

For complete method signatures, customization options, and architecture details, consult the [GitHub Repository](https://github.com/CupThread/CupThreadReactNativeSDK).
