# CupThread Agentic Coding

AI-friendly CLI, agent skills, OpenAPI specifications, and SDK synchronization tools for the [CupThread.com](https://cupthread.com) platform.

## 🌐 Official API Documentation & OpenAPI Spec
- **Interactive API Documentation (Web)**: [https://cupthread.com/api](https://cupthread.com/api)
- **OpenAPI 3.1 Spec (JSON)**: [https://api.cupthread.com/api/v1/openapi.json](https://api.cupthread.com/api/v1/openapi.json)
- **OpenAPI 3.1 Spec (YAML)**: [https://api.cupthread.com/api/v1/openapi.yaml](https://api.cupthread.com/api/v1/openapi.yaml)

---

## 🤖 Install AI Skills via `npx skills`

Install the official CupThread skills directly into your workspace for **Claude Code, Cursor, Windsurf, Gemini CLI, Codex, Copilot, Antigravity**, and any Agentic Coding environment:

### Install all CupThread Skills
```sh
npx skills add CupThread/CupThreadAgenticCoding
```

### Or Install Specific Skills by Target SDK / Use Case
```sh
# Flutter & Dart SDK
npx skills add CupThread/CupThreadAgenticCoding --skill cupthread-flutter-sdk

# React Native & Expo SDK
npx skills add CupThread/CupThreadAgenticCoding --skill cupthread-react-native-sdk

# Apple Platform (Swift & SwiftUI) SDK
npx skills add CupThread/CupThreadAgenticCoding --skill cupthread-swift-sdk

# Android Platform (Kotlin & Jetpack Compose) SDK
npx skills add CupThread/CupThreadAgenticCoding --skill cupthread-android-sdk

# CupThread CLI & Console Management
npx skills add CupThread/CupThreadAgenticCoding --skill cupthread-cli

# CupThread Public & Backend API Reference
npx skills add CupThread/CupThreadAgenticCoding --skill cupthread-api
```

---

## 📋 Quick-Start Prompts for AI Coding Agents

After installing the desired skill, copy and paste the corresponding prompt to your coding agent:

#### For Flutter Apps
```text
Integrate the CupThread feedback roadmap and changelog screens into this Flutter app using appKey app_xxx.
```

#### For React Native & Expo Apps
```text
Integrate the CupThread feedback roadmap and feature requests screens into this React Native app using appKey app_xxx.
```

#### For Apple (Swift / SwiftUI) Apps
```text
Integrate the CupThread SwiftUI roadmap board and changelog overlay into this app using appKey app_xxx.
```

#### For Android (Kotlin / Compose) Apps
```text
Integrate the CupThread feedback roadmap and changelog screens into this Android app using appKey app_xxx.
```

#### For Custom API Integrations
```text
Please read the CupThread OpenAPI 3.1 specification at https://api.cupthread.com/api/v1/openapi.json and implement a type-safe API client for submitting user feedback and querying roadmaps.
```

---

## 📚 Available Skills Reference

| Skill Name | Description | Target Surface |
| :--- | :--- | :--- |
| **`cupthread-flutter-sdk`** | Guide for integrating Flutter widgets (`RoadmapBoardScreen`, `FeatureRequestsScreen`, `WhatsNewScreen`, `ChangelogOverlay`, `FeedbackComposer`, `UserProfileView`), models, and client APIs. | Flutter apps (iOS, Android, macOS, Windows, Linux, Web) |
| **`cupthread-react-native-sdk`** | Guide for integrating React Native / Expo screens (`<RoadmapBoardScreen />`, `<FeatureRequestsScreen />`, `<WhatsNewScreen />`, `<ChangelogOverlay />`, `<FeedbackComposer />`), ThemeProvider, and client APIs. | React Native & Expo apps (iOS, Android, Web) |
| **`cupthread-swift-sdk`** | Guide for integrating SwiftUI views (`RoadmapBoardView`, `FeatureRequestsView`, `WhatsNewView`, `.changelogOverlay`, `FeedbackComposerView`), SPM packages, and client APIs. | Apple platforms (iOS, macOS, visionOS, tvOS) |
| **`cupthread-android-sdk`** | Guide for integrating Jetpack Compose screens (`RoadmapBoardScreen`, `FeatureRequestsScreen`, `WhatsNewScreen`, `ChangelogOverlay`, `FeedbackComposer`), Maven dependencies, and client APIs. | Android apps (Kotlin / Material 3) |
| **`cupthread-cli`** | Complete guide for using the `cupthread` Go CLI tool to manage Console resources (workspaces, apps, feedback inbox, features, changelog, imports, billing) directly from the terminal. | Command Line / CI / Agent scripts |
| **`cupthread-api`** | OpenAPI 3.1 schema and interactive documentation (`https://cupthread.com/api`) for CupThread console management APIs and public feedback endpoints. | Custom API integrations & backend webhooks |

---

## CupThread Ecosystem
- 🌐 [CupThread.com](https://cupthread.com) — Feedback SaaS platform, developer console, and API.
- 🍏 [CupThread/CupThreadSwiftSDK](https://github.com/CupThread/CupThreadSwiftSDK) — Apple platform SDK (SwiftUI / SPM / XCFramework).
- 🤖 [CupThread/CupThreadAndroidSDK](https://github.com/CupThread/CupThreadAndroidSDK) — Android SDK (Jetpack Compose / Maven).
- ⚛️ [CupThread/CupThreadReactNativeSDK](https://github.com/CupThread/CupThreadReactNativeSDK) — React Native & Expo SDK (TypeScript).
- 💙 [CupThread/CupThreadFlutterSDK](https://github.com/CupThread/CupThreadFlutterSDK) — Flutter SDK (Dart).
- 🧠 [CupThread/CupThreadAgenticCoding](https://github.com/CupThread/CupThreadAgenticCoding) — AI-friendly CLI & Skills for pair programming.

---

## The `cupthread` CLI (Go)

The `cupthread` binary lets developers manage the projects they created on
cupthread.com from the terminal — covering everything the web Console can do:
workspaces, members, apps and their settings, the feedback inbox, feature
requests, roadmap columns, versions, changelog entries, imports (GitHub /
Linear / Notion / Slack), integrations, notifications, billing, and global
search. Add `--json` to any command for machine-readable output.

### Installation

#### Option A — Homebrew Tap (Recommended)

```sh
brew tap CupThread/tap
brew install cupthread

# or one-liner:
brew install CupThread/tap/cupthread
```

#### Option B — Go Install / Build from Source

Requires Go 1.25+.

```sh
# Install directly via Go
go install github.com/CupThread/CupThreadAgenticCoding/cmd/cupthread@latest

# Or build from local clone
go build -o bin/cupthread ./cmd/cupthread
```

### Log in

The CLI supports two authentication methods.

#### Option A — OAuth via browser (recommended for daily use)

```sh
cupthread auth login
```

This opens your browser; you approve access once and the CLI stores a
long-lived token pair in `~/.config/cupthread/config.json` (file mode
`0600`). Expired access tokens are refreshed automatically and transparently.
On machines without a local browser (SSH, containers) use the device flow:

```sh
cupthread auth login --device
# First, open:  https://console.cupthread.com/oauth/device
# Enter code:   CPTW-4F7K
```

#### Option B — Personal access token (recommended for CI, agents, scripts)

Create a token in the Console under **Settings → API Tokens** (it looks like
`cpt_...` and is displayed only once at creation). Then either store it:

```sh
cupthread auth login --token cpt_...
# or pass "-" to read the token from stdin, keeping it out of your
# shell history and CI logs:
cupthread auth login --token - < token.txt
```

…or skip local storage entirely and pass the token through the environment:

```sh
export CUPTHREAD_TOKEN="cpt_..."
cupthread apps list        # every command picks the env token up automatically
```

How the environment variable behaves:

- `$CUPTHREAD_TOKEN` **overrides** any stored credential, so CI jobs and
  agents can inject their own token without touching the shared config file.
- Scope it to a single invocation instead of exporting globally:
  `CUPTHREAD_TOKEN="cpt_..." cupthread inbox list`
- It composes with CI secrets and secret managers (GitHub Actions secrets,
  direnv, 1Password CLI, …). Tokens are high-entropy and can be revoked in
  the Console at any time.
- `cupthread auth status` shows which credential is active — it reports
  `token ($CUPTHREAD_TOKEN)` for the env var versus `token` or `oauth` for
  the config file.
- With an env token there is no refresh machinery involved: a PAT is a
  static credential, so simply create one with a suitable expiry for the job.

`cupthread auth logout` removes stored credentials from this machine (an env
token simply stops being set); it never revokes anything server-side — revoke
tokens in the Console or via `cupthread api request DELETE /api/v1/console/tokens/<id>`.

### Output formats

Every command renders an aligned table for humans by default, and can emit
structured output for scripts, agents, and pipelines:

```sh
cupthread inbox list --json        # shorthand for --output json
cupthread inbox list -o json       # same thing
cupthread workspaces list -o yaml  # YAML
cupthread billing show -o yaml
```

`--output` (short `-o`) accepts `table` (default), `json`, or `yaml`. In
`json` mode commands print a faithful, indented copy of the API response; the
`yaml` variant renders the same data as YAML. `cupthread api request` also
honors both formats for raw endpoint calls.

### Manage your projects

```sh
cupthread workspaces list
cupthread workspaces use ws_abc123          # set the default workspace
cupthread apps list
cupthread apps use my-app                   # set the default app
cupthread inbox list                        # triage feedback
cupthread features list --sort revenue      # Pro-gated revenue view
cupthread changelog create --title "v1.2" --body-file notes.md --publish-now
cupthread imports create --source github_issues --mode preview
cupthread billing show
cupthread search "dark mode"
```

Every command accepts `--json`; agents and scripts can also call any endpoint
directly:

```sh
cupthread api request GET /api/v1/console/me
```

### Repo tooling

```sh
# Branch/commit status of the local CupThread repos
bin/cupthread status [--json]

# Link the agent skills into any project (.agents, .claude, .zcode)
bin/cupthread skills link /path/to/project
```

### Environment variables

| Variable | Purpose |
|---|---|
| `CUPTHREAD_TOKEN` | Access token for CI/agents; overrides stored credentials |
| `CUPTHREAD_BASE_URL` | API base URL override (default `https://api.cupthread.com`) |
| `CUPTHREAD_CONFIG` | Config file override (default `~/.config/cupthread/config.json`) |

### Development

```sh
go build ./... && go vet ./... && go test ./...
```

## License
MIT
