package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
