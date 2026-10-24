package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// capturedForm records what a redirect target actually received.
type capturedForm struct {
	hits int
	body string
}

// oauthRedirectFixture starts two servers: a target that records whatever
// arrives, and an origin that answers every POST with the given 3xx pointing
// at locationFor(target). 307 is the class that preserves the method and
// body, i.e. the exact hop that would replay the secret-bearing OAuth form
// to a foreign origin (issue #294).
func oauthRedirectFixture(t *testing.T, status int, locationFor func(target string) string) (origin *httptest.Server, captured *capturedForm) {
	t.Helper()

	captured = &capturedForm{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.hits++
		body, _ := io.ReadAll(r.Body)
		captured.body = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	location := target.URL + "/harvest"
	if locationFor != nil {
		location = locationFor(target.URL)
	}
	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, location, status)
	}))
	t.Cleanup(origin.Close)
	return origin, captured
}

// requireNoCrossOriginDelivery fails when the redirect target saw any
// traffic: the whole point of the policy is that the hop never happens.
func requireNoCrossOriginDelivery(t *testing.T, captured *capturedForm) {
	t.Helper()
	if captured.hits != 0 {
		t.Fatalf("redirect target received %d request(s), body %q; want 0 hops", captured.hits, captured.body)
	}
}

// requireRefusedByPolicy fails unless the error names the redirect policy as
// the reason — distinguishing a deliberate refusal from any other failure.
func requireRefusedByPolicy(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("flow succeeded against a redirecting endpoint; want a policy refusal")
	}
	if !strings.Contains(err.Error(), "refusing to follow redirect") {
		t.Fatalf("err = %v, want a redirect-policy refusal", err)
	}
}

// crossHostLocation renames the target host so the hop changes the hostname
// (127.0.0.1 → localhost) while staying on the same loopback machine: the
// origin strings no longer match, which is the classic leak hop.
func crossHostLocation(target string) string {
	return strings.Replace(target, "127.0.0.1", "localhost", 1) + "/harvest"
}

// TestRefreshRefusesCrossOriginRedirect pins that a 307 from the token
// endpoint never replays the refresh_token form to the redirect target.
func TestRefreshRefusesCrossOriginRedirect(t *testing.T) {
	origin, captured := oauthRedirectFixture(t, http.StatusTemporaryRedirect, crossHostLocation)

	_, err := Refresh(context.Background(), origin.URL, "cupthread-cli", "cpr_secret")
	requireRefusedByPolicy(t, err)
	requireNoCrossOriginDelivery(t, captured)
}

// TestExchangeCodeRefusesCrossOriginRedirect pins the same guarantee for the
// PKCE code exchange, whose form carries the code_verifier.
func TestExchangeCodeRefusesCrossOriginRedirect(t *testing.T) {
	origin, captured := oauthRedirectFixture(t, http.StatusTemporaryRedirect, crossHostLocation)

	_, err := exchangeCode(context.Background(), origin.URL, "cupthread-cli", "auth-code", "http://127.0.0.1:1/callback", "pkce-verifier-secret")
	requireRefusedByPolicy(t, err)
	requireNoCrossOriginDelivery(t, captured)
}

// TestRevokeRefusesCrossOriginRedirect pins that revocation cannot be
// bounced onto a foreign origin carrying the live token.
func TestRevokeRefusesCrossOriginRedirect(t *testing.T) {
	origin, captured := oauthRedirectFixture(t, http.StatusTemporaryRedirect, crossHostLocation)

	err := Revoke(context.Background(), origin.URL, "cupthread-cli", "cpt_live_token")
	requireRefusedByPolicy(t, err)
	requireNoCrossOriginDelivery(t, captured)
}

// TestDeviceWaitRefusesCrossOriginRedirect pins the device-flow poll, whose
// form carries the device_code.
func TestDeviceWaitRefusesCrossOriginRedirect(t *testing.T) {
	origin, captured := oauthRedirectFixture(t, http.StatusTemporaryRedirect, crossHostLocation)

	start := &DeviceStart{
		deviceCode: "dev-code",
		tokenURL:   origin.URL,
		clientID:   "cupthread-cli",
		UserCode:   "ABCD-EFGH",
		Interval:   time.Millisecond,
	}
	_, err := start.Wait(context.Background())
	requireRefusedByPolicy(t, err)
	requireNoCrossOriginDelivery(t, captured)
}

// TestOAuthRefusesSameHostDifferentPortRedirect pins the port nuance: Go's
// built-in credential stripper ignores the port, so a hop that keeps the
// host but moves the port must still be treated as cross-origin. Both
// httptest servers share the 127.0.0.1 host; the default fixture location
// only moves the port.
func TestOAuthRefusesSameHostDifferentPortRedirect(t *testing.T) {
	origin, captured := oauthRedirectFixture(t, http.StatusTemporaryRedirect, nil)

	_, err := Refresh(context.Background(), origin.URL, "cupthread-cli", "cpr_secret")
	requireRefusedByPolicy(t, err)
	requireNoCrossOriginDelivery(t, captured)
}

// TestOAuthRefusesSchemeChangingRedirect pins that a redirect onto a
// different scheme — the https→http downgrade in particular — is refused.
// The Location here points nowhere reachable on purpose: the policy must
// refuse client-side before any dial is attempted.
func TestOAuthRefusesSchemeChangingRedirect(t *testing.T) {
	origin, captured := oauthRedirectFixture(t, http.StatusTemporaryRedirect,
		func(string) string { return "https://127.0.0.1:1/harvest" })

	_, err := Refresh(context.Background(), origin.URL, "cupthread-cli", "cpr_secret")
	requireRefusedByPolicy(t, err)
	requireNoCrossOriginDelivery(t, captured)
}

// TestOAuthFollowsSameOriginRedirect is the positive control: a hop that
// stays on the dialed origin is still legitimate and must complete with the
// form intact.
func TestOAuthFollowsSameOriginRedirect(t *testing.T) {
	var posts int
	var origin *httptest.Server
	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		if posts == 1 {
			http.Redirect(w, r, origin.URL+"/token", http.StatusTemporaryRedirect)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.Form.Get("refresh_token") != "cpr_old" {
			t.Errorf("refresh_token = %q after the same-origin hop, want cpr_old", r.Form.Get("refresh_token"))
		}
		_, _ = w.Write([]byte(`{"access_token":"cpt_new","refresh_token":"cpr_rotated","token_type":"Bearer","expires_in":1209600,"scope":"full"}`))
	}))
	defer origin.Close()

	set, err := Refresh(context.Background(), origin.URL, "cupthread-cli", "cpr_old")
	if err != nil {
		t.Fatalf("Refresh across a same-origin redirect: %v", err)
	}
	if set.AccessToken != "cpt_new" {
		t.Errorf("AccessToken = %q, want cpt_new", set.AccessToken)
	}
	if posts != 2 {
		t.Errorf("origin saw %d requests, want 2 (the 307 and the replayed POST)", posts)
	}
}
