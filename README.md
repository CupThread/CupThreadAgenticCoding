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

## CLI Commands

Install dependencies and build:
```sh
npm install
npm run build
```

Run CLI:
```sh
# View ecosystem status
node bin/cupthread.js status

# Link skills into any project (.agents, .claude, .zcode)
node bin/cupthread.js skills link /path/to/project
```

## License
MIT
