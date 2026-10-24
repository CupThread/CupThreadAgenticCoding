package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostileRequestIDEchoSanitizedThroughCommands pins the issue #288
// end-to-end contract: a hostile X-Request-Id response header — the
// UTF-8-encoded C1 form (U+009D OSC, U+009C ST, U+009B CSI), which is legal
// obs-text and passes the transport — must never reach the terminal through
// the error line cobra prints on every typed command, nor the structured
// `api request` payload. The dropped echo renders the error line without a
// request-id segment and omits requestId from the structured document.
func TestHostileRequestIDEchoSanitizedThroughCommands(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	osc, st, csi := "\u009d", "\u009c", "\u009b"
	hostile := osc + "8;;https://evil.example" + st + "CLICK" + csi + "31mRED" + st

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", hostile)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Not found","code":"not_found"}`))
	}))
	defer server.Close()

	t.Run("workspaces list renders no control bytes", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		_, stderr, err := runRootCapture(t, cfgPath, server.URL, "workspaces", "list")
		if err == nil {
			t.Fatal("expected the 404 to fail the command")
		}
		assertNoControlBytesStrict(t, stderr)
		if strings.Contains(stderr, "request-id=") {
			t.Errorf("stderr = %q, want no request-id segment for a dropped echo", stderr)
		}
	})

	t.Run("api request --json omits requestId", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
			"api", "request", "GET", "/api/v1/console/me", "--json")
		if err == nil {
			t.Fatal("expected the 404 to fail the command")
		}
		assertNoControlBytesStrict(t, stdout)
		assertNoControlBytesStrict(t, stderr)

		var payload map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
			t.Fatalf("decode structured payload %q: %v", stdout, err)
		}
		if _, ok := payload["requestId"]; ok {
			t.Errorf("payload carries requestId %v, want it omitted for a dropped echo", payload["requestId"])
		}
		if payload["code"] != "not_found" {
			t.Errorf("payload code = %v, want not_found", payload["code"])
		}
	})
}

// TestLegitimateRequestIDEchoSurvivesThroughCommands guards the correlation
// half of the issue #288 contract: a conforming server echo (the OPS-01
// shape) still round-trips into the structured payload verbatim, so OPS-01
// correlation does not regress.
func TestLegitimateRequestIDEchoSurvivesThroughCommands(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	const legit = "srv-generated-uuid"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", legit)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Not found","code":"not_found"}`))
	}))
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL,
		"api", "request", "GET", "/api/v1/console/me", "--json")
	if err == nil {
		t.Fatal("expected the 404 to fail the command")
	}
	assertNoControlBytesStrict(t, stdout)
	assertNoControlBytesStrict(t, stderr)

	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
		t.Fatalf("decode structured payload %q: %v", stdout, err)
	}
	if got, _ := payload["requestId"].(string); got != legit {
		t.Errorf("payload requestId = %q, want the conforming echo %q byte-identical", got, legit)
	}
	if !strings.Contains(stderr, "request-id="+legit) {
		t.Errorf("stderr = %q, want it to quote the correlation ID", stderr)
	}
}
