package api

// Issue #435: optional image protection. The deployment can answer an
// optional managed image (GET /api/v1/files/*, console icon upload) with
// 503 {"error": …, "code": "resource_protection"} + Cache-Control: no-store
// + Retry-After while storage is protected. That is a temporary degradation,
// never a missing resource, so the client must classify it (ResourceProtected,
// actionable hint) and surface the server's retry timing instead of leaving
// callers to guess.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// protectedImageServer serves the issue #435 degraded response on the first
// recoverAfter hits and the recovered image bytes afterwards; a huge count
// keeps the endpoint always degraded. Retry-After: 0 keeps the default
// tests fast; protectedImageServerWithHint sets another hint value.
func protectedImageServer(t *testing.T, recoverAfter int) (*httptest.Server, *int) {
	t.Helper()
	return protectedImageServerWithHint(t, recoverAfter, "0")
}

func protectedImageServerWithHint(t *testing.T, recoverAfter int, retryAfter string) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits > recoverAfter {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"Image temporarily unavailable","code":"resource_protection"}`))
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// TestResourceProtection503IsTemporaryClassified pins the classification: a
// 503 carrying code resource_protection is a ResourceProtected APIError whose
// hint explains temporary unavailability, Retry-After pacing, the
// resource-protection signal endpoint, and the never-cache rule — and it is
// part of the rendered error text. A plain 503 without the code must not
// classify.
func TestResourceProtection503IsTemporaryClassified(t *testing.T) {
	server, _ := protectedImageServer(t, 1<<30)
	client := New(server.URL)
	client.NoRetry = true // classification, not the retry loop

	err := client.Do(t.Context(), "GET", "/api/v1/files/images%2Ficon.png", nil, nil, nil)
	var apiErr *APIError
	if !strings.Contains(err.Error(), "resource protected:") || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v, want a resource-protected 503 wrapper", err)
	}
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError inside %v", err)
	}
	if !apiErr.ResourceProtected() {
		t.Errorf("ResourceProtected() = false for %v", apiErr)
	}
	hint := apiErr.Hint()
	for _, want := range []string{
		"temporarily unavailable, not deleted",
		"Retry-After",
		"/api/v1/public/resource-protection",
		"optionalImagesDisabled",
		"never cache",
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("Hint() = %q, want it to contain %q", hint, want)
		}
	}
	if !strings.Contains(err.Error(), "temporarily unavailable, not deleted") {
		t.Errorf("err = %q, want the rendered error to carry the hint", err)
	}

	// A plain gateway 503 without the code stays an ordinary APIError.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("overloaded"))
	}))
	defer plain.Close()
	err2 := New(plain.URL).Do(t.Context(), "GET", "/x", nil, nil, nil)
	var plainErr *APIError
	if !errors.As(err2, &plainErr) {
		t.Fatalf("expected *APIError for a plain 503, got %v", err2)
	}
	if plainErr.ResourceProtected() {
		t.Errorf("plain 503 classified as resource-protected: %v", err2)
	}
	if hint := plainErr.Hint(); hint != "" {
		t.Errorf("Hint() = %q for a plain 503, want \"\"", hint)
	}
}

// TestResourceProtection503ExposesRetryAfter pins the pacing surface: the
// parsed Retry-After lands on APIError.RetryAfter for the degraded response,
// and a response without the header leaves it zero.
func TestResourceProtection503ExposesRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"Icon uploads are temporarily unavailable","code":"resource_protection"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	client.NoRetry = true
	err := client.Do(t.Context(), "POST", "/api/v1/console/workspaces/ws_1/apps/app_1/icon", nil, nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.RetryAfter != 120*time.Second {
		t.Errorf("RetryAfter = %v, want 120s", apiErr.RetryAfter)
	}

	// No header → no hint.
	noHint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer noHint.Close()
	err2 := New(noHint.URL).Do(t.Context(), "GET", "/x", nil, nil, nil)
	var apiErr2 *APIError
	if !errors.As(err2, &apiErr2) || apiErr2.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v without a Retry-After header, want 0 (%v)", apiErr2.RetryAfter, err2)
	}
}

// TestResourceProtection503RetriesAndRecovers pins the recovery contract
// end-to-end: a body-less GET against the degraded image endpoint is
// transient-retried (503 is in the retryable set), honors Retry-After: 0
// between attempts, and resumes the normal image bytes once the deployment
// lifts protection — the asset is fetched again, never treated as deleted.
func TestResourceProtection503RetriesAndRecovers(t *testing.T) {
	server, hits := protectedImageServer(t, 2) // 2 degraded, then recover

	client := New(server.URL)
	var sleeps []time.Duration
	client.RetryBaseDelay = time.Millisecond
	client.RetryMaxDelay = time.Millisecond
	client.Sleeper = func(d time.Duration) { sleeps = append(sleeps, d) }

	var raw json.RawMessage
	if err := client.Do(t.Context(), "GET", "/api/v1/files/images%2Ficon.png", nil, nil, &raw); err != nil {
		t.Fatalf("expected recovery on retry, got %v", err)
	}
	if *hits != 3 || string(raw) != "png" {
		t.Fatalf("hits = %d, body = %q, want 3 attempts ending in the recovered image", *hits, raw)
	}
	if len(sleeps) != 2 {
		t.Errorf("slept %d times, want 2 (one per retry)", len(sleeps))
	}
}

// TestResourceProtection503LongHintCapsInternalRetryWaits pins the full
// 300-second case the protection 503 actually serves: the CLI's internal
// retry loop still clamps each wait to DefaultRetryMaxDelay (the documented
// hostile-header ceiling — a server hint must not stall a batch command for
// minutes), burns the budget against the deliberate degradation window, and
// then surfaces the uncapped 300s hint on the exhausted error so callers can
// pace their next attempt by it.
func TestResourceProtection503LongHintCapsInternalRetryWaits(t *testing.T) {
	server, _ := protectedImageServerWithHint(t, 1<<30, "300") // always degraded, Retry-After: 300
	client := New(server.URL)
	client.MaxRetries = 2
	var sleeps []time.Duration
	client.Sleeper = func(d time.Duration) { sleeps = append(sleeps, d) }

	err := client.Do(t.Context(), "GET", "/api/v1/files/images%2Ficon.png", nil, nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if !apiErr.ResourceProtected() || apiErr.Attempts != 3 {
		t.Fatalf("attempts = %d, protected = %v; want 3 exhausted attempts of a resource-protected 503", apiErr.Attempts, apiErr.ResourceProtected())
	}
	if apiErr.RetryAfter != 300*time.Second {
		t.Errorf("RetryAfter = %v, want the uncapped 300s hint on the surfaced error", apiErr.RetryAfter)
	}
	for i, wait := range sleeps {
		if wait > DefaultRetryMaxDelay {
			t.Errorf("retry wait %d = %v, want ≤ the %v hostile-header ceiling", i, wait, DefaultRetryMaxDelay)
		}
	}
	if len(sleeps) != 2 {
		t.Errorf("slept %d times, want 2 (one per retry)", len(sleeps))
	}
}

// TestUploadAppIconResourceProtection503 pins the multipart path (S1): the
// console icon upload is the second surface the protection 503 serves, so
// the same classification, uncapped Retry-After surface, and recovery hint
// must apply there — the upload is a mutation and stays single-shot.
func TestUploadAppIconResourceProtection503(t *testing.T) {
	server, hits := protectedImageServerWithHint(t, 1<<30, "300") // always degraded
	client := New(server.URL)
	client.Token = func(context.Context) (string, error) { return "cpt_test", nil }

	_, err := client.UploadAppIcon(t.Context(), "ws_1", "app_1", "icon.png", []byte("png"))
	if *hits != 1 {
		t.Errorf("wire attempts = %d, want exactly 1 (mutations are single-shot)", *hits)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if !apiErr.ResourceProtected() {
		t.Fatalf("ResourceProtected() = false for %v", apiErr)
	}
	if apiErr.RetryAfter != 300*time.Second {
		t.Errorf("RetryAfter = %v, want the uncapped 300s hint", apiErr.RetryAfter)
	}
	for _, want := range []string{"resource protected:", "temporarily unavailable, not deleted", "try again in 5m0s"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	}
}

// TestParseRetryAfterHeader pins the package-level parser behind
// APIError.RetryAfter: delay-seconds and HTTP-date forms parse, negative,
// absent, and unparseable values yield 0, and an absurd value cannot wrap
// the time.Duration conversion negative (issue #374's trap).
func TestParseRetryAfterHeader(t *testing.T) {
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"0", 0},
		{"120", 120 * time.Second},
		{" 300 ", 300 * time.Second},
		{future, 89 * time.Second}, // HTTP-date; compared loosely below
		{"", 0},
		{"-5", 0},
		{"later", 0},
		{"9223372036854775807", 0}, // would overflow the seconds→ns conversion
	}
	for _, tc := range cases {
		got := parseRetryAfterHeader(tc.in)
		if tc.in == future {
			if got <= 80*time.Second || got > 90*time.Second {
				t.Errorf("parseRetryAfterHeader(%q) = %v, want ≈90s (HTTP-date form)", tc.in, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("parseRetryAfterHeader(%q) = %v, want %v", tc.in, got, tc.want)
		}
		if got < 0 {
			t.Errorf("parseRetryAfterHeader(%q) = %v, must never be negative", tc.in, got)
		}
	}
}
