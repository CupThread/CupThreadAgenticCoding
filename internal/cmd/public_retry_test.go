package cmd

// Issue #202: the four call sites that used to hand-roll api.New(...) —
// apps public-changelog / public-feature-requests / public-config and the
// auth login --token /console/me probe — must share the root client's retry
// contract. --no-retry / $CUPTHREAD_NO_RETRY restore single-shot semantics;
// without the opt-out a transient 429/502/503/504 is retried with one stderr
// notice per retry in human mode, while --json/-o yaml keeps stderr
// retry-silent and stdout a single document.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// publicRetryCase is one of the four former hand-rolled-client call sites,
// with a success body the command can decode once its retry succeeds.
type publicRetryCase struct {
	name string
	args []string
	// successBody is served after the transient failure so the happy-path
	// retry tests can prove the command still finishes cleanly.
	successBody string
}

// publicRetryCases enumerates the four call sites named in issue #202. The
// success bodies reuse the fixtures other test files in this package already
// pin: the public feeds, the public config, and /console/me for the probe.
func publicRetryCases() []publicRetryCase {
	return []publicRetryCase{
		{
			name:        "apps public-changelog",
			args:        []string{"apps", "public-changelog", "key_live_1"},
			successBody: `{"entries":[],"hasMore":false,"nextCursor":null}`,
		},
		{
			name:        "apps public-feature-requests",
			args:        []string{"apps", "public-feature-requests", "key_live_1"},
			successBody: publicRequestsFixture,
		},
		{
			name:        "apps public-config",
			args:        []string{"apps", "public-config", "key_live_1"},
			successBody: publicConfigFixture,
		},
		{
			name:        "auth login --token probe",
			args:        []string{"auth", "login", "--token", "cpt_probe_token"},
			successBody: meFixture,
		},
	}
}

// alwaysTransientServer answers 502 + Retry-After: 0 on every request and
// counts the wire attempts; the zero retry-after keeps the tests fast.
func alwaysTransientServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "0")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"Bad gateway"}`))
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// transientThenOKServer answers one 429 (Retry-After: 0) and then okBody, so
// a single retry recovers the command.
func transientThenOKServer(t *testing.T, okBody string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"Too many requests. Please try again shortly."}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okBody))
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// TestPublicCommandsHonorNoRetryOptOut: with --no-retry or
// $CUPTHREAD_NO_RETRY=1, each of the four call sites issues exactly one wire
// request against an always-502 stub — the single-shot semantics the flag
// promises (issue #202 acceptance criterion 1).
func TestPublicCommandsHonorNoRetryOptOut(t *testing.T) {
	for _, tc := range publicRetryCases() {
		t.Run(tc.name+" flag", func(t *testing.T) {
			server, hits := alwaysTransientServer(t)
			args := append(append([]string{}, tc.args...), "--no-retry")
			_, _, _ = runRetryRoot(t, server.URL, args...)
			if got := hits.Load(); got != 1 {
				t.Errorf("wire attempts = %d, want exactly 1 with --no-retry", got)
			}
		})
		t.Run(tc.name+" env", func(t *testing.T) {
			t.Setenv("CUPTHREAD_NO_RETRY", "1")
			server, hits := alwaysTransientServer(t)
			_, _, _ = runRetryRoot(t, server.URL, tc.args...)
			if got := hits.Load(); got != 1 {
				t.Errorf("wire attempts = %d, want exactly 1 with $CUPTHREAD_NO_RETRY=1", got)
			}
		})
	}
}

// TestPublicCommandsRetryWithStderrNotice: without the opt-out each command
// retries the transient 502 up to 3 more times (4 wire attempts) and, in
// human mode, prints one retry notice per retry on stderr only — the
// transparency every A.client command already had (issue #202 acceptance
// criterion 2).
func TestPublicCommandsRetryWithStderrNotice(t *testing.T) {
	for _, tc := range publicRetryCases() {
		t.Run(tc.name, func(t *testing.T) {
			server, hits := alwaysTransientServer(t)
			out, stderr, execErr := runRetryRoot(t, server.URL, tc.args...)
			if execErr == nil {
				t.Fatal("expected the exhausted-retry command to exit non-zero")
			}
			if got := hits.Load(); got != 4 {
				t.Errorf("wire attempts = %d, want 4 (1 + 3 retries)", got)
			}
			if got := strings.Count(stderr, "retrying (attempt"); got != 3 {
				t.Errorf("stderr = %q, want 3 retry notices, got %d", stderr, got)
			}
			if strings.Contains(out, "retrying") {
				t.Errorf("stdout = %q, want no retry noise there", out)
			}
		})
	}
}

// TestPublicCommandsJSONStaysRetrySilent: in --json mode the retries still
// happen (4 wire attempts) but stderr carries no retry notice and stdout no
// retry noise, so the machine stream stays parse-clean (issue #202
// acceptance criterion 2).
func TestPublicCommandsJSONStaysRetrySilent(t *testing.T) {
	for _, tc := range publicRetryCases() {
		t.Run(tc.name, func(t *testing.T) {
			server, hits := alwaysTransientServer(t)
			args := append(append([]string{}, tc.args...), "--json")
			out, stderr, _ := runRetryRoot(t, server.URL, args...)
			if got := hits.Load(); got != 4 {
				t.Errorf("wire attempts = %d, want 4 (1 + 3 retries)", got)
			}
			if strings.Contains(stderr, "retrying") {
				t.Errorf("stderr = %q, want it retry-silent in --json mode", stderr)
			}
			if strings.Contains(out, "retrying") {
				t.Errorf("stdout = %q, want no retry noise there", out)
			}
		})
	}
}

// TestPublicCommandsSucceedAfterTransientRetry: one throttled attempt
// followed by success lets each command finish cleanly with exactly one
// stderr notice in human mode — the shared wiring did not break the happy
// path — and in --json mode with stdout still a single JSON document.
func TestPublicCommandsSucceedAfterTransientRetry(t *testing.T) {
	for _, tc := range publicRetryCases() {
		t.Run(tc.name+" human", func(t *testing.T) {
			server, hits := transientThenOKServer(t, tc.successBody)
			out, stderr, execErr := runRetryRoot(t, server.URL, tc.args...)
			if execErr != nil {
				t.Fatalf("expected the retried command to succeed, got %v\nstdout:\n%s", execErr, out)
			}
			if got := hits.Load(); got != 2 {
				t.Errorf("wire attempts = %d, want 2 (1 throttled + 1 retry)", got)
			}
			if got := strings.Count(stderr, "retrying (attempt"); got != 1 {
				t.Errorf("stderr = %q, want exactly 1 retry notice, got %d", stderr, got)
			}
			if strings.Contains(out, "retrying") {
				t.Errorf("stdout = %q, want no retry noise there", out)
			}
		})
		t.Run(tc.name+" json", func(t *testing.T) {
			server, hits := transientThenOKServer(t, tc.successBody)
			args := append(append([]string{}, tc.args...), "--json")
			out, stderr, execErr := runRetryRoot(t, server.URL, args...)
			if execErr != nil {
				t.Fatalf("expected the retried command to succeed, got %v\nstdout:\n%s", execErr, out)
			}
			if got := hits.Load(); got != 2 {
				t.Errorf("wire attempts = %d, want 2 (1 throttled + 1 retry)", got)
			}
			if strings.Contains(stderr, "retrying") {
				t.Errorf("stderr = %q, want it retry-silent in --json mode", stderr)
			}
			if !json.Valid([]byte(strings.TrimSpace(out))) {
				t.Errorf("stdout = %q, want a single valid JSON document", out)
			}
		})
	}
}

// TestNoHandRolledAPIClients guards the source-level invariant behind issue
// #202: within internal/cmd only root.go may construct an api.Client
// directly. Any other file must go through app.buildClient (authenticated) or
// app.unauthenticatedClient (public/probe), so the --no-retry opt-out and
// the human-mode stderr retry notices apply uniformly instead of silently
// disappearing at a fresh call site.
func TestNoHandRolledAPIClients(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read internal/cmd: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") || name == "root.go" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(data), "api.New(") {
			t.Errorf("%s constructs an api.Client by hand; build it through app.buildClient/unauthenticatedClient so the retry opt-out and notices apply (issue #202)", name)
		}
	}
}
