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
