package api

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// Client talks to the CupThread API. It is safe for concurrent use.
type Client struct {
	// BaseURL is the API origin, e.g. https://api.cupthread.com.
	BaseURL string
	HTTP    *http.Client
	// WorkspaceID, when set, is sent as X-Workspace-Id on every request.
	WorkspaceID string
	// AppKey, when set, is sent as X-App-Key on every request.
	AppKey string
	// UserToken, when set, is sent as X-User-Token on every request.
	UserToken string
	// Token returns the bearer credential. It is consulted per request so
	// OAuth tokens can be refreshed transparently.
	Token func(ctx context.Context) (string, error)
}

// New creates a client for baseURL.
func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

// requestIDHeader is the OPS-01 correlation header: every API response
// echoes it, and the API accepts a caller-supplied value only when it
// matches ^[A-Za-z0-9._-]{8,64}$ (anything else is ignored and replaced).
const requestIDHeader = "X-Request-Id"

// NewRequestID returns a fresh correlation ID in the cli-<uuid> form the API
// accepts — 40 characters within the 8–64 charset budget. The time+pid
// fallback stays inside the same charset in case crypto/rand ever fails.
func NewRequestID() string {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		return fmt.Sprintf("cli-%d%d", time.Now().UnixNano(), os.Getpid())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("cli-%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// hasRequestIDHeader reports whether the caller already supplied an
// X-Request-Id in the extra-headers map (key case-insensitive).
func hasRequestIDHeader(headers map[string]string) bool {
	for k := range headers {
		if http.CanonicalHeaderKey(k) == requestIDHeader {
			return true
		}
	}
	return false
}

// APIError is a non-2xx API response.
type APIError struct {
	Status  int
	Message string
	Code    string
	// RequestID is the X-Request-Id correlation value the API echoed on the
	// failing response (OPS-01): the caller's ID when it was format-valid,
	// otherwise a server-generated UUID. Quote it in bug reports and support
	// requests so the server side can find the exact request.
	RequestID string
	// Details is the server's field-level validation payload from a 400
	// response — the zod `.flatten()` object `{"formErrors": [...],
	// "fieldErrors": {field: [reasons...]}}` — kept as raw JSON verbatim for
	// programmatic consumers. Error() renders a capped, sanitized human
	// summary; it is nil whenever the server sent no details.
	Details json.RawMessage
}

// Limits for rendering APIError.Details so a large schema error cannot flood
// the single error line: at most detailsMaxGroups groups (form errors count
// as one group each), each truncated to detailsMaxRunes runes.
const (
	detailsMaxGroups = 5
	detailsMaxRunes  = 120
)

// validationDetails mirrors the server's zod `.flatten()` payload sent as
// `details` on every 400 Validation failed response.
type validationDetails struct {
	FormErrors  []string            `json:"formErrors"`
	FieldErrors map[string][]string `json:"fieldErrors"`
}

// detailsSuffix renders Details as a single `: …` line suffix: form errors
// first, then field errors in sorted key order with the field's reasons
// joined by "; ". It returns "" when there is nothing to show (no details,
// undecodable payload, or an empty flatten), which keeps Error() output
// byte-identical to the pre-details rendering.
func (e *APIError) detailsSuffix() string {
	if len(e.Details) == 0 {
		return ""
	}
	var d validationDetails
	if json.Unmarshal(e.Details, &d) != nil {
		return ""
	}
	groups := make([]string, 0, len(d.FormErrors)+len(d.FieldErrors))
	groups = append(groups, d.FormErrors...)
	keys := make([]string, 0, len(d.FieldErrors))
	for k := range d.FieldErrors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		groups = append(groups, k+": "+strings.Join(d.FieldErrors[k], "; "))
	}
	if len(groups) == 0 {
		return ""
	}
	if len(groups) > detailsMaxGroups {
		groups = append(groups[:detailsMaxGroups:detailsMaxGroups],
			fmt.Sprintf("(+%d more)", len(groups)-detailsMaxGroups))
	}
	for i, g := range groups {
		groups[i] = truncateRunes(sanitizeErrorText(g), detailsMaxRunes)
	}
	return ": " + strings.Join(groups, "; ")
}

// sanitizeErrorText strips terminal control characters (C0, DEL, and the C1
// range) from server-supplied text before it is inlined into an error
// string, so a hostile response cannot forge output lines or emit OSC/SGR
// escape sequences through validation messages.
func sanitizeErrorText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}

// truncateRunes shortens s to max runes, marking the cut with an ellipsis.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func (e *APIError) Error() string {
	suffix := e.detailsSuffix()
	switch {
	case e.Code != "" && e.RequestID != "":
		return fmt.Sprintf("%s (HTTP %d, code=%s, request-id=%s)%s", e.Message, e.Status, e.Code, e.RequestID, suffix)
	case e.Code != "":
		return fmt.Sprintf("%s (HTTP %d, code=%s)%s", e.Message, e.Status, e.Code, suffix)
	case e.RequestID != "":
		return fmt.Sprintf("%s (HTTP %d, request-id=%s)%s", e.Message, e.Status, e.RequestID, suffix)
	default:
		return fmt.Sprintf("%s (HTTP %d)%s", e.Message, e.Status, suffix)
	}
}

// TierLimit returns true when the error is a subscription tier limit (402).
func (e *APIError) TierLimit() bool { return e.Status == http.StatusPaymentRequired }

// RateLimited returns true when the API throttled the request (429). Public
// endpoints budgeted per client IP include the changelog subscribe/unsubscribe
// flows, the PUT /api/v1/public/apps/{appKey}/user attribute upsert, and —
// sharing that same 60/minute bucket — GET /api/v1/users/{userId}/profile.
func (e *APIError) RateLimited() bool { return e.Status == http.StatusTooManyRequests }

// tierLimitHints maps known 402 error codes to actionable remediation for
// CLI users and agents: submission endpoints (POST /api/v1/feature-requests,
// POST /api/v1/feedback) and workspace creation (POST /api/v1/console/
// workspaces, capped per developer account).
var tierLimitHints = map[string]string{
	"tier_limit_submissions":  "the workspace reached its monthly submission quota; upgrade the plan (cupthread billing show / Console → Billing) or wait for the quota to reset, then retry",
	"subscription_inactive":   "the workspace subscription is inactive or canceled; renew it in Console → Billing before submitting",
	"workspace_limit_reached": "the developer account already owns the maximum number of workspaces; delete one you own or transfer its ownership (Console → Workspaces), then retry — member/admin seats in other workspaces do not count toward the cap",
}

// forbiddenHints maps the AUTH-01 workspace RBAC 403 codes to actionable
// remediation: every /api/v1/console/workspaces/* route declares one
// capability checked against the caller's role, and members.manage,
// billing.manage, integration.manage, and changelog.publish (SEC-40:
// publishing or scheduling a changelog entry, admin/owner only)
// additionally reject cpt_ API tokens outright (interactive Clerk session
// required).
var forbiddenHints = map[string]string{
	"capability_required":          "your workspace role does not include the capability this action requires; ask a workspace admin or owner to perform it, or have an owner change your role (Console → Members)",
	"interactive_session_required": "this action rejects cpt_ API tokens; sign in interactively with 'cupthread auth login' (browser OAuth) or manage it in the Console web UI",
}

// Hint returns actionable remediation for known API error codes, e.g. 402
// tier-limit responses on submission endpoints, 429 throttling on public
// write endpoints, and 403 AUTH-01 workspace RBAC denials. It returns ""
// when there is no specific guidance.
func (e *APIError) Hint() string {
	if e.RateLimited() {
		return "too many requests from this client IP; wait before retrying and back off exponentially on repeated 429s"
	}
	if e.Forbidden() {
		return forbiddenHints[e.Code]
	}
	if !e.TierLimit() {
		return ""
	}
	if hint, ok := tierLimitHints[e.Code]; ok {
		return hint
	}
	return "check the workspace subscription and plan quotas (cupthread billing show / Console → Billing)"
}

// NotFound returns true when the API answered 404 for the targeted resource,
// e.g. a comment that does not exist in the workspace.
func (e *APIError) NotFound() bool { return e.Status == http.StatusNotFound }

// Forbidden returns true when the API rejected the caller's authorization
// (403): a workspace role missing the endpoint's capability
// (capability_required) or a cpt_ API token on an interactive-session-only
// endpoint (interactive_session_required).
func (e *APIError) Forbidden() bool { return e.Status == http.StatusForbidden }

// workspaceScopedPrefix marks paths that already carry the workspace id. For
// these the API treats the path id as authoritative: X-Workspace-Id is
// optional and rejected with 400 when it disagrees with the path.
const workspaceScopedPrefix = "/api/v1/console/workspaces/"

// Do performs an API request. Path must start with "/" and is appended to
// BaseURL verbatim. When body is non-nil it is JSON-encoded; when out is
// non-nil the response body is decoded into it (*json.RawMessage receives
// the undecoded bytes).
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	return c.DoWithHeaders(ctx, method, path, query, nil, body, out)
}

// DoWithHeaders performs an API request with optional extra request headers.
func (c *Client) DoWithHeaders(ctx context.Context, method, path string, query url.Values, headers map[string]string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// The path id is authoritative on workspace-scoped endpoints; the
	// redundant header is dropped so a mismatch can never trigger a 400.
	if c.WorkspaceID != "" && !strings.HasPrefix(path, workspaceScopedPrefix) {
		req.Header.Set("X-Workspace-Id", c.WorkspaceID)
	}
	if c.AppKey != "" {
		req.Header.Set("X-App-Key", c.AppKey)
	}
	if c.UserToken != "" {
		req.Header.Set("X-User-Token", c.UserToken)
	}
	// OPS-01: send a fresh correlation ID unless the caller supplied one; the
	// server echoes the valid value back so any error can be traced.
	if !hasRequestIDHeader(headers) {
		req.Header.Set(requestIDHeader, NewRequestID())
	}
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	if c.Token != nil {
		token, err := c.Token(ctx)
		if err != nil {
			return err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	if query != nil {
		req.URL.RawQuery = query.Encode()
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: read response: %w", method, path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{
			Status:  resp.StatusCode,
			Message: strings.TrimSpace(http.StatusText(resp.StatusCode)),
		}
		var parsed struct {
			Error   string          `json:"error"`
			Code    string          `json:"code"`
			Details json.RawMessage `json:"details"`
		}
		if json.Unmarshal(data, &parsed) == nil && parsed.Error != "" {
			apiErr.Message = parsed.Error
			apiErr.Code = parsed.Code
			apiErr.Details = parsed.Details
		}
		apiErr.RequestID = resp.Header.Get(requestIDHeader)
		if apiErr.TierLimit() {
			if hint := apiErr.Hint(); hint != "" {
				return fmt.Errorf("tier limit: %w — %s", apiErr, hint)
			}
			return fmt.Errorf("tier limit: %w", apiErr)
		}
		if apiErr.RateLimited() {
			return fmt.Errorf("rate limited: %w — %s", apiErr, apiErr.Hint())
		}
		if apiErr.Forbidden() {
			if hint := apiErr.Hint(); hint != "" {
				return fmt.Errorf("forbidden: %w — %s", apiErr, hint)
			}
			return apiErr
		}
		return apiErr
	}

	if out == nil {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = data
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", method, path, err)
	}
	return nil
}

// UploadAppIcon uploads an app icon through the console app-icon endpoint,
// which validates the image (SVG allowed for developer-configured icons,
// screened for active content) and updates the app record in the same
// request. It requires the app.configure capability (workspace admin or
// owner). The public feedback image endpoint cannot be used here: it expects
// an upload-session token rather than a console credential, and rejects SVG
// with 415.
func (c *Client) UploadAppIcon(ctx context.Context, workspaceID, appID, filename string, data []byte) (*AppRecord, error) {
	endpoint := fmt.Sprintf("/api/v1/console/workspaces/%s/apps/%s/icon",
		url.PathEscape(workspaceID), url.PathEscape(appID))
	var rec AppRecord
	if err := c.postMultipartFile(ctx, endpoint, filename, data, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// mimeTypeForFilename mirrors the server's extension→MIME fallback so the
// multipart part declares the type the magic-byte check expects; Go's
// CreateFormFile would label every part application/octet-stream, which the
// API rejects with 400 "Only PNG, JPEG, WebP, and GIF images are supported."
func mimeTypeForFilename(filename string) string {
	ext := strings.ToLower(filename)
	i := strings.LastIndexByte(ext, '.')
	if i < 0 {
		return "application/octet-stream"
	}
	switch ext[i+1:] {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

func escapeQuotes(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (c *Client) postMultipartFile(ctx context.Context, endpoint, filename string, data []byte, out any) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapeQuotes(filename)))
	header.Set("Content-Type", mimeTypeForFilename(filename))
	part, err := mw.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+endpoint, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set(requestIDHeader, NewRequestID())
	if c.Token != nil {
		token, err := c.Token(ctx)
		if err != nil {
			return err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("upload %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("upload %s: read response: %w", endpoint, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{
			Status:  resp.StatusCode,
			Message: strings.TrimSpace(string(body)),
		}
		var parsed struct {
			Error   string          `json:"error"`
			Code    string          `json:"code"`
			Details json.RawMessage `json:"details"`
		}
		if json.Unmarshal(body, &parsed) == nil && parsed.Error != "" {
			apiErr.Message = parsed.Error
			apiErr.Code = parsed.Code
			apiErr.Details = parsed.Details
		}
		apiErr.RequestID = resp.Header.Get(requestIDHeader)
		if resp.StatusCode == http.StatusUnsupportedMediaType {
			return fmt.Errorf("unsupported image type: %w", apiErr)
		}
		if apiErr.Forbidden() {
			if hint := apiErr.Hint(); hint != "" {
				return fmt.Errorf("forbidden: %w — %s", apiErr, hint)
			}
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("upload %s: decode response: %w", endpoint, err)
	}
	return nil
}
