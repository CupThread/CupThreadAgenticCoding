---
name: cupthread-api
description: Comprehensive reference for CupThread backend API endpoints, authentication flows, Cloudflare Workers architecture, and D1 database schema.
---

# CupThread API Reference

## Base URL
- Production: `https://api.cupthread.com`
- Local: `http://127.0.0.1:8787`

## Roles & Authentication
- **Developer / Console**: Clerk JWT session + workspace tenant verification (`/api/v1/console/*`).
- **End-User / Public**: Identified by `appKey` in path/query/body, optional `X-User-Token` header for anonymous voting/draft tracking (`/api/v1/public/*`, `/api/v1/feedback`, `/api/v1/feature-requests`).
- **Platform Admin**: Clerk session + platform role (`/api/v1/platform/*`).

## Key Public & SDK Endpoints

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/public/config/:appKey` | `GET` | Fetches app metadata, SDK skin/appearance, enabled platforms, changelog copy. |
| `/api/v1/public/columns/:appKey` | `GET` | Roadmap Kanban columns sorted by position. |
| `/api/v1/public/versions/:appKey` | `GET` | Release versions sorted by position. |
| `/api/v1/public/apps/:appKey/changelog` | `GET` | Published release notes and changelog items. |
| `/api/v1/public/apps/:appKey/changelog/subscribe` | `POST` | Subscribe email to changelog updates. |
| `/api/v1/public/apps/:appKey/changelog/unsubscribe` | `POST` | Unsubscribe email from changelog. |
| `/api/v1/public/apps/:appKey/user` | `PUT` | Update host app user attributes (paying, MRR, currency). |
| `/api/v1/feature-requests` | `GET` | List/search feature requests (`limit`, `offset`, `versionId`, `q`). |
| `/api/v1/feature-requests` | `POST` | Submit feature request. |
| `/api/v1/feature-requests/:id/vote` | `POST` | Upvote / remove vote on feature request. |
| `/api/v1/feedback` | `POST` | Submit feedback with optional attachments. |
| `/api/v1/uploads/images` | `POST` | Multipart upload for images to Cloudflare Images. |
| `/api/v1/uploads/r2` | `POST` | Multipart upload for logs / non-image attachments to Cloudflare R2. |
