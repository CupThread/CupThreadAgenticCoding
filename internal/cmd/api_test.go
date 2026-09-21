package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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

// runRootWithStdin executes the CLI with stdin replaced by content, covering
// the "-" / "@" readInputFile path.
func runRootWithStdin(t *testing.T, serverURL, content string, args ...string) (string, error) {
	t.Helper()

	f, err := os.Create(filepath.Join(t.TempDir(), "stdin"))
	if err != nil {
		t.Fatalf("create stdin file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write stdin file: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("rewind stdin file: %v", err)
	}
	old := os.Stdin
	os.Stdin = f
	defer func() {
		os.Stdin = old
		if err := f.Close(); err != nil {
			t.Errorf("close stdin file: %v", err)
		}
	}()
	return runRoot(t, serverURL, args...)
}

// writeInputFile writes content to a temp file and returns its path.
func writeInputFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write input file: %v", err)
	}
	return path
}

// TestAPIRequestInputRejectsTrailingData covers issue #87: `api request
// --input` previously decoded only the first JSON value and silently dropped
// everything after it, sending a truncated body. Trailing values or prose must
// now fail the command before any request is issued; a whitespace-only tail
// stays accepted.
func TestAPIRequestInputRejectsTrailingData(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	cases := []struct {
		name    string
		content string
		stdin   bool
		wantErr string
		wantHit bool
	}{
		{name: "file two top-level values", content: `{"title":"A"}` + "\n" + `{"title":"B"}` + "\n", wantErr: "trailing data", wantHit: false},
		{name: "file trailing prose", content: `{"title":"A"} oops`, wantErr: "trailing data", wantHit: false},
		{name: "file whitespace tail ok", content: "{\"title\":\"A\"}\n\n", wantErr: "", wantHit: true},
		{name: "stdin two top-level values", content: `{"title":"A"}` + "\n" + `{"title":"B"}` + "\n", stdin: true, wantErr: "trailing data", wantHit: false},
		{name: "stdin trailing prose", content: `{"title":"A"} oops`, stdin: true, wantErr: "trailing data", wantHit: false},
		{name: "stdin whitespace tail ok", content: "{\"title\":\"A\"}\n\n", stdin: true, wantErr: "", wantHit: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			var gotBody string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				body, _ := io.ReadAll(r.Body)
				gotBody = string(body)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()

			args := []string{"api", "request", "POST", "/api/v1/feature-requests"}
			if tc.stdin {
				args = append(args, "--input", "-")
			}
			var err error
			if tc.stdin {
				_, err = runRootWithStdin(t, server.URL, tc.content, append(args, "--json")...)
			} else {
				_, err = runRoot(t, server.URL, append(args, "--input", writeInputFile(t, tc.content), "--json")...)
			}

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("api request: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("api request succeeded, want trailing-data error")
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), tc.wantErr)
				}
			}
			if tc.wantHit {
				if hits != 1 {
					t.Fatalf("server hits = %d, want 1", hits)
				}
				if gotBody != `{"title":"A"}` {
					t.Errorf("wire body = %q, want the single JSON value", gotBody)
				}
			} else if hits != 0 {
				t.Errorf("server hits = %d, want 0: a malformed input file must fail before any request", hits)
			}
		})
	}
}

// TestSettingsSetInputRejectsTrailingData pins the issue #87 parity guard:
// `apps settings set --input` has always been strict (json.Unmarshal rejects
// trailing data) — the strictness split must never silently flip.
func TestSettingsSetInputRejectsTrailingData(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apps":[{"appId":"app_1","appKey":"key_1","slug":"app-one","name":"App One"}]}`))
	}))
	defer server.Close()

	_, err := runRoot(t, server.URL, "apps", "settings", "set", "app_1",
		"--workspace", "ws_1", "--input", writeInputFile(t, `{"allowAnonymousVote":false}{"allowAnonymousVote":true}`))
	if err == nil {
		t.Fatal("apps settings set succeeded with trailing data, want a parse error")
	}
	if !strings.Contains(err.Error(), "parse settings JSON") {
		t.Errorf("error = %q, want it to mention the settings JSON parse failure", err.Error())
	}
}

// TestImportsCreateOptionsRejectTrailingData pins the issue #87 parity guard
// for `imports create --options`, the other strict json.Unmarshal consumer.
func TestImportsCreateOptionsRejectTrailingData(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		_, _ = w.Write([]byte(`{"job":{"id":"job_1"}}`))
	}))
	defer server.Close()

	_, err := runRoot(t, server.URL, "imports", "create",
		"--workspace", "ws_1", "--app", "app_1", "--source", "github_issues",
		"--options", writeInputFile(t, `{"owner":"a"}{"owner":"b"}`))
	if err == nil {
		t.Fatal("imports create succeeded with trailing data, want a parse error")
	}
	if !strings.Contains(err.Error(), "parse options JSON") {
		t.Errorf("error = %q, want it to mention the options JSON parse failure", err.Error())
	}
}

// TestAPIRequestJSONErrorIncludesValidationDetails covers issue #73: when the
// API answers 400 with the zod-flatten details object, `api request --json`
// must pass it through verbatim so programmatic consumers see exactly what
// the server sent.
func TestAPIRequestJSONErrorIncludesValidationDetails(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	serverDetails := `{"formErrors":[],"fieldErrors":{"title":["Title must be at least 3 characters"],"versionId":["Invalid version"]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Validation failed","details":` + serverDetails + `}`))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "api", "request", "POST", "/api/v1/x", "--json")
	if err != nil {
		t.Fatalf("api request: %v", err)
	}
	var payload struct {
		Error   string          `json:"error"`
		Status  int             `json:"status"`
		Details json.RawMessage `json:"details"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("decode structured output %q: %v", out, err)
	}
	if payload.Status != http.StatusBadRequest || payload.Error != "Validation failed" {
		t.Errorf("payload = %+v", payload)
	}
	// The structured writer pretty-prints the whole payload (re-indenting
	// raw JSON without re-parsing it), so compare the details semantically:
	// it must carry exactly the server's keys and values.
	var gotDetails, wantDetails any
	if err := json.Unmarshal(payload.Details, &gotDetails); err != nil {
		t.Fatalf("decode details %s: %v", payload.Details, err)
	}
	if err := json.Unmarshal([]byte(serverDetails), &wantDetails); err != nil {
		t.Fatalf("decode expected details: %v", err)
	}
	if !reflect.DeepEqual(gotDetails, wantDetails) {
		t.Errorf("details = %s, want the server's object %s", payload.Details, serverDetails)
	}
}

// TestAPIRequestJSONErrorOmitsEmptyDetails pins the other half of issue #73's
// contract: when the server sends no `details`, the structured error payload
// must not grow a null/empty details key.
func TestAPIRequestJSONErrorOmitsEmptyDetails(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Validation failed"}`))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "api", "request", "POST", "/api/v1/x", "--json")
	if err != nil {
		t.Fatalf("api request: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("decode structured output %q: %v", out, err)
	}
	if _, ok := payload["details"]; ok {
		t.Errorf("payload = %v, want no details key when the server sent none", payload)
	}
}

// TestFeaturesCreateSurfacesValidationField covers issue #73's integration
// expectation: a typed command failing with a detailed 400 names the
// offending field in its error, so callers see why instead of a bare
// "Validation failed (HTTP 400)".
func TestFeaturesCreateSurfacesValidationField(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/console/workspaces/ws_1/feature-requests" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Validation failed","details":{"formErrors":[],"fieldErrors":{"title":["Title must be at least 3 characters"]}}}`))
	}))
	defer server.Close()

	_, err := runRootWithSeededConfig(t, server.URL, defaultAppConfig,
		"features", "create", "--title", "ab", "--description", "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "title: Title must be at least 3 characters") {
		t.Errorf("error = %v, want the field-level reason", err)
	}
}
