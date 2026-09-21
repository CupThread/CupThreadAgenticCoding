package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// allowedRedirectURI mirrors the authorization server's redirect-URI scheme
// policy (SEC-46: isAllowedRedirectUri in the SaaS shared package): absolute
// https: URLs without userinfo or fragment, or loopback http://127.0.0.1 /
// http://[::1] / http://localhost with any port. Go's net/url parses a
// slightly different grammar than the server's WHATWG URL, but the mirror
// accepts and rejects every vector the server's own SEC-46 suite pins.
func allowedRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.User != nil || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return u.Host != ""
	}
	if u.Scheme == "http" {
		switch strings.ToLower(u.Hostname()) {
		case "127.0.0.1", "::1", "localhost":
			return u.Host != ""
		}
	}
	return false
}

// TestRedirectURISchemePolicyVectors pins the accept/reject sets documented by
// the server's SEC-46 contract so a future CLI change that moves the callback
// off loopback (or onto a custom scheme) fails here with a pointer at the
// policy instead of a cryptic 400 invalid_request from the authorize endpoint.
func TestRedirectURISchemePolicyVectors(t *testing.T) {
	allowed := []string{
		"https://example.com/callback",
		"https://example.com:8443/callback?app=cli", // query strings are fine
		"http://127.0.0.1/cupthread/callback",
		"http://127.0.0.1:49152/cupthread/callback",
		"http://localhost:8321/cb",
		"http://[::1]:8321/cb",
	}
	rejected := []string{
		"javascript:alert(1)",
		"data:text/html,<script>x</script>",
		"file:///etc/passwd",
		"vbscript:msgbox(1)",
		"myapp://callback", // custom app schemes
		"http://evil.example/cupthread/callback",
		"http://localhost.evil.com/cb",
		"//evil.example/cb", // protocol-relative
		"https://user:pass@example.com/cb",
		"https://example.com/cb#fragment",
		"/relative/callback",
		"",
	}
	for _, raw := range allowed {
		if !allowedRedirectURI(raw) {
			t.Errorf("allowedRedirectURI(%q) = false, want true", raw)
		}
	}
	for _, raw := range rejected {
		if allowedRedirectURI(raw) {
			t.Errorf("allowedRedirectURI(%q) = true, want false", raw)
		}
	}
}

// TestLoopbackRedirectURIPortsAllowed pins that every port a local listener
// could pick yields a redirect_uri the authorization server accepts.
func TestLoopbackRedirectURIPortsAllowed(t *testing.T) {
	for _, port := range []int{1, 80, 8321, 49152, 65535} {
		uri := loopbackRedirectURI(port)
		if !allowedRedirectURI(uri) {
			t.Fatalf("loopbackRedirectURI(%d) = %q violates the SEC-46 redirect-URI policy", port, uri)
		}
	}
	if got, want := loopbackRedirectURI(49152), "http://127.0.0.1:49152"+CallbackPath; got != want {
		t.Fatalf("loopbackRedirectURI(49152) = %q, want %q", got, want)
	}
}

// TestLoginPKCELoopbackRedirectEndToEnd drives the whole browser flow against
// stubs and pins the wire contract: the authorize URL carries a
// policy-compliant loopback redirect_uri, the simulated browser callback
// delivers the code to that same loopback URL, and the token exchange echoes
// the identical redirect_uri (the server rejects mismatches).
func TestLoginPKCELoopbackRedirectEndToEnd(t *testing.T) {
	var authorizeRedirectURI string

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token form: %v", err)
		}
		if got := r.Form.Get("redirect_uri"); got != authorizeRedirectURI {
			t.Errorf("token exchange redirect_uri = %q, want the authorize-time %q", got, authorizeRedirectURI)
		}
		if !allowedRedirectURI(r.Form.Get("redirect_uri")) {
			t.Errorf("token exchange redirect_uri %q violates the SEC-46 policy", r.Form.Get("redirect_uri"))
		}
		_, _ = w.Write([]byte(`{"access_token":"cpt_new","refresh_token":"cpr_rotated","token_type":"Bearer","expires_in":1209600,"scope":"full"}`))
	}))
	defer tokenServer.Close()

	openBrowser := func(authorizeURL string) error {
		u, err := url.Parse(authorizeURL)
		if err != nil {
			return err
		}
		q := u.Query()
		if got := q.Get("response_type"); got != "code" {
			t.Errorf("response_type = %q, want code", got)
		}
		if got := q.Get("client_id"); got != FirstPartyClientID {
			t.Errorf("client_id = %q, want %q", got, FirstPartyClientID)
		}
		if got := q.Get("code_challenge_method"); got != "S256" {
			t.Errorf("code_challenge_method = %q, want S256", got)
		}
		authorizeRedirectURI = q.Get("redirect_uri")
		if !allowedRedirectURI(authorizeRedirectURI) {
			t.Errorf("authorize redirect_uri %q violates the SEC-46 policy", authorizeRedirectURI)
		}
		// Simulate the consent page redirecting back to the loopback callback.
		resp, err := http.Get(authorizeRedirectURI + "?code=ac_123&state=" + url.QueryEscape(q.Get("state")))
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}

	set, err := LoginPKCE(context.Background(), "http://authorize.invalid"+AuthorizePath, tokenServer.URL, FirstPartyClientID, openBrowser)
	if err != nil {
		t.Fatalf("LoginPKCE: %v", err)
	}
	if set.AccessToken != "cpt_new" || set.RefreshToken != "cpr_rotated" {
		t.Errorf("set = %+v", set)
	}
}
