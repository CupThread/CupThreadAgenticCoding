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
