---
name: cupthread-dev
description: Developer environment and local testing workflow guide for CupThread SaaS, Workers, D1 database, and SDK demos.
---

# CupThread Development Workflow

## Local Services
In `~/g/SaaS`:
- `npm run dev` — Starts API (`8787`), Console (`5173`), Web (`5174`), and Landing (`5175`).
- `npm --workspace apps/api run d1:migrate:local` — Applies D1 migrations.
- `npm run typecheck` — Full typecheck across all apps and shared packages.
- `npm run build` — Builds all surfaces for production.

## SDK Demo Testing
- **Apple Demo**: In `~/g/CupThreadSwiftSDK/Demo`, run in iOS Simulator against local backend:
  `SIMCTL_CHILD_CUPTHREAD_BASE_URL=http://127.0.0.1:8787 xcrun simctl launch booted com.lex.cupthread.demo`
- **Android Demo**: In `~/g/CupThreadAndroidSDK`, build with:
  `./gradlew :demo:assembleDebug -PcupthreadBaseUrl=http://10.0.2.2:8787`
