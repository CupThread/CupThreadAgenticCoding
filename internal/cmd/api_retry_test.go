package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// runRetryRoot executes the CLI capturing both stdout and stderr, with every
// flag global reset first (they persist across Execute calls in one test
// binary). It exists so retry tests can pin the stdout/stderr split without
// inheriting --no-retry or output-format state from other tests.
func runRetryRoot(t *testing.T, serverURL string, args ...string) (string, string, error) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = outW
	os.Stderr = errW

	flagBaseURL = ""
	flagWorkspace = ""
	flagApp = ""
	flagJSON = false
	flagOutput = ""
	flagNoRetry = false
	defer func() { flagNoRetry = false }()

	root := newRootCmd()
	full := append(append([]string{}, args...), "--base-url", serverURL, "--config", filepath.Join(t.TempDir(), "config.json"))
	root.SetArgs(full)
	execErr := root.Execute()

	os.Stdout = oldStdout
	os.Stderr = oldStderr
	for _, c := range []*os.File{outW, errW} {
		if err := c.Close(); err != nil {
			t.Fatalf("close pipe: %v", err)
		}
	}
	out, err := io.ReadAll(outR)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	errText, err := io.ReadAll(errR)
	if err != nil {
		t.Fatalf("read stderr pipe: %v", err)
	}
	return string(out), string(errText), execErr
}

// TestAPIRequestRetriesExhausted: an always-429 GET fires 1 + 3 attempts.
// In human mode the final failure exits non-zero and each retry logged one
// notice on stderr. Every response supplies Retry-After: 0, keeping the
// waits instantaneous.
func TestAPIRequestRetriesExhausted(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "0")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"Too many requests. Please try again shortly."}`))
	}))
	defer server.Close()

	out, stderr, execErr := runRetryRoot(t, server.URL, "api", "request", "GET", "/x")
	if execErr == nil {
		t.Fatal("expected the exhausted-retry request to exit non-zero")
	}
	if got := hits.Load(); got != 4 {
		t.Errorf("wire attempts = %d, want 4 (1 + 3 retries)", got)
	}
	if strings.Contains(out, "retrying") {
		t.Errorf("stdout = %q, want no retry noise there", out)
	}
	if got := strings.Count(stderr, "retrying (attempt"); got != 3 {
		t.Errorf("stderr = %q, want 3 retry notices, got %d", stderr, got)
	}
}

// TestAPIRequestJSONPayloadReportsAttempts: the --json error payload carries
// error/status/hint plus attempts=4 so agents can tell exhausted retries
// from permanent failures. (The exit code of --json error payloads is issue
// #72's separate contract, being fixed by its own PR — not asserted here.)
func TestAPIRequestJSONPayloadReportsAttempts(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "0")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"Too many requests. Please try again shortly."}`))
	}))
	defer server.Close()

	out, _, _ := runRetryRoot(t, server.URL, "api", "request", "GET", "/x", "--json")
	if got := hits.Load(); got != 4 {
		t.Errorf("wire attempts = %d, want 4 (1 + 3 retries)", got)
	}
	var payload struct {
		Error    string `json:"error"`
		Status   int    `json:"status"`
		Hint     string `json:"hint"`
		Attempts int    `json:"attempts"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("decode stdout payload %q: %v", out, err)
	}
	if payload.Status != http.StatusTooManyRequests || payload.Error != "Too many requests. Please try again shortly." {
		t.Errorf("payload = %+v", payload)
	}
	if payload.Attempts != 4 {
		t.Errorf("attempts = %d, want 4 in the --json error payload", payload.Attempts)
	}
	if !strings.Contains(payload.Hint, "back off exponentially") {
		t.Errorf("hint = %q, want the backoff guidance", payload.Hint)
	}
}

// TestAPIRequestNoRetryFlagSingleShot: --no-retry restores single-shot
// semantics — exactly one wire attempt and no attempts field in the payload.
func TestAPIRequestNoRetryFlagSingleShot(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"Too many requests. Please try again shortly."}`))
	}))
	defer server.Close()

	out, _, _ := runRetryRoot(t, server.URL, "api", "request", "GET", "/x", "--json", "--no-retry")
	if got := hits.Load(); got != 1 {
		t.Errorf("wire attempts = %d, want exactly 1 with --no-retry", got)
	}
	if strings.Contains(out, "attempts") {
		t.Errorf("stdout = %q, want no attempts field for a single-shot failure", out)
	}
}

// TestNoRetryEnvSingleShot: $CUPTHREAD_NO_RETRY=1 behaves like --no-retry.
func TestNoRetryEnvSingleShot(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	t.Setenv("CUPTHREAD_NO_RETRY", "1")

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"Too many requests. Please try again shortly."}`))
	}))
	defer server.Close()

	_, _, _ = runRetryRoot(t, server.URL, "api", "request", "GET", "/x", "--json")
	if got := hits.Load(); got != 1 {
		t.Errorf("wire attempts = %d, want exactly 1 with $CUPTHREAD_NO_RETRY=1", got)
	}
}

// TestRetryNoticeStaysOffStdout: in human mode a successful retry prints the
// retry notice to stderr only — stdout keeps exactly the normal ✓ line and
// response body, so scripts scraping stdout see no retry noise.
func TestRetryNoticeStaysOffStdout(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"Too many requests. Please try again shortly."}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	out, stderr, execErr := runRetryRoot(t, server.URL, "api", "request", "GET", "/x")
	if execErr != nil {
		t.Fatalf("expected the retry to succeed, got %v", execErr)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("wire attempts = %d, want 2", got)
	}
	if !strings.Contains(out, `{"ok":true}`) {
		t.Errorf("stdout = %q, want the response body", out)
	}
	if strings.Contains(out, "retrying") {
		t.Errorf("stdout = %q, want no retry noise there", out)
	}
	if !strings.Contains(stderr, "GET /x got HTTP 429") || !strings.Contains(stderr, "retrying (attempt 2/4") {
		t.Errorf("stderr = %q, want exactly one retry notice", stderr)
	}
	if strings.Count(stderr, "retrying") != 1 {
		t.Errorf("stderr = %q, want exactly one retrying line", stderr)
	}
}
