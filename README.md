# CupThread Agentic Coding

AI-friendly CLI, agent skills, and SDK synchronization tools for the [CupThread.com](https://cupthread.com) platform.

## CupThread Ecosystem
- 🌐 [CupThread.com](https://cupthread.com) — Feedback SaaS platform, developer console, and API.
- 🍏 [CupThread/CupThreadSwiftSDK](https://github.com/CupThread/CupThreadSwiftSDK) — Apple platform SDK (SwiftUI / SPM / XCFramework).
- 🤖 [CupThread/CupThreadAndroidSDK](https://github.com/CupThread/CupThreadAndroidSDK) — Android SDK (Jetpack Compose / Maven).
- 🧠 [CupThread/CupThreadAgenticCoding](https://github.com/CupThread/CupThreadAgenticCoding) — AI-friendly CLI & Skills for pair programming.

---

## Agent Skills Included
- `cupthread-ecosystem`: Master navigation, platform concepts, and architecture.
- `cupthread-api`: Public, Console, and Admin API endpoints reference.
- `cupthread-sdk-sync`: Procedures for keeping API models and SDK implementations in sync.
- `cupthread-dev`: Local Cloudflare Workers, Pages, and SDK demo execution workflow.
- Full suite of Clerk authentication skills (`clerk`, `clerk-swift`, `clerk-android`, `clerk-react-patterns`, `clerk-billing`, `clerk-cli`, `clerk-webhooks`, etc.).

---

## The `cupthread` CLI (Go)

The `cupthread` binary lets developers manage the projects they created on
cupthread.com from the terminal — covering everything the web Console can do:
workspaces, members, apps and their settings, the feedback inbox, feature
requests, roadmap columns, versions, changelog entries, imports (GitHub /
Linear / Notion / Slack), integrations, notifications, billing, and global
search. Add `--json` to any command for machine-readable output.

### Build & install

Requires Go 1.25+.

```sh
go build -o bin/cupthread ./cmd/cupthread
# or install into $GOPATH/bin:
go install ./cmd/cupthread
```

### Log in

```sh
# OAuth (opens your browser; use --device on headless machines)
cupthread auth login

# Or with a personal access token from the Console (Settings → API Tokens)
cupthread auth login --token cpt_...
```

> The token/OAuth endpoints are being shipped in the platform repository — the
> contracts are specified in `SaaS/docs/CLI-Access-Tokens.md` and
> `SaaS/docs/CLI-OAuth.md`. Until then, point the CLI at a local dev API
> (`--base-url http://127.0.0.1:8787`) or use `$CUPTHREAD_TOKEN`.

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
