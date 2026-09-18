package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
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

// APIError is a non-2xx API response.
type APIError struct {
	Status  int
	Message string
	Code    string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s (HTTP %d, code=%s)", e.Message, e.Status, e.Code)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// TierLimit returns true when the error is a subscription tier limit (402).
func (e *APIError) TierLimit() bool { return e.Status == http.StatusPaymentRequired }

// RateLimited returns true when the API throttled the request (429). Public
// write endpoints are budgeted per client IP: changelog subscribe/unsubscribe
// and the PUT /api/v1/public/apps/{appKey}/user attribute upsert.
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

// Hint returns actionable remediation for known API error codes, e.g. 402
// tier-limit responses on submission endpoints and 429 throttling on public
// write endpoints. It returns "" when there is no specific guidance.
func (e *APIError) Hint() string {
	if e.RateLimited() {
		return "too many requests from this client IP; wait before retrying and back off exponentially on repeated 429s"
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
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if json.Unmarshal(data, &parsed) == nil && parsed.Error != "" {
			apiErr.Message = parsed.Error
			apiErr.Code = parsed.Code
		}
		if apiErr.TierLimit() {
			if hint := apiErr.Hint(); hint != "" {
				return fmt.Errorf("tier limit: %w — %s", apiErr, hint)
			}
			return fmt.Errorf("tier limit: %w", apiErr)
		}
		if apiErr.RateLimited() {
			return fmt.Errorf("rate limited: %w — %s", apiErr, apiErr.Hint())
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
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if json.Unmarshal(body, &parsed) == nil && parsed.Error != "" {
			apiErr.Message = parsed.Error
			apiErr.Code = parsed.Code
		}
		if resp.StatusCode == http.StatusUnsupportedMediaType {
			return fmt.Errorf("unsupported image type: %w", apiErr)
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
