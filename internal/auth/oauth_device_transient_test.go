package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// rawDevicePollScript serves each poll with the next scripted action; the
// last action repeats once the script is exhausted. Actions either write a
// raw response (any status/body) or abort the connection. It backs the
// issue-#358 tests, which need 502 HTML pages, 429 throttles, and closed
// connections that scriptedDeviceTokenServer's RFC 8628 code script cannot
// express.
type rawDevicePollAction func(w http.ResponseWriter)

func rawDevicePollServer(actions ...rawDevicePollAction) (*httptest.Server, *devicePollLog) {
	log := &devicePollLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record()
		n := log.count()
		action := actions[len(actions)-1]
		if n-1 < len(actions) {
			action = actions[n-1]
		}
		action(w)
	}))
	return server, log
}

func htmlGateway502(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte("<html><body>502 Bad Gateway</body></html>"))
}

func throttle429(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"rate limited"}`))
}

func serverError503(w http.ResponseWriter) {
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("overloaded"))
}

func pendingJSON(w http.ResponseWriter) {
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
}

func successTokens(w http.ResponseWriter) {
	_, _ = w.Write([]byte(`{"access_token":"cpt_dev","refresh_token":"cpr_dev","token_type":"Bearer","expires_in":1209600,"scope":"full"}`))
}

// TestDeviceWaitToleratesTransientFailures pins the issue #358 contract: a
// single transient token-poll failure — a gateway 502 HTML page, a 429
// throttle answering the RFC 6749 shape, or a closed connection (transport
// error) — must not kill the login. Each script is blip → pending → success
// and Wait must return the token pair, emitting exactly one stderr notice
// for the absorbed blip.
func TestDeviceWaitToleratesTransientFailures(t *testing.T) {
	cases := map[string]rawDevicePollAction{
		"502 html gateway page": htmlGateway502,
		"429 throttle":          throttle429,
		"closed connection":     func(w http.ResponseWriter) { panic(http.ErrAbortHandler) },
	}
	for name, blip := range cases {
		t.Run(name, func(t *testing.T) {
			server, log := rawDevicePollServer(blip, pendingJSON, successTokens)
			defer server.Close()

			d := newDeviceStart(server.URL)
			d.Interval = 20 * time.Millisecond
			stop := captureStderr(t)
			set, err := d.Wait(context.Background())
			stderr := stop()
			if err != nil {
				t.Fatalf("Wait: %v", err)
			}
			if set == nil || set.AccessToken != "cpt_dev" {
				t.Fatalf("set = %+v, want the scripted token pair", set)
			}
			if log.count() != 3 {
				t.Errorf("flow used %d polls, want 3 (blip, pending, success)", log.count())
			}
			if notices := strings.Count(stderr, "transient failure 1/5"); notices != 1 {
				t.Errorf("stderr carried %d absorbed-failure notices, want exactly 1; stderr: %s", notices, stderr)
			}
		})
	}
}

// TestDeviceWaitAbortsAfterConsecutiveTransientFailures pins the tolerance
// bound: an endpoint that keeps failing transiently must end the flow with
// the consecutive-failure error after maxConsecutiveTransientPollFailures
// polls — not hang until ExpiresAt and not loop forever.
func TestDeviceWaitAbortsAfterConsecutiveTransientFailures(t *testing.T) {
	server, log := rawDevicePollServer(serverError503)
	defer server.Close()

	d := newDeviceStart(server.URL)
	d.Interval = time.Millisecond
	done := make(chan error, 1)
	go func() {
		_, err := d.Wait(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "consecutive polls") {
			t.Fatalf("Wait error = %v, want the consecutive-failure message", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait still running after 5s against an always-503 endpoint (bound not enforced?)")
	}
	if got := log.count(); got != maxConsecutiveTransientPollFailures {
		t.Errorf("endpoint polled %d times, want %d (the tolerance bound)", got, maxConsecutiveTransientPollFailures)
	}
}

// TestDeviceWaitTransientCounterResetsAfterProgress pins that only
// *consecutive* transient failures count: an answered poll (here
// authorization_pending) resets the counter, so blips spread across the
// window never reach the bound.
func TestDeviceWaitTransientCounterResetsAfterProgress(t *testing.T) {
	server, log := rawDevicePollServer(
		serverError503, serverError503, serverError503, serverError503, // 4 tolerated
		pendingJSON, // answered poll resets the count
		serverError503,
		pendingJSON,
		successTokens,
	)
	defer server.Close()

	d := newDeviceStart(server.URL)
	d.Interval = time.Millisecond
	set, err := d.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if set.AccessToken != "cpt_dev" {
		t.Fatalf("set = %+v", set)
	}
	if log.count() != 8 {
		t.Errorf("flow used %d polls, want 8", log.count())
	}
}

// TestDeviceWaitBacksOffOnConsecutiveTransientFailures pins the backoff:
// each consecutive transient failure at least doubles the next wait, and a
// later answered poll resets it — the poll after progress waits the plain
// interval again.
func TestDeviceWaitBacksOffOnConsecutiveTransientFailures(t *testing.T) {
	server, log := rawDevicePollServer(
		serverError503, serverError503, serverError503,
		pendingJSON,
		successTokens,
	)
	defer server.Close()

	d := newDeviceStart(server.URL)
	d.Interval = 40 * time.Millisecond
	if _, err := d.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if log.count() != 5 {
		t.Fatalf("flow used %d polls, want 5", log.count())
	}
	// Lower bounds: ~1×, 2×, 4× the interval (timers fire late, so ≥).
	if s := log.spacing(0); s < 40*time.Millisecond {
		t.Errorf("spacing after 1st 503 = %v, want ≥ 40ms", s)
	}
	if s := log.spacing(1); s < 80*time.Millisecond {
		t.Errorf("spacing after 2nd 503 = %v, want ≥ 80ms (backoff doubles)", s)
	}
	if s := log.spacing(2); s < 160*time.Millisecond {
		t.Errorf("spacing after 3rd 503 = %v, want ≥ 160ms (backoff doubles again)", s)
	}
	// After the pending answer the backoff is reset: poll 5 waits ~1×.
	// The 120ms ceiling budgets ~80ms of handler/scheduling slack on top of
	// the 40ms interval while still separating cleanly from a missed reset,
	// which would hold the 4× (160ms) backoff and always cross the bound.
	if s := log.spacing(3); s >= 120*time.Millisecond {
		t.Errorf("spacing after pending = %v, want ≈ 40ms (backoff leaked across progress)", s)
	}
}

// TestDeviceWaitHonorsRetryAfter pins the review fix: a 429 carrying
// Retry-After must pace the next poll by the server's hint (the greater of
// hint and interval) instead of re-polling at the advertised interval —
// otherwise the tolerance budget burns through before the throttle window
// ends. The delay is per-attempt: the following poll waits the plain
// interval again.
func TestDeviceWaitHonorsRetryAfter(t *testing.T) {
	server, log := rawDevicePollServer(
		func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "1")
			throttle429(w)
		},
		pendingJSON,
		successTokens,
	)
	defer server.Close()

	d := newDeviceStart(server.URL)
	d.Interval = 20 * time.Millisecond
	set, err := d.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if set.AccessToken != "cpt_dev" {
		t.Fatalf("set = %+v", set)
	}
	if log.count() != 3 {
		t.Fatalf("flow used %d polls, want 3", log.count())
	}
	// Lower bound: poll 2 waited at least the hinted second. Upper bound
	// guards the per-attempt scope: it must not have grown by the cap.
	if spacing := log.spacing(0); spacing < 950*time.Millisecond {
		t.Errorf("spacing after 429 Retry-After: 1 = %v, want ≥ 950ms", spacing)
	} else if spacing > 3*time.Second {
		t.Errorf("spacing after 429 = %v, want ≈ the hinted 1s (delay leaked past one attempt?)", spacing)
	}
	// Poll 3 waited the plain interval again.
	if spacing := log.spacing(1); spacing >= 900*time.Millisecond {
		t.Errorf("spacing after pending = %v, want ≈ the 20ms interval (Retry-After leaked into later polls)", spacing)
	}
}

// TestDeviceWaitCapsRetryAfter pins the ceiling: a hostile or clumsy
// Retry-After cannot park one poll cycle longer than maxHonoredRetryAfter.
// The test shrinks the cap so the bound itself is what the spacing proves.
func TestDeviceWaitCapsRetryAfter(t *testing.T) {
	old := maxHonoredRetryAfter
	maxHonoredRetryAfter = 120 * time.Millisecond
	defer func() { maxHonoredRetryAfter = old }()

	server, log := rawDevicePollServer(
		func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "120")
			serverError503(w)
		},
		successTokens,
	)
	defer server.Close()

	d := newDeviceStart(server.URL)
	d.Interval = 20 * time.Millisecond
	if _, err := d.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if spacing := log.spacing(0); spacing < 100*time.Millisecond || spacing > 2*time.Second {
		t.Errorf("spacing with Retry-After: 120 under a 120ms cap = %v, want ≈ the capped 120ms", spacing)
	}
}

// TestParseRetryAfter pins the Retry-After parser: delay-seconds and
// HTTP-date forms are honored, absent/negative/unparseable values mean "no
// hint", and every result is capped at maxHonoredRetryAfter.
func TestParseRetryAfter(t *testing.T) {
	old := maxHonoredRetryAfter
	maxHonoredRetryAfter = time.Minute
	defer func() { maxHonoredRetryAfter = old }()

	future := time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)
	cases := map[string]struct {
		v    string
		want time.Duration
	}{
		"absent":            {"", 0},
		"seconds":           {"30", 30 * time.Second},
		"capped seconds":    {"3600", time.Minute},
		"negative":          {"-5", 0},
		"garbage":           {"soon", 0},
		"http-date future":  {future, time.Minute},
		"http-date past":    {time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
		"surrounding space": {"  5  ", 5 * time.Second},
		// Issue #374: delay-seconds values past int64-nanosecond range
		// (~292 years) used to wrap negative and slip past the
		// maxHonoredRetryAfter cap; they must clamp to it instead.
		"first overflowing": {"9223372037", time.Minute},
		"ten billion":       {"10000000000", time.Minute},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := parseRetryAfter(tc.v); got != tc.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.v, got, tc.want)
			}
		})
	}
}

// TestPostFormPopulatesRetryAfter pins that postForm surfaces the parsed
// Retry-After on the APIError so the poll loop can honor it.
func TestPostFormPopulatesRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
	}))
	defer server.Close()

	_, err := postForm(context.Background(), server.URL, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("postForm error = %v, want *APIError", err)
	}
	if apiErr.RetryAfter != 7*time.Second {
		t.Errorf("APIError.RetryAfter = %v, want 7s", apiErr.RetryAfter)
	}
	if apiErr.Status != http.StatusTooManyRequests {
		t.Errorf("APIError.Status = %d, want 429", apiErr.Status)
	}
}

// TestDeviceWaitStillAbortsOnDefinitive4xx pins the regression guard: a
// non-device-flow 4xx protocol rejection (invalid_client with 401) still
// ends the flow on first occurrence — transient tolerance must not swallow
// definitive rejections.
func TestDeviceWaitStillAbortsOnDefinitive4xx(t *testing.T) {
	server, log := rawDevicePollServer(
		pendingJSON,
		func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"unknown client"}`))
		},
	)
	defer server.Close()

	d := newDeviceStart(server.URL)
	d.Interval = time.Millisecond
	_, err := d.Wait(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid_client") {
		t.Fatalf("Wait error = %v, want the invalid_client rejection", err)
	}
	if log.count() != 2 {
		t.Errorf("flow used %d polls, want 2 (pending, then abort)", log.count())
	}
}

// TestAPIErrorCarriesStatus pins the plumbing half of issue #358: postForm
// populates APIError.Status from the HTTP response, so a poller can tell a
// 429/5xx from a definitive 4xx.
func TestAPIErrorCarriesStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   int
	}{
		{"429", http.StatusTooManyRequests, 429},
		{"502", http.StatusBadGateway, 502},
		{"400", http.StatusBadRequest, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
			}))
			defer server.Close()

			_, err := postForm(context.Background(), server.URL, nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("postForm error = %v, want *APIError", err)
			}
			if apiErr.Status != tc.want {
				t.Errorf("APIError.Status = %d, want %d", apiErr.Status, tc.want)
			}
		})
	}
}

// TestTransientPollFailureClassification pins the classifier itself against
// silent regressions in both directions.
func TestTransientPollFailureClassification(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"transport error":         {errors.New("POST https://x: connection reset by peer"), true},
		"429":                     {&APIError{Code: "invalid_request", Status: 429}, true},
		"502":                     {&APIError{Code: "", Body: "<html>bad gateway</html>", Status: 502}, true},
		"503":                     {&APIError{Status: 503}, true},
		"400 invalid_request":     {&APIError{Code: "invalid_request", Status: 400}, false},
		"401 invalid_client":      {&APIError{Code: "invalid_client", Status: 401}, false},
		"status lost (zero)":      {&APIError{Code: "invalid_grant"}, false},
		"wrapped transport error": {fmt.Errorf("POST %s: %w", "https://x", context.DeadlineExceeded), true},
		"wrapped 5xx APIError":    {fmt.Errorf("POST %s: %w", "https://x", &APIError{Status: 502}), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := transientPollFailure(tc.err); got != tc.want {
				t.Errorf("transientPollFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
