# AGENTS.md — CupThread Agentic Coding & CLI

## Purpose
`CupThreadAgenticCoding` contains AI-friendly tools, agent skills, and the `@cupthread/cli` command-line utility for developing and integrating with [CupThread.com](https://cupthread.com) and the CupThread SDKs.

## Multi-Repo Ecosystem
- **CupThread Platform**: [`CupThread.com`](https://cupthread.com) (Main SaaS website & backend API)
- **Apple SDK**: [`CupThread/CupThreadSwiftSDK`](https://github.com/CupThread/CupThreadSwiftSDK) (SwiftUI / SPM / XCFramework)
- **Android SDK**: [`CupThread/CupThreadAndroidSDK`](https://github.com/CupThread/CupThreadAndroidSDK) (Kotlin + Jetpack Compose)
- **Agentic Coding & CLI**: [`CupThread/CupThreadAgenticCoding`](https://github.com/CupThread/CupThreadAgenticCoding) (AI Skills, CLI tools)

## CLI Usage for Agents
- `node bin/cupthread.js status --json`: Inspect status in machine-readable JSON.
- `node bin/cupthread.js skills list`: List all agent skills.
- `node bin/cupthread.js skills link <targetDir>`: Symlink skills into `.agents`, `.claude`, and `.zcode` of target project.
