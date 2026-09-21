package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// requestIDPattern mirrors the API's accepted correlation-ID charset and
// length (OPS-01): ^[A-Za-z0-9._-]{8,64}$.
var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// cliRequestIDPattern pins the CLI's own cli-<uuid v4> generator shape.
var cliRequestIDPattern = regexp.MustCompile(
	`^cli-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewRequestIDFormat(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		id := NewRequestID()
		if !cliRequestIDPattern.MatchString(id) {
			t.Fatalf("NewRequestID() = %q, want cli-<uuid v4>", id)
		}
		if !requestIDPattern.MatchString(id) {
			t.Fatalf("NewRequestID() = %q violates the API charset/length contract", id)
		}
		if seen[id] {
			t.Fatalf("NewRequestID() repeated %q across %d generations", id, i+1)
		}
		seen[id] = true
	}
}

func TestDoSendsRequestIDHeader(t *testing.T) {
	var ids []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, r.Header.Get("X-Request-Id"))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := New(server.URL)
	for i := 0; i < 2; i++ {
		if err := client.Do(context.Background(), "GET", "/x", nil, nil, nil); err != nil {
			t.Fatalf("Do: %v", err)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("got %d requests, want 2", len(ids))
	}
	for i, id := range ids {
		if !cliRequestIDPattern.MatchString(id) {
			t.Errorf("request %d X-Request-Id = %q, want cli-<uuid v4>", i, id)
		}
	}
	if ids[0] == ids[1] {
		t.Errorf("both requests reused request id %q; want a fresh ID per request", ids[0])
	}
}

func TestDoRespectsCallerRequestID(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Request-Id")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.DoWithHeaders(context.Background(), "GET", "/x", nil,
		map[string]string{"X-Request-Id": "agent-correlation-1"}, nil, nil)
	if err != nil {
		t.Fatalf("DoWithHeaders: %v", err)
	}
	if got != "agent-correlation-1" {
		t.Errorf("X-Request-Id = %q, want the caller-supplied value verbatim", got)
	}
}

func TestAPIErrorCarriesEchoedRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "srv-generated-uuid")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Comment not found in this workspace"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.RequestID != "srv-generated-uuid" {
		t.Errorf("RequestID = %q, want the echoed response header", apiErr.RequestID)
	}
	if !strings.Contains(apiErr.Error(), "request-id=srv-generated-uuid") {
		t.Errorf("Error() = %q, want it to quote the correlation ID", apiErr.Error())
	}
}

func TestUploadAppIconSendsAndEchoesRequestID(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Request-Id")
		w.Header().Set("X-Request-Id", got)
		w.WriteHeader(http.StatusUnsupportedMediaType)
		_, _ = w.Write([]byte(`{"error":"Only PNG, JPEG, WebP, and GIF images are supported.","code":"unsupported_media_type"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	_, err := client.UploadAppIcon(context.Background(), "ws_1", "app_1", "icon.png", []byte("not an image"))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if !cliRequestIDPattern.MatchString(got) {
		t.Errorf("multipart X-Request-Id = %q, want cli-<uuid v4>", got)
	}
	if apiErr.RequestID != got {
		t.Errorf("RequestID = %q, want the echoed %q", apiErr.RequestID, got)
	}
}

func TestDoSendsAuthAndWorkspaceHeaders(t *testing.T) {
	var gotAuth, gotWorkspace, gotPath, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotWorkspace = r.Header.Get("X-Workspace-Id")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := New(server.URL)
	client.WorkspaceID = "ws_1"
	client.Token = func(context.Context) (string, error) { return "cpt_tok", nil }

	var out map[string]any
	if err := client.Do(context.Background(), "GET", "/api/v1/console/me", map[string][]string{"q": {"x"}}, nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAuth != "Bearer cpt_tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotWorkspace != "ws_1" {
		t.Errorf("X-Workspace-Id = %q", gotWorkspace)
	}
	if gotPath != "/api/v1/console/me" || gotQuery != "q=x" {
		t.Errorf("path/query = %s?%s", gotPath, gotQuery)
	}
}

func TestDoEncodesJSONBody(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.Do(context.Background(), "POST", "/x", nil, map[string]string{"name": "app"}, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if body["name"] != "app" {
		t.Errorf("body = %v", body)
	}
}

func TestDoMapsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"Access denied: not a member of this workspace"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.Status != 403 || apiErr.Message != "Access denied: not a member of this workspace" {
		t.Errorf("apiErr = %+v", apiErr)
	}
}

func TestDoTierLimitFlag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"App limit reached","code":"tier_limit_apps"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.Do(context.Background(), "POST", "/x", nil, nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if !apiErr.TierLimit() {
		t.Errorf("TierLimit() = false for %+v", apiErr)
	}
	if apiErr.Code != "tier_limit_apps" {
		t.Errorf("code = %q", apiErr.Code)
	}
}

// TestDoFeatureRequestQuotaHints covers the 402 contract from issue #21:
// POST /api/v1/feature-requests (and POST /api/v1/feedback) respond with an
// ErrorResponse whose code distinguishes the monthly submission quota
// (tier_limit_submissions) from an inactive subscription
// (subscription_inactive). The wrapped error must stay an *APIError and carry
// an actionable hint.
func TestDoFeatureRequestQuotaHints(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantHint string
	}{
		{
			name:     "tier_limit_submissions",
			body:     `{"error":"Monthly submission quota reached","code":"tier_limit_submissions"}`,
			wantHint: "monthly submission quota",
		},
		{
			name:     "subscription_inactive",
			body:     `{"error":"Subscription inactive","code":"subscription_inactive"}`,
			wantHint: "inactive or canceled",
		},
		{
			name:     "unknown 402 code falls back to generic hint",
			body:     `{"error":"Plan limit","code":"tier_limit_other"}`,
			wantHint: "check the workspace subscription and plan quotas",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client := New(server.URL)
			err := client.Do(context.Background(), "POST", "/api/v1/feature-requests", nil, nil, nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected *APIError, got %v", err)
			}
			if apiErr.Status != http.StatusPaymentRequired {
				t.Errorf("status = %d", apiErr.Status)
			}
			if hint := apiErr.Hint(); !strings.Contains(hint, tc.wantHint) {
				t.Errorf("Hint() = %q, want it to contain %q", hint, tc.wantHint)
			}
			if hint := apiErr.Hint(); hint == "" {
				t.Error("Hint() is empty for a 402 response")
			}
			// The actionable hint must be part of the rendered error text.
			if !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("err = %q, want it to contain the hint %q", err, tc.wantHint)
			}
		})
	}
}

// TestHintEmptyForNonTierLimitErrors guards the Hint helper returning no
// guidance for ordinary errors.
func TestHintEmptyForNonTierLimitErrors(t *testing.T) {
	apiErr := &APIError{Status: http.StatusForbidden, Message: "Access denied"}
	if hint := apiErr.Hint(); hint != "" {
		t.Errorf("Hint() = %q for a non-402 error, want \"\"", hint)
	}
}

// TestDoWorkspaceLimitReachedHint covers the 402 contract from issue #18:
// POST /api/v1/console/workspaces responds with code workspace_limit_reached
// when the developer already owns the cap of workspaces. The hint must point
// at deleting/transferring an owned workspace, not at the subscription
// fallback used for unknown codes.
func TestDoWorkspaceLimitReachedHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"Workspace limit reached (maximum 3 workspaces per developer account)","code":"workspace_limit_reached"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.Do(context.Background(), "POST", "/api/v1/console/workspaces", nil, nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.Status != http.StatusPaymentRequired || apiErr.Code != "workspace_limit_reached" {
		t.Fatalf("apiErr = %+v", apiErr)
	}
	hint := apiErr.Hint()
	for _, want := range []string{"already owns the maximum number of workspaces", "delete one you own or transfer its ownership", "do not count toward the cap"} {
		if !strings.Contains(hint, want) {
			t.Errorf("Hint() = %q, want it to contain %q", hint, want)
		}
	}
	if strings.Contains(hint, "check the workspace subscription") {
		t.Errorf("Hint() = %q, want the workspace-specific guidance rather than the generic subscription fallback", hint)
	}
	if !strings.Contains(err.Error(), "already owns the maximum number of workspaces") {
		t.Errorf("err = %q, want the rendered error to carry the hint", err)
	}
}

// TestDoRateLimitHint covers the 429 contract from issue #14: public write
// endpoints (changelog subscribe/unsubscribe, PUT /user attribute upsert) are
// rate limited per client IP and respond with
// {"error":"Too many requests. Please try again shortly."}. The wrapped error
// must stay an *APIError and carry a backoff hint.
func TestDoRateLimitHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"Too many requests. Please try again shortly."}`))
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.Do(context.Background(), "PUT", "/api/v1/public/apps/app_key_123/user", nil, nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.Status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", apiErr.Status)
	}
	if apiErr.Message != "Too many requests. Please try again shortly." {
		t.Errorf("message = %q", apiErr.Message)
	}
	if !apiErr.RateLimited() {
		t.Errorf("RateLimited() = false for %+v", apiErr)
	}
	hint := apiErr.Hint()
	for _, want := range []string{"client IP", "back off exponentially"} {
		if !strings.Contains(hint, want) {
			t.Errorf("Hint() = %q, want it to contain %q", hint, want)
		}
	}
	if !strings.Contains(err.Error(), "rate limited") || !strings.Contains(err.Error(), "back off exponentially") {
		t.Errorf("err = %q, want the rendered error to carry the rate-limit hint", err)
	}
}

// TestHintEmptyForOrdinary4xxErrors guards the Hint helper returning no
// guidance for errors that are neither 402 tier limits nor 429 throttling,
// and that RateLimited only matches 429.
func TestHintEmptyForOrdinary4xxErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *APIError
	}{
		{"forbidden", &APIError{Status: http.StatusForbidden, Message: "Access denied"}},
		{"not-found", &APIError{Status: http.StatusNotFound, Message: "App not found"}},
		{"payment-required", &APIError{Status: http.StatusPaymentRequired, Message: "Monthly submission quota reached"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.RateLimited() {
				t.Errorf("RateLimited() = true for status %d", tc.err.Status)
			}
			if tc.err.Status != http.StatusPaymentRequired {
				if hint := tc.err.Hint(); hint != "" {
					t.Errorf("Hint() = %q for a plain %d error, want \"\"", hint, tc.err.Status)
				}
			}
		})
	}
}

func TestDoRawMessagePassthrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[1,2,3],"extra":"kept"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	var raw json.RawMessage
	if err := client.Do(context.Background(), "GET", "/x", nil, nil, &raw); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(raw) != `{"items":[1,2,3],"extra":"kept"}` {
		t.Errorf("raw = %s", raw)
	}
}

func TestDoTokenProviderErrorPropagates(t *testing.T) {
	client := New("http://127.0.0.1:1")
	client.Token = func(context.Context) (string, error) { return "", errors.New("not logged in") }
	err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
	if err == nil || err.Error() != "not logged in" {
		t.Fatalf("expected token provider error, got %v", err)
	}
}

func TestDoSendsClientIdentificationHeaders(t *testing.T) {
	var gotAppKey, gotUserToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAppKey = r.Header.Get("X-App-Key")
		gotUserToken = r.Header.Get("X-User-Token")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := New(server.URL)
	client.AppKey = "app_key_123"
	client.UserToken = "usr_tok_456"

	if err := client.Do(context.Background(), "GET", "/api/v1/feature-requests/req_1/comments", nil, nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAppKey != "app_key_123" {
		t.Errorf("X-App-Key = %q, want %q", gotAppKey, "app_key_123")
	}
	if gotUserToken != "usr_tok_456" {
		t.Errorf("X-User-Token = %q, want %q", gotUserToken, "usr_tok_456")
	}
}

func TestDoWithCustomHeaders(t *testing.T) {
	var gotCustom string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCustom = r.Header.Get("X-Custom-Header")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := New(server.URL)
	headers := map[string]string{"X-Custom-Header": "custom-value"}
	if err := client.DoWithHeaders(context.Background(), "GET", "/x", nil, headers, nil, nil); err != nil {
		t.Fatalf("DoWithHeaders: %v", err)
	}
	if gotCustom != "custom-value" {
		t.Errorf("X-Custom-Header = %q, want %q", gotCustom, "custom-value")
	}
}

func TestMimeTypeForFilename(t *testing.T) {
	cases := map[string]string{
		"icon.png":       "image/png",
		"icon.PNG":       "image/png",
		"photo.jpg":      "image/jpeg",
		"photo.jpeg":     "image/jpeg",
		"anim.gif":       "image/gif",
		"pic.webp":       "image/webp",
		"logo.svg":       "image/svg+xml",
		"archive.zip":    "application/octet-stream",
		"noextension":    "application/octet-stream",
		"trailing.dots.": "application/octet-stream",
	}
	for name, want := range cases {
		if got := mimeTypeForFilename(name); got != want {
			t.Errorf("mimeTypeForFilename(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestUploadAppIconHitsConsoleEndpoint verifies the icon upload targets the
// console app-icon endpoint (the public feedback image endpoint requires an
// upload-session token and rejects SVG), declares the part content type from
// the filename, and returns the updated app record.
func TestUploadAppIconHitsConsoleEndpoint(t *testing.T) {
	var gotPath, gotMethod, gotPartType, gotFilename string
	var gotBytes []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("file field: %v", err)
			return
		}
		defer file.Close()
		gotPartType = header.Header.Get("Content-Type")
		gotFilename = header.Filename
		gotBytes, _ = io.ReadAll(file)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"appId":"app_1","iconUrl":"https://cdn.example.com/icon.png"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	client.Token = func(context.Context) (string, error) { return "cpt_tok", nil }

	rec, err := client.UploadAppIcon(context.Background(), "ws_1", "app_1", "icon.png", []byte("png-bytes"))
	if err != nil {
		t.Fatalf("UploadAppIcon: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/console/workspaces/ws_1/apps/app_1/icon" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotPartType != "image/png" || gotFilename != "icon.png" {
		t.Errorf("part content-type/filename = %q/%q", gotPartType, gotFilename)
	}
	if string(gotBytes) != "png-bytes" {
		t.Errorf("part body = %q", gotBytes)
	}
	if rec.IconURL == nil || *rec.IconURL != "https://cdn.example.com/icon.png" {
		t.Errorf("IconURL = %v", rec.IconURL)
	}
}

// TestUploadAppIcon415Message pins the SEC-13 rejection contract: the 415
// bodies carry only an error message (no code), and the CLI surfaces them as
// an "unsupported image type" failure.
func TestUploadAppIcon415Message(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnsupportedMediaType)
		_, _ = w.Write([]byte(`{"error":"SVG images are not supported. Upload a PNG, JPEG, WebP, or GIF image."}`))
	}))
	defer server.Close()

	client := New(server.URL)
	_, err := client.UploadAppIcon(context.Background(), "ws_1", "app_1", "icon.svg", []byte("<svg/>"))
	if err == nil {
		t.Fatal("UploadAppIcon: want error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnsupportedMediaType {
		t.Fatalf("error = %v, want *APIError with status 415", err)
	}
	if apiErr.Code != "" {
		t.Errorf("Code = %q, want empty (415 bodies carry no code)", apiErr.Code)
	}
	if !strings.Contains(err.Error(), "unsupported image type") ||
		!strings.Contains(err.Error(), "SVG images are not supported") {
		t.Errorf("error = %v, want unsupported-image-type prefix plus server message", err)
	}
}

// TestUploadAppIconOtherErrorsKeptRaw verifies non-415 upload failures are
// not relabeled: the JSON error body is parsed into Message/Code untouched.
func TestUploadAppIconOtherErrorsKeptRaw(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"Access denied: your workspace role does not include the 'app.configure' capability","code":"capability_required"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	_, err := client.UploadAppIcon(context.Background(), "ws_1", "app_1", "icon.png", []byte("png"))
	if err == nil {
		t.Fatal("UploadAppIcon: want error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("error = %v, want *APIError with status 403", err)
	}
	if apiErr.Code != "capability_required" {
		t.Errorf("Code = %q, want capability_required", apiErr.Code)
	}
	if strings.Contains(err.Error(), "unsupported image type") {
		t.Errorf("error = %v, want no unsupported-image-type relabeling on 403", err)
	}
}

// TestDoDropsWorkspaceHeaderOnWorkspaceScopedPaths covers the issue #3 header
// semantics: on /api/v1/console/workspaces/{id}/... routes the path id is
// authoritative, so the client must not send X-Workspace-Id — a stale or
// mismatched value would now be rejected with 400.
func TestDoDropsWorkspaceHeaderOnWorkspaceScopedPaths(t *testing.T) {
	cases := []struct {
		path        string
		wantHeader  bool
		description string
	}{
		{path: "/api/v1/console/workspaces/ws_1/feature-requests/fr_1/comments", wantHeader: false, description: "moderation list"},
		{path: "/api/v1/console/workspaces/ws_1/comments/c_1/hide", wantHeader: false, description: "comment hide"},
		{path: "/api/v1/console/workspaces/ws_1/comments/c_1", wantHeader: false, description: "comment delete"},
		{path: "/api/v1/console/me", wantHeader: true, description: "identity endpoint keeps the header"},
		{path: "/api/v1/feature-requests/fr_1/comments", wantHeader: true, description: "public endpoints keep the header"},
	}
	for _, tc := range cases {
		t.Run(tc.description, func(t *testing.T) {
			var gotWorkspace string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotWorkspace = r.Header.Get("X-Workspace-Id")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()

			client := New(server.URL)
			client.WorkspaceID = "ws_1"
			if err := client.Do(context.Background(), "GET", tc.path, nil, nil, nil); err != nil {
				t.Fatalf("Do: %v", err)
			}
			if tc.wantHeader && gotWorkspace != "ws_1" {
				t.Errorf("X-Workspace-Id = %q, want %q", gotWorkspace, "ws_1")
			}
			if !tc.wantHeader && gotWorkspace != "" {
				t.Errorf("X-Workspace-Id = %q, want no header on workspace-scoped path", gotWorkspace)
			}
		})
	}
}

func TestAPIErrorNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Comment not found in this workspace"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.Do(context.Background(), "DELETE", "/x", nil, nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if !apiErr.NotFound() {
		t.Errorf("NotFound() = false for %+v", apiErr)
	}
	if apiErr.Message != "Comment not found in this workspace" {
		t.Errorf("message = %q", apiErr.Message)
	}
}

func TestForbiddenCapabilityRequiredHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"Access denied: your workspace role does not include the 'members.manage' capability","code":"capability_required"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.Do(context.Background(), "POST", "/api/v1/console/workspaces/ws_1/members", nil,
		map[string]string{"email": "dev@example.com", "role": "member"}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{
		"capability_required",
		"does not include the 'members.manage' capability",
		"ask a workspace admin or owner to perform it",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestForbiddenInteractiveSessionRequiredHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"This action requires an interactive session; API tokens are not permitted","code":"interactive_session_required"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.Do(context.Background(), "POST", "/api/v1/console/workspaces/ws_1/billing/checkout", nil,
		map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{
		"interactive_session_required",
		"API tokens are not permitted",
		"cupthread auth login",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestForbiddenUnknownCodeUntouched(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"Token management requires an interactive session"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.Do(context.Background(), "POST", "/api/v1/console/tokens", nil, map[string]string{"name": "k"}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if err.Error() != "Token management requires an interactive session (HTTP 403)" {
		t.Errorf("error = %q, want the plain APIError without a forbidden hint", err)
	}
}

func TestHintForbiddenCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		want   string
	}{
		{"capability_required", http.StatusForbidden, "capability_required", "workspace admin or owner"},
		{"interactive_session_required", http.StatusForbidden, "interactive_session_required", "cupthread auth login"},
		{"unknown 403 code", http.StatusForbidden, "some_future_code", ""},
		{"403 without code", http.StatusForbidden, "", ""},
		{"hint does not leak across statuses", http.StatusPaymentRequired, "capability_required", "check the workspace subscription"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &APIError{Status: tc.status, Message: "x", Code: tc.code}
			if got := e.Hint(); !strings.Contains(got, tc.want) {
				t.Errorf("Hint() = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestUploadAppIconForbiddenHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/console/workspaces/ws_1/apps/app_1/icon" {
			t.Errorf("request path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"Access denied: your workspace role does not include the 'app.configure' capability","code":"capability_required"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	_, err := client.UploadAppIcon(context.Background(), "ws_1", "app_1", "icon.png", []byte("png-bytes"))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"forbidden:", "app.configure", "ask a workspace admin or owner to perform it"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// ---- Transient-failure retry suite (issue #71) ----

// sleepRecorder captures the delays the retry loop requests so tests run
// instantly and can assert exact backoff values.
type sleepRecorder struct{ waits []time.Duration }

func (s *sleepRecorder) sleep(d time.Duration) { s.waits = append(s.waits, d) }

// retryTestClient builds a client against server whose waits are recorded
// instead of slept.
func retryTestClient(server *httptest.Server) (*Client, *sleepRecorder) {
	rec := &sleepRecorder{}
	client := New(server.URL)
	client.Sleeper = rec.sleep
	return client, rec
}

// TestRetryGetThenSuccess pins the core contract: a 429 on a body-less GET
// is retried once and the command succeeds; exactly one backoff wait within
// the first window (≤ DefaultRetryBaseDelay, full jitter) is requested, and
// the decoded payload is the second attempt's body.
func TestRetryGetThenSuccess(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"Too many requests. Please try again shortly."}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client, rec := retryTestClient(server)
	var out struct {
		OK bool `json:"ok"`
	}
	if err := client.Do(context.Background(), "GET", "/x", nil, nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !out.OK {
		t.Errorf("ok = false, want the retry's decoded body")
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2 (429 then success)", got)
	}
	if len(rec.waits) != 1 {
		t.Fatalf("sleeps = %d (%v), want exactly 1", len(rec.waits), rec.waits)
	}
	if rec.waits[0] < 0 || rec.waits[0] > DefaultRetryBaseDelay {
		t.Errorf("first wait = %v, want a full-jitter draw in [0, %v]", rec.waits[0], DefaultRetryBaseDelay)
	}
}

// TestRetryReusesCallerRequestID pins the `api request` observability
// contract: a caller-supplied correlation ID (one per invocation) rides
// every attempt, so the echoed ID on the final error traces the whole
// retry chain.
func TestRetryReusesCallerRequestID(t *testing.T) {
	var ids []string
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, r.Header.Get("X-Request-Id"))
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client, _ := retryTestClient(server)
	err := client.DoWithHeaders(context.Background(), "GET", "/x", nil,
		map[string]string{"X-Request-Id": "agent-correlation-1"}, nil, nil)
	if err != nil {
		t.Fatalf("DoWithHeaders: %v", err)
	}
	if len(ids) != 3 || ids[0] != "agent-correlation-1" || ids[1] != "agent-correlation-1" || ids[2] != "agent-correlation-1" {
		t.Errorf("request ids = %v, want the caller-supplied id on every attempt", ids)
	}
}

// TestRetryExhaustedAlways429 pins the exhaustion contract: 1 initial
// attempt + 3 retries, three backoff waits, and the final error keeps the
// rate-limited wrapping and hint while stamping the attempt count.
func TestRetryExhaustedAlways429(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"Too many requests. Please try again shortly."}`))
	}))
	defer server.Close()

	client, rec := retryTestClient(server)
	err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
	if err == nil {
		t.Fatal("expected the always-429 request to fail")
	}
	if got := hits.Load(); got != 4 {
		t.Errorf("attempts = %d, want 4 (1 + DefaultMaxRetries)", got)
	}
	if len(rec.waits) != 3 {
		t.Fatalf("sleeps = %d (%v), want 3", len(rec.waits), rec.waits)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.Attempts != 4 {
		t.Errorf("APIError.Attempts = %d, want 4", apiErr.Attempts)
	}
	for _, want := range []string{"rate limited", "back off exponentially"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// TestRetryExhaustedAlways503 proves the 5xx transient class (gateway/
// overload) gets the same retry budget as throttling.
func TestRetryExhaustedAlways503(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"upstream unavailable"}`))
	}))
	defer server.Close()

	client, rec := retryTestClient(server)
	err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
	if err == nil {
		t.Fatal("expected the always-503 request to fail")
	}
	if got := hits.Load(); got != 4 {
		t.Errorf("attempts = %d, want 4", got)
	}
	if len(rec.waits) != 3 {
		t.Fatalf("sleeps = %d (%v), want 3", len(rec.waits), rec.waits)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.Attempts != 4 || apiErr.Status != http.StatusServiceUnavailable {
		t.Errorf("apiErr = %+v, want a 503 with Attempts=4", apiErr)
	}
}

// TestMutationNeverRetried pins the safety scope: a POST carrying a body is
// a single wire attempt even on a retryable status — replaying mutations
// could double-apply them.
func TestMutationNeverRetried(t *testing.T) {
	var hits atomic.Int32
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"upstream unavailable"}`))
	}))
	defer server.Close()

	client, rec := retryTestClient(server)
	err := client.Do(context.Background(), "POST", "/api/v1/feature-requests", nil, map[string]any{"appKey": "k"}, nil)
	if err == nil {
		t.Fatal("expected the always-503 POST to fail")
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("attempts = %d, want exactly 1 for a mutation", got)
	}
	if len(rec.waits) != 0 {
		t.Errorf("sleeps = %v, want none for a mutation", rec.waits)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Attempts != 1 {
		t.Errorf("apiErr = %+v, want Attempts=1", apiErr)
	}
}

// TestOnlyIdempotentMethodsRetried sweeps the verb matrix: GET and HEAD
// retry, every mutation verb stays single-shot.
func TestOnlyIdempotentMethodsRetried(t *testing.T) {
	for _, tc := range []struct {
		method string
		want   int32
	}{
		{"GET", 4},
		{"HEAD", 4},
		{"PUT", 1},
		{"PATCH", 1},
		{"DELETE", 1},
	} {
		t.Run(tc.method, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer server.Close()

			client, rec := retryTestClient(server)
			_ = client.Do(context.Background(), tc.method, "/x", nil, nil, nil)
			if got := hits.Load(); got != tc.want {
				t.Errorf("%s attempts = %d, want %d", tc.method, got, tc.want)
			}
			wantSleeps := int(tc.want) - 1
			if len(rec.waits) != wantSleeps {
				t.Errorf("%s sleeps = %d, want %d", tc.method, len(rec.waits), wantSleeps)
			}
		})
	}
}

// TestRetryAfterSecondsHonored: when the server supplies Retry-After the
// client waits exactly that long (no jitter) — here 1 s.
func TestRetryAfterSecondsHonored(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client, rec := retryTestClient(server)
	if err := client.Do(context.Background(), "GET", "/x", nil, nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(rec.waits) != 1 || rec.waits[0] != time.Second {
		t.Errorf("waits = %v, want exactly [1s] from Retry-After", rec.waits)
	}
}

// TestRetryAfterCappedAtMaximum: an absurd Retry-After is clamped to
// DefaultRetryMaxDelay so a server cannot stall the CLI for minutes.
func TestRetryAfterCappedAtMaximum(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "9999")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client, rec := retryTestClient(server)
	if err := client.Do(context.Background(), "GET", "/x", nil, nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(rec.waits) != 1 || rec.waits[0] != DefaultRetryMaxDelay {
		t.Errorf("waits = %v, want the %v cap", rec.waits, DefaultRetryMaxDelay)
	}
}

// TestNoRetryFieldSingleShot: Client.NoRetry restores exact single-shot
// semantics — one wire attempt, no waits.
func TestNoRetryFieldSingleShot(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client, rec := retryTestClient(server)
	client.NoRetry = true
	err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
	if err == nil {
		t.Fatal("expected the always-429 request to fail")
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("attempts = %d, want exactly 1 with NoRetry", got)
	}
	if len(rec.waits) != 0 {
		t.Errorf("sleeps = %v, want none with NoRetry", rec.waits)
	}
}

// TestRetryNoticeGoesToStderr: each retry writes one human-readable line to
// Stderr (never stdout) and a nil Stderr — structured mode — stays silent.
func TestRetryNoticeGoesToStderr(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client, _ := retryTestClient(server)
	var buf bytes.Buffer
	client.Stderr = &buf
	if err := client.Do(context.Background(), "GET", "/items", nil, nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	line := buf.String()
	for _, want := range []string{"GET /items got HTTP 429", "retrying (attempt 2/4", "waiting 0s"} {
		if !strings.Contains(line, want) {
			t.Errorf("stderr notice %q missing %q", line, want)
		}
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("stderr notice = %q, want exactly one line", line)
	}
}

// TestRetryAbortsOnCanceledContext: Ctrl-C during a backoff wait aborts the
// command instead of finishing the sleep first — one wire attempt, then a
// context-canceled error.
func TestRetryAbortsOnCanceledContext(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := New(server.URL)
	client.RetryBaseDelay = 30 * time.Second // real sleep path, canceled mid-wait
	go func() {
		// Let the first attempt land and the backoff sleep begin.
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := client.Do(ctx, "GET", "/x", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("err = %v, want a context-canceled failure", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (no retry after cancellation)", got)
	}
}

// TestBackoffDelayFullJitterBounds: computed waits are full-jitter draws —
// uniformly bounded by min(base·2^attempt, max), never exceeding the cap
// even for large attempt numbers.
func TestBackoffDelayFullJitterBounds(t *testing.T) {
	client := &Client{RetryBaseDelay: 10 * time.Millisecond, RetryMaxDelay: 40 * time.Millisecond}
	for attempt := 0; attempt < 10; attempt++ {
		ceiling := 10 * time.Millisecond << attempt
		if ceiling > 40*time.Millisecond {
			ceiling = 40 * time.Millisecond
		}
		for i := 0; i < 200; i++ {
			d := client.backoffDelay(attempt)
			if d < 0 || d > ceiling {
				t.Fatalf("backoffDelay(%d) = %v, want a draw in [0, %v]", attempt, d, ceiling)
			}
		}
	}
	// A max below the base clamps every window immediately.
	clamped := &Client{RetryBaseDelay: time.Second, RetryMaxDelay: 5 * time.Millisecond}
	for i := 0; i < 200; i++ {
		if d := clamped.backoffDelay(3); d > 5*time.Millisecond {
			t.Fatalf("backoffDelay(3) = %v, want ≤ 5ms under the tiny cap", d)
		}
	}
}

// TestParseRetryAfter covers the header grammar the client accepts:
// delay-seconds (trimmed, non-negative) and HTTP-date, with absent,
// negative, and garbage values rejected.
func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	future := now.Add(90 * time.Second).UTC().Format(http.TimeFormat)
	for _, tc := range []struct {
		name    string
		value   string
		wantOK  bool
		wantMin time.Duration
		wantMax time.Duration
	}{
		{"absent", "", false, 0, 0},
		{"seconds", "120", true, 120 * time.Second, 120 * time.Second},
		{"zero", "0", true, 0, 0},
		{"negative", "-5", false, 0, 0},
		{"padded", " 90 ", true, 90 * time.Second, 90 * time.Second},
		{"garbage", "soon", false, 0, 0},
		{"http-date future", future, true, 88 * time.Second, 91 * time.Second},
		{"http-date past", now.Add(-time.Hour).Format(http.TimeFormat), true, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := parseRetryAfter(tc.value, now)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && (d < tc.wantMin || d > tc.wantMax) {
				t.Errorf("delay = %v, want within [%v, %v]", d, tc.wantMin, tc.wantMax)
			}
		})
	}
}
