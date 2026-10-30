# Upload Sessions & Attachments

Reference detail for the `cupthread-api` skill, split out of `SKILL.md` so the
skill stays under the agent-skill size budget (issue #318). The live OpenAPI 3.1
spec (<https://api.cupthread.com/api/v1/openapi.json>) remains the authoritative
contract; these files mirror the shipped behavior behind it.

## Attachment Content-Scan Rejections (`422 Unprocessable Entity`)

`POST /api/v1/feedback` requires every referenced `uploadId` to have **passed content inspection** at submission time (SEC-25). If an attachment was rejected during upload inspection (prohibited file type, malware signature, …), the whole submission fails with `422 Unprocessable Entity` instead of binding the invalid file:

```json
{
  "error": "Upload object <uploadId> was rejected by content scan: <reason>",
  "code": "scan_rejected"
}
```

Contract:

- The `: <reason>` suffix is present only when the scanner recorded a reason. Parse the `code` field, never the message text.
- `422` is reserved for `scan_rejected`. Two concurrent `POST /api/v1/feedback` requests that reference the same `uploadId` both pass the read-then-act checks; an atomic claim lets exactly one proceed. The loser returns `409 {"error": "Upload object <uploadId> has already been finalized into another submission", "code": "already_finalized"}` and creates no submission, notification, or quota write. Do not retry that `uploadId` — the winner owns it. A later submit that finds the upload already bound to a submission returns `400` with the same `code`. Every other attachment-validation failure (unknown `uploadId`, object from another workspace or app, non-`uploaded` state, expired session, uploader mismatch, more than 8 attachments) remains `400` with the same `{"error": string, "code"?: string}` shape.
- All attachments are validated before any is bound, so a scan-rejected submission binds none of its attachments — the scan-passing `uploadId`s stay unbound and reusable for a corrected resubmission.
- Rejection is deterministic — the same file fails again on retry, so never retry automatically. Tell the end user that the referenced attachment could not be uploaded because content inspection rejected it, and let them remove or replace the file before resubmitting.

The OpenAPI document exposes this `422` response on `POST /api/v1/feedback`.

---

## Uploader Identity Binding on Upload Sessions (SEC-28)

Every upload session is bound to an uploader identity at creation time, and feedback submission must present the **same identity**. Unbound sessions can no longer be created (fail-closed).

**Session creation — `POST /api/v1/uploads/sessions`:**

- **Anonymous callers** must send a valid `X-User-Token` UUID header (any UUID version, case-insensitive, surrounding whitespace ignored). Missing or malformed tokens are rejected with `400`:

```json
{ "error": "A valid X-User-Token UUID is required to create an upload session when not signed in", "code": "uploader_identity_required" }
```

- **Signed-in Clerk callers** are bound to their Clerk user id; `X-User-Token` is optional for them and ignored for identity resolution.

**Feedback submission — `POST /api/v1/feedback`:**

- Requests referencing `uploadId`s must present the identity that created the session: the same `X-User-Token`, or the same Clerk session. The check is **fail-closed** — a session that stored an identity plus a submitter with no identity is also a mismatch. Failures return `400`:

```json
{ "error": "Upload session was created by a different uploader", "code": "uploader_mismatch" }
```

- The referenced file is **not** attached and is left unfinalized, so a corrected resubmission (with the matching identity) can succeed without re-uploading.

Contract for clients and SDKs:

1. When creating an upload session for an anonymous end user, send the **same** `X-User-Token` already used for feedback submit and voting — never create sessions without an identity.
2. Never submit feedback with a different token (or a Clerk session) than the one used at session create.
3. Parse the `code` field and surface `uploader_identity_required` and `uploader_mismatch` as deterministic client errors (fix the identity, then retry), never as generic retryable `400`s.
4. The OpenAPI document exposes the `X-User-Token` header parameter on `POST /api/v1/uploads/sessions`.

---

## Feedback Attachment Upload Lifecycle (Upload Sessions)

Feedback attachments are uploaded through **pre-allocated upload sessions**; direct external URLs and raw storage keys are rejected at submission time. The flow is always: **create session → upload each reserved slot → submit feedback referencing the `uploadId`s**. Agents and automated tools must create the session first — there is no un-sessioned upload path left on the API.

**Step 1 — create the session: `POST /api/v1/uploads/sessions`** (JSON body):

```json
{
  "appKey": "app_xxx",
  "files": [
    { "clientFileId": "local-file-1", "filename": "screenshot.png", "mimeType": "image/png", "size": 102400 }
  ]
}
```

- 1–8 `files` per session. Each entry takes an optional `clientFileId` (echoed back for client-side correlation), a `filename`, and MIME/size in either the `mimeType`/`size` or the legacy `contentType`/`sizeBytes` spelling. An optional `purpose` field selects `feedback_attachment` (default) or `branding`; `turnstileToken` is optional and only checked when the app has Turnstile configured.
- `201` response: the envelope **always serves six top-level keys** (SaaS #1474): the nested session descriptor `session` = `{ "sessionId", "sessionToken", "expiresAt", "maxFileSizeBytes", "maxFiles" }` and the per-file client-handle array `files` = `[{ "clientFileId", "uploadId", "uploadUrl", "maxSizeBytes" }]` (same order as the request; a slot created without a `clientFileId` echoes its `uploadId` in that field), plus the legacy flat mirror for pre-envelope clients — `sessionId`, `sessionToken`, `expiresAt`, and `uploads` = `[{ "clientFileId"?, "uploadId", "uploadUrl", "filename", "mimeType", "maxBytes" }]`. Note the two arrays are **not** the same shape: `files[]` carries the client handle (`maxSizeBytes`), `uploads[]` carries the **intake descriptor** (`filename`/`mimeType`/`maxBytes`) — parse one consistently, never mix them. The `sessionToken` (prefix `cpt_up_`) authorizes the uploads and expires in about **1 hour**; `uploadId`s are shaped `upl_<32hex>` and `uploadUrl` is the relative `PUT` path. Treat `maxBytes`/`maxSizeBytes` as the per-file cap.
- Per-file policy failures surface at creation: `413` `file_too_large` (over the app's configured attachment limit), `400` `executable_extension_prohibited`, `400` `unsupported_mime_type`, and `429` `daily_storage_quota_exceeded` (workspace daily upload bytes). Shared submission-endpoint errors also apply (`402` quotas, `403` Turnstile, `404` unknown app, `401` when the app disables anonymous feedback).

**Step 2 — fill each reserved slot: `PUT /api/v1/uploads/{uploadId}`** (`POST` works too). Send the raw bytes with `Content-Type: <file mime>` and a binary body, or `multipart/form-data` with a `file` field. Authorize with `Authorization: Bearer <sessionToken>` (an `X-Upload-Session-Token` header is also accepted).

- Success `200`: `{ "uploadId", "status": "uploaded", "stored": true, "filename", "mimeType", "size", "sha256" }`.
- Uploads are bounded streams: an over-cap body fails with `413` `file_too_large` (the `Content-Length` header is checked before a byte is read).
- Content inspection (magic bytes, malware sniffing) runs on the stored bytes: a rejected file is marked scan-rejected and answers `415` with the inspection reason. Deterministic — never retry; drop or replace the file.
- Session/slot errors: `401` `unauthorized` (no token), `401` `session_invalid_or_expired`, `401` `session_expired`, `404` `upload_not_found` (that `uploadId` is not in this session), `409` `already_uploaded` (slots are single-shot), `409` `session_not_pending` (the session was already finalized into a submission). The item write is metered per client IP like session creation, so `429` can occur.

**Step 3 — submit feedback with the finalized ids: `POST /api/v1/feedback`**:

```json
{
  "appKey": "app_xxx",
  "title": "Issue title",
  "description": "Issue description",
  "platform": "universal",
  "uploadIds": ["upl_xxx"]
}
```

- Up to 8 `uploadIds`. The legacy `attachments` array is still accepted when every entry carries an `uploadId`; an attachment supplying a raw `url` or storage `key` instead is rejected with `400` `{"error": "Direct attachment URLs and keys are forbidden. Attachments must reference verified upload sessions via uploadId.", "code": "direct_attachment_forbidden"}`.
- Every referenced upload must be in `uploaded` state, unbound to another submission, belong to the same app/workspace, have passed content scan (`422` `scan_rejected`, SEC-25), and come from a session created by the **same identity** (`400` `uploader_mismatch`, SEC-28). All validation happens before anything binds, so a failed submission leaves its good `uploadId`s reusable for a corrected resubmission. A concurrent duplicate submit of the same `uploadId` loses the atomic claim and returns `409` `already_finalized` with nothing created; do not resubmit that id.
- A successful submission finalizes the uploads (they can never be re-bound) and answers one of **three success statuses, all with the same `FeedbackSubmissionResponse` body** `{ "submissionId", "forwardedToGithub", "githubDiscussionId"?, "githubDiscussionUrl"?, "warning"?, "warningCode"? }` — no attachment payloads (SaaS #1474): `200` when the submission was forwarded to GitHub Discussions synchronously (`forwardedToGithub: true`), `201` when it is stored in the CupThread inbox and the app has no GitHub delivery configured (`forwardedToGithub: false`), and `202` when it is stored with GitHub delivery queued and retrying (`forwardedToGithub: false`). On the `201`/`202` paths the advisory `warning` string is **always present** (e.g. `"Submission saved to the developer's CupThread inbox."`); on `200` it appears only when attachment signing is unconfigured. `warningCode` is the stable machine-readable counterpart to the human-readable `warning` (for example, `attachment_signing_unconfigured`); clients should prefer it for structured handling and localization and never parse the `warning` text. Download links are issued separately through the workspace Console; when the workspace lacks signing configured the API logs a warning and omits the links.

**Private attachment download — `GET /api/v1/feedback/attachments/{id}/download`** (`GET …/attachments/{id}` is an alias). Authorized by either a signed download token (`?token=<HMAC>`; tokens are bounded, at most 30 days) or workspace-member auth (`workspace.read`, e.g. a `cpt_` token in the owning workspace). Without either: `403` `{"error": "Authentication or valid download token required", "code": "unauthorized"}`. Unknown attachment → `404` `attachment_not_found`; missing storage object → `404` `file_not_found`. Responses are `Content-Disposition: attachment` with `private, no-store` caching — do not proxy or pre-fetch these URLs.

Client guidance:

1. Create the session **before** uploading anything, and reuse one session for all files of a single composer submission (1–8 slots).
2. Present the **same identity** on session creation and feedback submission — for anonymous users that means the same `X-User-Token` on both calls (SEC-28).
3. Send an honest `Content-Type` and stay under the slot's `maxBytes`; treat `413`/`415` as deterministic client errors and surface them to the user.
4. Never fabricate, guess, or pre-assign `uploadId`s, and never send raw attachment URLs or storage keys — `direct_attachment_forbidden` is a hard rejection.

---

## Feedback Image Media-Type and Storage-Failure Policy (415 / 500)

`POST /api/v1/uploads/images` (end-user feedback image upload) enforces a strict media-type policy:

- **Accepted types**: `image/png`, `image/jpeg`, `image/webp`, `image/gif` only. Any other declared **non-SVG** type fails earlier with `400 {"error": "Only PNG, JPEG, WebP, and GIF images are supported."}` — a declared `image/svg+xml` is excluded from that rule and gets the dedicated `415` below; empty files also fail with `400`, and files over 10 MB with `413`.
- **The stored image is a private attachment (PRIV-02).** Success is `200` `{ "kind": "image", "uploadId", "status": "uploaded", "stored": true, "filename", "mimeType", "size", "sha256" }` — no `url`, `key`, or `variants`. The object lives under `attachments/{workspaceId}/{appId}/{uploadId}/…` and `GET /api/v1/files/:key` rejects it with `403 private_attachment_forbidden`. After `POST /api/v1/feedback` finalizes the upload, download it through `GET /api/v1/feedback/attachments/:id/download`. Do not treat the upload as publicly fetchable.
- **SVG is rejected with `415`** — when the upload declares `image/svg+xml`, or when a `.svg` filename falls through the server's extension fallback (clients that send no per-part content type). SVG executes script in browsers and is never stored from end-user uploads: `415 {"error": "SVG images are not supported. Upload a PNG, JPEG, WebP, or GIF image."}`
- **Declared MIME must match the file content.** The server sniffs magic bytes (PNG, JPEG, GIF87a/GIF89a, WebP/RIFF). An unrecognized signature, or a mismatch with the declared type (e.g. HTML or SVG bytes named `.png`), fails with `415 {"error": "File content does not match declared image type (<declared>)."}`

The `415` bodies carry no `code` field — match on the status, not on an error code. The one coded body this endpoint answers with is the storage-failure `500`: any failure inside the route that is not one of the platform-authored validation errors above (a raw R2 / Cloudflare Images response body, transport error text, a D1 statement error) is replaced with the stable, provider-agnostic `500 {"error": "Image upload failed. Please try again.", "code": "image_upload_failed"}` — the same no-context rule the console icon route applies (SEC-69). The `error` field is fixed text (it previously could echo internal provider response fragments and transport errors), so existing display code keeps working unchanged, and the provider/infrastructure detail is logged server-side only with the request id — it never reaches the caller (SaaS PR #793).

Client guidance:

- Restrict client-side image pickers / file-type allowlists to PNG, JPEG, WebP, and GIF (drop SVG for feedback screenshots).
- Treat `415` as a deterministic client error: surface a user-facing "unsupported image type" message and let the user pick a different file; never retry automatically.
- On a `500`, branch on `code == "image_upload_failed"` to show a retry prompt and retry with backoff — never parse the `error` text (it is deliberately stable and opaque), and never treat the storage failure as a validation problem with the file.

**Console-configured app icons are exempt.** `POST /api/v1/console/workspaces/{wsId}/apps/{appId}/icon` (capability `app.configure`, so workspace admin/owner; `cpt_` API tokens are accepted) still accepts SVG icons, but screens them for active content — markers like `<script`, `javascript:`, `onload`/`onerror`/`onclick` handlers, `<!entity`, and `<foreignObject` fail with `415 {"error": "SVG contains prohibited active scripts or external entity references"}` — and applies the same magic-byte check. The endpoint stores the image and updates the app record (`iconUrl`) in the same request, returning the updated `AppRecord`.

---

## Optional Pre-Parse Authorization on `POST /api/v1/uploads/images` (header credentials)

The images route historically buffered the whole multipart body before any identity check, because the credentials could ride inside the form itself (`appKey`, `sessionToken`, `sessionId`) — the transport forced a parse-then-validate order. The endpoint now also accepts **out-of-band header credentials** (SaaS #1164 / SaaS#1174): send the public app key in the **`X-App-Key`** header and the upload session token in a header — **`X-Upload-Session-Token`**, preferred over `Authorization: Bearer <sessionToken>` so a signed-in caller can keep the Clerk JWT in `Authorization` — and the full authorization chain runs **before a single body byte is buffered**:

- **Rejections land before the body is read.** An unknown app → `404 {"error": "App not found"}`; a disabled app (`allowPublic = false`) → `403 {"error": "Public feedback is disabled for this app"}`; a missing identity on an app that requires sign-in → `401 {"error": "Sign in is required to upload feedback attachments", "code": "authentication_required"}`; and a missing session token (`401 upload_session_required`), an invalid or expired one (`401 session_invalid`), or a session already finalized into a submission (`409 session_not_pending`) — every one of these answers with the request stream untouched, so a client that presents headers never uploads a large body that will be thrown away.
- **Form fields are ignored in this mode.** The `appKey`, `sessionToken`, and `sessionId` form fields are not read when `X-App-Key` plus a session-token header are presented — send credentials only via headers. (A legacy caller that keeps credentials in the form fields gets the unchanged parse-then-validate order, with the header session token still winning over the form fields in the post-parse resolution.)
- **Tighter parse bound in header mode.** The anonymous pre-parse ceiling on this endpoint is a ~30 MB multipart envelope (30,000,000 bytes + 4 KB envelope slack — sized for the isolate's memory math, not the schema); an authorized header-mode upload parses against the app's own attachment bound instead, and an oversized declared body is rejected `413 file_too_large` before reading. Attachments above the platform ceiling belong on the session item-PUT path (`PUT /api/v1/uploads/{uploadId}`), which bounds its envelope by the declared file size.
- **Compatibility.** Sending neither header preserves the legacy behavior exactly (credentials in the multipart form fields, parse-then-validate order, unchanged response bodies). Both modes return identical response bodies and status codes — only the moment of rejection differs.

Client guidance: SDK image-upload helpers can opt in by setting `X-App-Key` and `X-Upload-Session-Token` (or `Authorization`) and dropping the credential form fields — upload rejections (unknown app, disabled app, sign-in required, dead session) then arrive before any body byte is transferred. No migration is required.

---
