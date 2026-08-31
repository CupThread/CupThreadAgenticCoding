# AGENTS.md — CupThread Agentic Coding & CLI

## Purpose
`CupThreadAgenticCoding` contains AI-friendly tools, agent skills, and the `cupthread` command-line utility (implemented in Go) for developing and integrating with [CupThread.com](https://cupthread.com) and the CupThread SDKs.

## Multi-Repo Ecosystem
- **CupThread Platform**: [`CupThread.com`](https://cupthread.com) (Main SaaS website & backend API; source in the local `~/g/SaaS` monorepo)
- **Apple SDK**: [`CupThread/CupThreadSwiftSDK`](https://github.com/CupThread/CupThreadSwiftSDK) (SwiftUI / SPM / XCFramework)
- **Android SDK**: [`CupThread/CupThreadAndroidSDK`](https://github.com/CupThread/CupThreadAndroidSDK) (Kotlin + Jetpack Compose)
- **Agentic Coding & CLI**: [`CupThread/CupThreadAgenticCoding`](https://github.com/CupThread/CupThreadAgenticCoding) (AI Skills, Go CLI tools)

## CLI Usage for Agents
Build once (`go build -o bin/cupthread ./cmd/cupthread`, requires Go 1.25+), then:

- `bin/cupthread status --json`: Inspect local repo status in machine-readable JSON.
- `bin/cupthread skills list`: List all agent skills.
- `bin/cupthread skills link <targetDir>`: Symlink skills into `.agents`, `.claude`, and `.zcode` of target project.
- `bin/cupthread auth login --token cpt_...` (or plain `bin/cupthread auth login` for the browser OAuth flow): Authenticate.
- `bin/cupthread workspaces list`, `apps list`, `inbox list`, `features list`, `changelog list`, `billing show`, `search <q>`, …: Manage the caller's CupThread projects (the full Console surface).
- `bin/cupthread api request GET /api/v1/console/me`: Raw authenticated API passthrough.
- Append `--json` to any command for stable machine-readable output.

Global overrides: `--base-url` / `$CUPTHREAD_BASE_URL` (default `https://api.cupthread.com`, local dev `http://127.0.0.1:8787`), `--workspace` / `--app` for context, `$CUPTHREAD_TOKEN` for credential injection, `--config` / `$CUPTHREAD_CONFIG` for the config path (default `~/.config/cupthread/config.json`).

## API Contracts
The CLI mirrors the Console API in `~/g/SaaS/apps/api` (shared schemas: `SaaS/packages/shared/src/schemas.ts`). CLI authentication (access token + OAuth) is specified in `~/g/SaaS/docs/CLI-Access-Tokens.md` and `~/g/SaaS/docs/CLI-OAuth.md`.
