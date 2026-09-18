package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
