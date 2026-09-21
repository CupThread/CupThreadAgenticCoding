package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
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

// TestDoSendsRawJSONBodyVerbatim covers issue #75: a json.RawMessage (or
// *json.RawMessage) request body must reach the server byte-identical —
// re-encoding through json.Marshal would round-trip every number through
// float64 and silently rewrite integers a float64 cannot represent exactly.
// An empty RawMessage means "no body"; plain values keep going through
// json.Marshal.
func TestDoSendsRawJSONBodyVerbatim(t *testing.T) {
	var gotBody, gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotContentType = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := New(server.URL)
	const body = `{"big":12345678901234567890, "pad": [1, 2]}`
	if err := client.Do(context.Background(), "POST", "/x", nil, json.RawMessage(body), nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotBody != body {
		t.Errorf("wire body = %q, want the RawMessage bytes %q verbatim", gotBody, body)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}

	rawPtr := json.RawMessage(body)
	if err := client.Do(context.Background(), "POST", "/x", nil, &rawPtr, nil); err != nil {
		t.Fatalf("Do with *json.RawMessage: %v", err)
	}
	if gotBody != body {
		t.Errorf("*json.RawMessage wire body = %q, want %q verbatim", gotBody, body)
	}

	if err := client.Do(context.Background(), "POST", "/x", nil, json.RawMessage{}, nil); err != nil {
		t.Fatalf("Do with empty RawMessage: %v", err)
	}
	if gotBody != "" || gotContentType != "" {
		t.Errorf("empty RawMessage sent body %q (Content-Type %q), want no body at all", gotBody, gotContentType)
	}

	if err := client.Do(context.Background(), "POST", "/x", nil, map[string]string{"name": "app"}, nil); err != nil {
		t.Fatalf("Do with a plain map: %v", err)
	}
	if gotBody != `{"name":"app"}` {
		t.Errorf("plain map wire body = %q, want the marshaled form", gotBody)
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
