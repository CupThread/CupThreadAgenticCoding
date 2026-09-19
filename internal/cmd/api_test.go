package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAPIRequestSurfacesQuotaHint covers issue #21: when the API rejects a
// submission with 402 (POST /api/v1/feature-requests quota contract), the
// `api request --json` escape hatch must surface the machine-readable code
// plus an actionable hint so agents can react without guessing.
func TestAPIRequestSurfacesQuotaHint(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/feature-requests" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"Monthly submission quota reached","code":"tier_limit_submissions"}`))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "api", "request", "POST", "/api/v1/feature-requests", "--json")
	if err != nil {
		t.Fatalf("api request: %v", err)
	}
	var payload struct {
		Error  string `json:"error"`
		Code   string `json:"code"`
		Status int    `json:"status"`
		Hint   string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("decode structured output %q: %v", out, err)
	}
	if payload.Status != http.StatusPaymentRequired || payload.Code != "tier_limit_submissions" {
		t.Errorf("payload = %+v", payload)
	}
	if payload.Error != "Monthly submission quota reached" {
		t.Errorf("error = %q", payload.Error)
	}
	if !strings.Contains(payload.Hint, "monthly submission quota") {
		t.Errorf("hint = %q, want it to mention the submission quota", payload.Hint)
	}
}

// TestAPIRequestQuotesRequestIDOnSuccess covers issue #6: every invocation
// sends an X-Request-Id correlation header and the table output quotes the
// exact value the server received, so it can be cited in support flows.
func TestAPIRequestQuotesRequestIDOnSuccess(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Request-Id")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "api", "request", "GET", "/api/v1/console/me")
	if err != nil {
		t.Fatalf("api request: %v", err)
	}
	if !strings.Contains(out, "request-id "+got) {
		t.Errorf("output = %q, want it to quote the wire request id %q", out, got)
	}
	if !strings.Contains(got, "cli-") {
		t.Errorf("wire X-Request-Id = %q, want the CLI-generated cli-<uuid> form", got)
	}
}

// TestAPIRequestJSONErrorIncludesRequestID covers issue #6: the structured
// error payload of the escape hatch carries the correlation ID so agents can
// file reproducible bug reports.
func TestAPIRequestJSONErrorIncludesRequestID(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Request-Id")
		w.Header().Set("X-Request-Id", got)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"App not found"}`))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "api", "request", "GET", "/api/v1/x", "--json")
	if err != nil {
		t.Fatalf("api request: %v", err)
	}
	var payload struct {
		Error     string `json:"error"`
		Status    int    `json:"status"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("decode structured output %q: %v", out, err)
	}
	if payload.Status != http.StatusNotFound || payload.Error != "App not found" {
		t.Errorf("payload = %+v", payload)
	}
	if payload.RequestID != got {
		t.Errorf("requestId = %q, want the echoed %q", payload.RequestID, got)
	}
}
