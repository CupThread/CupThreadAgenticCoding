package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestS256Challenge(t *testing.T) {
	// RFC 7636 appendix B test vector.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := s256Challenge(verifier); got != want {
		t.Errorf("s256Challenge = %q, want %q", got, want)
	}
}

func TestEndpoints(t *testing.T) {
	base := "https://api.cupthread.com/"
	authorize, token, deviceAuthorize, deviceToken := Endpoints(base)
	for _, ep := range []string{authorize, token, deviceAuthorize, deviceToken} {
		if !strings.HasPrefix(ep, "https://api.cupthread.com/api/v1/oauth") {
			t.Errorf("endpoint %q does not hang off the base URL", ep)
		}
	}
	if authorize != "https://api.cupthread.com"+AuthorizePath {
		t.Errorf("authorize = %q", authorize)
	}
}

func TestRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("refresh_token") != "cpr_old" {
			t.Errorf("refresh_token = %q", r.Form.Get("refresh_token"))
		}
		_, _ = w.Write([]byte(`{"access_token":"cpt_new","refresh_token":"cpr_rotated","token_type":"Bearer","expires_in":1209600,"scope":"full"}`))
	}))
	defer server.Close()

	set, err := Refresh(context.Background(), server.URL, "cupthread-cli", "cpr_old")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if set.AccessToken != "cpt_new" || set.RefreshToken != "cpr_rotated" || set.ExpiresIn != 1209600 {
		t.Errorf("set = %+v", set)
	}
}

func TestPostTokenSurfacesOAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code expired"}`))
	}))
	defer server.Close()

	_, err := postToken(context.Background(), server.URL, url.Values{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_grant" {
		t.Fatalf("expected APIError invalid_grant, got %v", err)
	}
}

// TestRevokeSendsClientIdentity pins the issue #144 contract: the revocation
// endpoint authenticates the caller (RFC 7009 §2.1) and answers 400
// invalid_request without a client_id, so Revoke must always send client_id
// alongside the token. The CLI is a public client, so client_id alone must
// suffice — no client_secret on the wire.
func TestRevokeSendsClientIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != RevokePath {
			t.Errorf("revoke path = %q, want %q", r.URL.Path, RevokePath)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.Form.Get("client_id"); got != FirstPartyClientID {
			t.Errorf("client_id = %q, want %q", got, FirstPartyClientID)
		}
		if got := r.Form.Get("token"); got != "cpr_stored" {
			t.Errorf("token = %q, want %q", got, "cpr_stored")
		}
		if got := r.Form.Get("client_secret"); got != "" {
			t.Errorf("client_secret = %q, want none (public client)", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := Revoke(context.Background(), server.URL+RevokePath, FirstPartyClientID, "cpr_stored"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
}

// TestRevokeSurfacesIdentityError pins that a revocation rejected on client
// identity (issue #144: 400 invalid_request / invalid_client, 401
// invalid_client) propagates as an APIError instead of being swallowed, so
// `auth logout --revoke` can warn about it best-effort.
func TestRevokeSurfacesIdentityError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"client_id is required"}`))
	}))
	defer server.Close()

	err := Revoke(context.Background(), server.URL+RevokePath, FirstPartyClientID, "cpr_stored")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_request" {
		t.Fatalf("expected APIError invalid_request, got %v", err)
	}
}

// TestRefreshBoundsStalledTokenEndpoint pins the issue #56 regression: postForm
// used to send via http.DefaultClient, which has no timeout, so an endpoint
// that accepts the connection but never answers hung Refresh — and with it
// every authenticated command — forever.
func TestRefreshBoundsStalledTokenEndpoint(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release) // unblock the handler first so server.Close can finish

	old := httpClient
	httpClient = &http.Client{Timeout: 200 * time.Millisecond}
	defer func() { httpClient = old }()

	done := make(chan error, 1)
	go func() {
		_, err := Refresh(context.Background(), server.URL, "cupthread-cli", "cpr_old")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error from the stalled token endpoint, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Refresh still blocked after 2s against a stalled endpoint (client has no timeout?)")
	}
}

func TestStartDeviceRejectsMissingFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"unsupported_grant_type"}`))
	}))
	defer server.Close()

	if _, err := StartDevice(context.Background(), server.URL, server.URL, "cupthread-cli"); err == nil {
		t.Fatal("expected error when device_code/user_code missing")
	}
}

// devicePollLog records every token-endpoint arrival so tests can assert the
// spacing DeviceStart.Wait actually waited between polls.
type devicePollLog struct {
	mu    sync.Mutex
	times []time.Time
}

func (l *devicePollLog) record() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.times = append(l.times, time.Now())
}

func (l *devicePollLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.times)
}

// spacing returns the gap between poll i and poll i+1 (0-indexed).
func (l *devicePollLog) spacing(i int) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.times[i+1].Sub(l.times[i])
}

// scriptedDeviceTokenServer answers each token poll with the next status;
// the last status repeats once the script is exhausted, so a poller that
// keeps going (e.g. past authorization_pending) has something to receive.
// "success" answers 200 with a token pair; anything else answers 400 with
// the RFC 8628 §3.5 error code.
func scriptedDeviceTokenServer(statuses []string) (*httptest.Server, *devicePollLog) {
	log := &devicePollLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record()
		n := log.count()
		status := statuses[len(statuses)-1]
		if n-1 < len(statuses) {
			status = statuses[n-1]
		}
		if status == "success" {
			_, _ = w.Write([]byte(`{"access_token":"cpt_dev","refresh_token":"cpr_dev","token_type":"Bearer","expires_in":1209600,"scope":"full"}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"` + status + `","error_description":"scripted"}`))
	}))
	return server, log
}

func newDeviceStart(serverURL string) *DeviceStart {
	return &DeviceStart{
		deviceCode: "dc_test",
		tokenURL:   serverURL,
		clientID:   FirstPartyClientID,
		UserCode:   "XXXX-XXXX",
		Interval:   40 * time.Millisecond,
		ExpiresAt:  time.Now().Add(time.Minute),
	}
}

// waitForPolls blocks until the scripted endpoint has recorded n arrivals.
func waitForPolls(t *testing.T, log *devicePollLog, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for log.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("device flow reached only %d/%d polls in 5s", log.count(), n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestDeviceWaitGrowsIntervalOnSlowDown pins the issue #145 contract (RFC
// 8628 §3.5): every slow_down answer must grow the next wait by the 5-second
// penalty, cumulatively — the server enforces the escalated spacing, so a
// poller that ignores slow_down is held at slow_down until the code expires.
func TestDeviceWaitGrowsIntervalOnSlowDown(t *testing.T) {
	old := slowDownPenalty
	slowDownPenalty = 40 * time.Millisecond
	defer func() { slowDownPenalty = old }()

	server, log := scriptedDeviceTokenServer([]string{"authorization_pending", "slow_down", "slow_down", "authorization_pending"})
	defer server.Close()

	d := newDeviceStart(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.Wait(ctx)
		done <- err
	}()

	// Four polls = one pending plus two slow_downs applied and honored.
	waitForPolls(t, log, 4)
	time.Sleep(50 * time.Millisecond) // let Wait process the 4th answer
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context.Canceled", err)
	}

	want := 40*time.Millisecond + 2*slowDownPenalty
	if d.Interval != want {
		t.Errorf("Interval = %v after two slow_downs, want %v (interval + 2 penalties)", d.Interval, want)
	}
	// The wire must show the escalated spacing too: poll 3 waits at least
	// interval + penalty after poll 2, poll 4 at least interval + 2 penalties
	// after poll 3. Lower bounds only — timers can only fire late.
	if min := d.Interval - 2*slowDownPenalty + slowDownPenalty - 5*time.Millisecond; log.spacing(1) < min {
		t.Errorf("spacing after first slow_down = %v, want ≥ %v", log.spacing(1), min)
	}
	if min := 40*time.Millisecond + 2*slowDownPenalty - 5*time.Millisecond; log.spacing(2) < min {
		t.Errorf("spacing after second slow_down = %v, want ≥ %v (cumulative growth)", log.spacing(2), min)
	}
}

// TestDeviceWaitKeepsIntervalWhilePending pins the other half of the issue
// #145 contract: authorization_pending keeps the poller at the advertised
// interval — only slow_down grows it.
func TestDeviceWaitKeepsIntervalWhilePending(t *testing.T) {
	server, log := scriptedDeviceTokenServer([]string{"authorization_pending"})
	defer server.Close()

	d := newDeviceStart(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.Wait(ctx)
		done <- err
	}()

	waitForPolls(t, log, 3)
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context.Canceled", err)
	}

	if d.Interval != 40*time.Millisecond {
		t.Errorf("Interval = %v after 3 authorization_pending answers, want the advertised 40ms unchanged", d.Interval)
	}
}

// TestDeviceWaitStopsOnTerminalErrors pins that access_denied and
// expired_token end the poll instead of looping until the code expires.
func TestDeviceWaitStopsOnTerminalErrors(t *testing.T) {
	cases := map[string]string{
		"access_denied": "authorization denied",
		"expired_token": "device code expired",
	}
	for status, wantMsg := range cases {
		t.Run(status, func(t *testing.T) {
			server, log := scriptedDeviceTokenServer([]string{status})
			defer server.Close()

			d := newDeviceStart(server.URL)
			d.Interval = time.Millisecond
			if _, err := d.Wait(context.Background()); err == nil || err.Error() != wantMsg {
				t.Fatalf("Wait error = %v, want %q", err, wantMsg)
			}
			if log.count() != 1 {
				t.Errorf("terminal %s answered after %d polls, want exactly 1", status, log.count())
			}
		})
	}
}

// TestDeviceWaitReturnsTokenSet pins the happy path: after a pending answer
// the approval issues the token pair exactly once and Wait returns it.
func TestDeviceWaitReturnsTokenSet(t *testing.T) {
	server, log := scriptedDeviceTokenServer([]string{"authorization_pending", "success"})
	defer server.Close()

	d := newDeviceStart(server.URL)
	set, err := d.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if set.AccessToken != "cpt_dev" || set.RefreshToken != "cpr_dev" || set.ExpiresIn != 1209600 {
		t.Errorf("set = %+v", set)
	}
	if log.count() != 2 {
		t.Errorf("flow used %d polls, want 2 (pending, then success)", log.count())
	}
}

// TestDeviceWaitProgressLineStripsTerminalControls pins the issue #183
// stderr contract: the "Waiting for authorization (code …)" progress line
// echoes a server-supplied user code, so it must reach the terminal with
// every terminal control stripped while the visible text survives.
func TestDeviceWaitProgressLineStripsTerminalControls(t *testing.T) {
	server, _ := scriptedDeviceTokenServer([]string{"authorization_pending"})
	defer server.Close()

	d := &DeviceStart{
		deviceCode: "dc_test",
		tokenURL:   server.URL,
		clientID:   FirstPartyClientID,
		UserCode:   "\x1b]8;;https://evil.example\x1b\\CODE\x1b]8;;\x1b\\\u009b31mred\u009b0m",
		Interval:   5 * time.Millisecond,
		ExpiresAt:  time.Now().Add(40 * time.Millisecond),
	}

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	_, waitErr := d.Wait(context.Background())
	os.Stderr = old
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	stderr := string(out)

	if waitErr == nil || waitErr.Error() != "device code expired" {
		t.Fatalf("Wait error = %v, want device code expired", waitErr)
	}
	for _, line := range strings.Split(stderr, "\n") {
		for _, r := range line {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				t.Errorf("stderr contains control rune %U:\n%q", r, stderr)
				break
			}
		}
	}
	if !strings.Contains(stderr, "Waiting for authorization (code ]8;;https://evil.example\\CODE]8;;\\31mred0m)...") {
		t.Errorf("stderr lost the visible code text:\n%q", stderr)
	}
}
