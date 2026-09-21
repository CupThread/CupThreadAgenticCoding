package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAPIRequestSurfacesQuotaHint covers issue #21: when the API rejects a
// submission with 402 (POST /api/v1/feature-requests quota contract), the
// `api request --json` escape hatch must surface the machine-readable code
// plus an actionable hint so agents can react without guessing. Issue #72:
// the invocation must also fail (non-nil error drives exit 1 in main.go) —
// the payload on stdout never excuses a zero exit code.
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
	if err == nil {
		t.Fatal("api request exited 0 on a 402, want a non-nil error so the process exits 1")
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
// file reproducible bug reports. Issue #72: the 404 must also fail the
// invocation in --json mode.
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
	if err == nil {
		t.Fatal("api request exited 0 on a 404, want a non-nil error so the process exits 1")
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

// TestAPIRequestJSONValidationErrorExitsNonZero covers issue #72: a 400 in
// --json mode must fail the invocation while the {error, code, status}
// payload still reaches stdout, so scripts get both the exit-code signal and
// a parseable failure description.
func TestAPIRequestJSONValidationErrorExitsNonZero(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Validation failed"}`))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "api", "request", "POST", "/api/v1/console/workspaces/ws_1/feature-requests", "--json")
	if err == nil {
		t.Fatal("api request exited 0 on a 400, want a non-nil error so the process exits 1")
	}
	var payload struct {
		Error  string `json:"error"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("decode structured output %q: %v", out, err)
	}
	if payload.Status != http.StatusBadRequest || payload.Error != "Validation failed" {
		t.Errorf("payload = %+v, want status 400 / Validation failed", payload)
	}
}

// TestAPIRequestYAMLErrorExitsNonZero covers issue #72 for -o yaml: the yaml
// error payload must still reach stdout while the invocation fails.
func TestAPIRequestYAMLErrorExitsNonZero(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Validation failed"}`))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "api", "request", "POST", "/api/v1/console/workspaces/ws_1/feature-requests", "-o", "yaml")
	if err == nil {
		t.Fatal("api request exited 0 on a 400 in yaml mode, want a non-nil error so the process exits 1")
	}
	var payload struct {
		Error  string `yaml:"error"`
		Status int    `yaml:"status"`
	}
	if err := yaml.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("decode yaml output %q: %v", out, err)
	}
	if payload.Status != http.StatusBadRequest || payload.Error != "Validation failed" {
		t.Errorf("payload = %+v, want status 400 / Validation failed", payload)
	}
}

// TestAPIRequestJSONSuccessPassthrough pins the issue #72 success path: a 200
// in --json mode keeps exit 0 and passes the raw response body through
// faithfully — identical once indentation is removed, with every number
// preserved exactly as the server sent it.
func TestAPIRequestJSONSuccessPassthrough(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	const body = `{"me":{"id":"usr_1","email":"dev@example.com"},"bigNumber":1699999999999999999}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "api", "request", "GET", "/api/v1/console/me", "--json")
	if err != nil {
		t.Fatalf("api request: %v", err)
	}
	compact := func(s string) string {
		t.Helper()
		var buf bytes.Buffer
		if err := json.Compact(&buf, []byte(s)); err != nil {
			t.Fatalf("compact output %q: %v", s, err)
		}
		return buf.String()
	}
	if got := compact(out); got != body {
		t.Errorf("stdout = %q, want the raw body compacted to %q", got, body)
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

// TestAPIRequestInputSendsNumbersByteIdentical covers issue #75: the escape
// hatch must forward the --input bytes verbatim. The previous decode-into-any
// path rebuilt every number through float64, silently rewriting integers
// beyond 2^53, nanosecond timestamps and number formatting before the request
// left the CLI.
func TestAPIRequestInputSendsNumbersByteIdentical(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	const body = `{"bigId":12345678901234567890,"ts":1699999999999999999,` +
		`"exp":1e21,"neg":-9007199254740993,"frac":0.30000000000000004, "pad": [1, 2]}`
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	if _, err := runRoot(t, server.URL, "api", "request", "POST", "/api/v1/anything",
		"--input", writeInputFile(t, body)); err != nil {
		t.Fatalf("api request: %v", err)
	}
	if got != body {
		t.Errorf("wire body =\n%q\nwant the input file's exact bytes:\n%q", got, body)
	}
}

// TestSettingsSetInputPreservesNumbersAndFlagsOverride covers issue #75 for
// `apps settings set --input` (raw JSON values must not round-trip through
// float64) and the documented merge order: the --input body supplies the
// base, boolean flags override their keys.
func TestSettingsSetInputPreservesNumbersAndFlagsOverride(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var gotPut string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/apps"):
			_, _ = w.Write([]byte(`{"apps":[{"appId":"app_1","appKey":"key_1","slug":"app-one","name":"App One"}]}`))
		case r.Method == http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			gotPut = string(b)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	if _, err := runRoot(t, server.URL, "apps", "settings", "set", "app_1", "--workspace", "ws_1",
		"--input", writeInputFile(t, `{"allowAnonymousRoadmap":true,"maxAttachments":12345678901234567890}`),
		"--anon-roadmap=false"); err != nil {
		t.Fatalf("apps settings set: %v", err)
	}
	if !strings.Contains(gotPut, `"maxAttachments":12345678901234567890`) {
		t.Errorf("wire body = %s, want the big integer sent verbatim", gotPut)
	}
	if !strings.Contains(gotPut, `"allowAnonymousRoadmap":false`) {
		t.Errorf("wire body = %s, want the boolean flag to override the --input key", gotPut)
	}
}

// TestImportsCreateOptionsPreserveNumbers covers issue #75 for the second
// decode-into-any consumer: numeric option values from --options must reach
// the server unchanged.
func TestImportsCreateOptionsPreserveNumbers(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job":{"id":"job_1","source":"linear","mode":"preview","status":"queued"},"drain":{"processed":0,"succeeded":0,"failed":0}}`))
	}))
	defer server.Close()

	if _, err := runRoot(t, server.URL, "imports", "create", "--workspace", "ws_1", "--app", "app_1",
		"--source", "linear", "--options", writeInputFile(t, `{"teamId":"t_1","limit":12345678901234567890}`)); err != nil {
		t.Fatalf("imports create: %v", err)
	}
	if !strings.Contains(got, `"limit":12345678901234567890`) {
		t.Errorf("wire body = %s, want the big integer sent verbatim", got)
	}
	if !strings.Contains(got, `"teamId":"t_1"`) {
		t.Errorf("wire body = %s, want the raw options object carried through", got)
	}
}

// TestImportsCreateFlagsStillReachOptions guards the flag branch after the
// issue #75 raw-passthrough change: with --options absent, the per-source
// flags keep building the options object.
func TestImportsCreateFlagsStillReachOptions(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job":{"id":"job_1","source":"github_issues","mode":"preview","status":"queued"},"drain":{"processed":0,"succeeded":0,"failed":0}}`))
	}))
	defer server.Close()

	if _, err := runRoot(t, server.URL, "imports", "create", "--workspace", "ws_1", "--app", "app_1",
		"--source", "github_issues", "--owner", "acme", "--repo", "api", "--state", "open",
		"--limit", "7"); err != nil {
		t.Fatalf("imports create: %v", err)
	}
	for _, want := range []string{`"owner":"acme"`, `"repo":"api"`, `"state":"open"`, `"limit":7`} {
		if !strings.Contains(got, want) {
			t.Errorf("wire body = %s, missing %s", got, want)
		}
	}
}
