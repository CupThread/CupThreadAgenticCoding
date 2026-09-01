---
name: cupthread-cli
description: Guide for using the cupthread CLI command-line tool to manage workspaces, apps, feedback inbox, feature requests, roadmaps, changelogs, and integrations.
---

# CupThread CLI Guide

The `cupthread` CLI allows developers and AI agents to manage all resources on [CupThread.com](https://cupthread.com) (workspaces, apps, feedback inbox, feature requests, roadmap columns, versions, changelogs, integrations, billing) directly from the terminal.

---

## 1. Installation

If `cupthread` is not yet installed or not found in `$PATH`:

### Option A: Homebrew Tap (Recommended)
```sh
brew tap CupThread/tap
brew install cupthread

# Or one-liner:
brew install CupThread/tap/cupthread
```

### Option B: Go Install (requires Go 1.25+)
```sh
go install github.com/CupThread/CupThreadAgenticCoding/cmd/cupthread@latest
```

### Option C: Build from Source
If working directly inside the `CupThreadAgenticCoding` repository:
```sh
go build -o bin/cupthread ./cmd/cupthread
```

---

## 2. Authentication

### Method A: Browser / Device OAuth (Interactive)
```sh
# Browser OAuth flow
cupthread auth login

# Headless / SSH device code flow
cupthread auth login --device
```

### Method B: Personal Access Token (CI / Agents)
```sh
# Stored token
cupthread auth login --token cpt_...

# Or inject via environment variable (overrides stored credentials)
export CUPTHREAD_TOKEN="cpt_..."
```

Check current authentication status:
```sh
cupthread auth status
```

---

## 3. Global Flags & Output Options

- `--json`: Shorthand for `-o json` (emits indented machine-readable JSON). **Recommended for AI agents.**
- `-o, --output <table|json|yaml>`: Select output formatting (default `table`).
- `-w, --workspace <id>`: Target workspace ID (overrides default).
- `-a, --app <id>`: Target app ID (overrides default).
- `--base-url <url>`: API endpoint override (default `https://api.cupthread.com`).

---

## 4. Key CLI Commands

### Workspaces & Context
```sh
cupthread workspaces list                  # List all available workspaces
cupthread workspaces use <workspace-id>    # Set active workspace context
cupthread me                               # Show current user, workspaces, and roles
```

### Apps Management
```sh
cupthread apps list                        # List apps in current workspace
cupthread apps use <app-id>                # Set active app context
cupthread apps get <app-id>                # Show app details and configuration
cupthread apps create --name "My App"      # Create a new app
```

### Feedback Inbox Triage
```sh
cupthread inbox list                       # List recent feedback submissions
cupthread inbox list --status open --json  # List open feedback in JSON
cupthread inbox get <feedback-id>          # View feedback details and attachments
cupthread inbox update <feedback-id> --status resolved
```

### Feature Requests & Roadmap
```sh
cupthread features list                    # List feature requests
cupthread features list --sort revenue     # Sort by user ARR/MRR (Pro plan)
cupthread features get <request-id>        # View feature request details (requester info, commenters)
cupthread features create --title "Dark mode" --description "Add dark theme support"
cupthread columns list                     # List public roadmap columns
cupthread versions list                    # List release milestones / versions
```

### Comments & @Replies
```sh
cupthread comments list <featureRequestId> # List comments on a feature request
cupthread comments create <featureRequestId> --body "Great idea!" [--reply-to <clerkId>] [--parent-id <commentId>]
```

### User Profiles
```sh
cupthread users profile <userId>           # Look up a public developer profile, apps, and comments
```

### Changelog & Releases
```sh
cupthread changelog list                   # List published and draft changelogs
cupthread changelog create --title "v1.2.0" --body-file ./release-notes.md --publish-now
```

### Search
```sh
cupthread search "crash on login"          # Global fuzzy search across apps, feedback, and roadmap
```

### Raw API Passthrough
Agents can invoke any API endpoint directly:
```sh
cupthread api request GET /api/v1/console/me --json
```

### Repository & Skills Management
```sh
# Inspect git status of local CupThread repositories
bin/cupthread status --json

# Symlink skills into target project (.agents, .claude, .zcode)
bin/cupthread skills list
bin/cupthread skills link /path/to/target/project
```

---

## 5. Agent Best Practices

1. **Always use `--json`**: When calling CLI commands from automated tools, subagents, or scripts, append `--json` for predictable, parseable output.
2. **Set context once**: Use `cupthread workspaces use <id>` and `cupthread apps use <id>` to avoid repeating `-w` and `-a` on every command.
3. **Use `$CUPTHREAD_TOKEN` in CI**: Inject credentials via environment variable rather than storing them in config files.
