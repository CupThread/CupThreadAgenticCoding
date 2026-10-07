package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newTestCallbackServer mounts a fresh one-transaction callback handler on an
// httptest server and returns the server plus its result channels (each with
// capacity 1, mirroring the wiring in LoginPKCE).
func newTestCallbackServer(t *testing.T, state string) (*httptest.Server, <-chan string, <-chan error) {
	t.Helper()
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	server := httptest.NewServer(newCallbackHandler(state, codeCh, errCh))
	t.Cleanup(server.Close)
	return server, codeCh, errCh
}

// getCallback issues one simulated-browser callback request and returns the
// response with its body fully read.
func getCallback(t *testing.T, server *httptest.Server, query url.Values) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(server.URL + CallbackPath + "?" + query.Encode())
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp, b.String()
}

// TestCallbackErrorParameterIsNeverReflected is the SEC-1 regression: the
// callback used to interpolate the authorization-error query parameter into
// the response before validating state, so any local process could render
// markup in the browser during login. The response must now be static plain
// text regardless of the payload, and the terminal-side error must not carry
// the raw payload either.
func TestCallbackErrorParameterIsNeverReflected(t *testing.T) {
	const state = "st_known_only_to_the_login"
	payloads := []string{
		`<script>alert(document.domain)</script>`,
		`"><img src=x onerror=alert(1)>`,
		"\x1b]0;window title pwned\x07\x1b[31mterminal escape\x1b[0m",
		"access_denied\x00\r\nSet-Cookie: pwned=1",
		strings.Repeat("A", 5000),
	}
	for _, payload := range payloads {
		server, _, errCh := newTestCallbackServer(t, state)
		resp, body := getCallback(t, server, url.Values{"error": {payload}, "state": {state}})

		if got := resp.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Errorf("payload %q: Content-Type = %q, want text/plain; charset=utf-8", payload, got)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("payload %q: missing X-Content-Type-Options: nosniff", payload)
		}
		want := "Authorization was declined. You can close this window.\n"
		if body != want {
			t.Errorf("payload %q: body = %q, want the static decline text %q", payload, body, want)
		}
		if strings.ContainsAny(body, "<>\x00\x07\x1b") {
			t.Errorf("payload %q: body contains markup or control characters: %q", payload, body)
		}

		select {
		case err := <-errCh:
			if strings.ContainsAny(err.Error(), "<>\x00\x07\x1b") {
				t.Errorf("payload %q: returned error contains markup or control characters: %q", payload, err)
			}
			if strings.Contains(err.Error(), strings.Repeat("A", 100)) {
				t.Errorf("payload %q: returned error carries the raw payload: %q", payload, err)
			}
		default:
			t.Errorf("payload %q: expected the login to fail with a decline error", payload)
		}
	}
}

// TestCallbackRejectsUnknownStateBeforeErrorBranch pins the ordering fix: a
// callback that does not present the secret state is rejected before the
// error branch is consulted, so an unsolicited ?error= can neither trigger
// the decline response nor poison the login.
func TestCallbackRejectsUnknownStateBeforeErrorBranch(t *testing.T) {
	const state = "st_real"
	for name, query := range map[string]url.Values{
		"wrong state":   {"error": {"access_denied"}, "state": {"st_forged"}},
		"missing state": {"error": {"access_denied"}},
	} {
		server, _, errCh := newTestCallbackServer(t, state)
		resp, body := getCallback(t, server, query)

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Errorf("%s: Content-Type = %q, want text/plain; charset=utf-8", name, got)
		}
		if !strings.Contains(body, "State mismatch") || strings.Contains(body, "declined") {
			t.Errorf("%s: body = %q, want the static state-mismatch text only", name, body)
		}
		select {
		case err := <-errCh:
			if err == nil || err.Error() != "oauth state mismatch" {
				t.Errorf("%s: err = %v, want oauth state mismatch", name, err)
			}
		default:
			t.Errorf("%s: expected the state mismatch to fail the login", name)
		}
	}
}

// TestCallbackHappyPathUnchanged proves the hardening did not disturb the
// success contract: a valid state plus authorization code still answers 200
// and delivers the code.
func TestCallbackHappyPathUnchanged(t *testing.T) {
	const state = "st_ok"
	server, codeCh, errCh := newTestCallbackServer(t, state)
	resp, body := getCallback(t, server, url.Values{"code": {"ac_123"}, "state": {state}})

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", got)
	}
	if !strings.Contains(body, "Logged in") {
		t.Errorf("body = %q, want the success message", body)
	}
	select {
	case code := <-codeCh:
		if code != "ac_123" {
			t.Errorf("delivered code = %q, want ac_123", code)
		}
	default:
		t.Error("expected the code to be delivered")
	}
	select {
	case err := <-errCh:
		t.Errorf("unexpected error on the happy path: %v", err)
	default:
	}
}

// TestCallbackCompletionIsSingleShot pins that only the first callback
// outcome counts: later requests get a static already-completed answer and
// can neither block on the buffered result channels nor replace or duplicate
// the recorded outcome.
func TestCallbackCompletionIsSingleShot(t *testing.T) {
	const state = "st_once"

	t.Run("second request after a completed login", func(t *testing.T) {
		server, codeCh, errCh := newTestCallbackServer(t, state)
		resp, body := getCallback(t, server, url.Values{"code": {"ac_first"}, "state": {state}})
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Logged in") {
			t.Fatalf("first request: status %d body %q, want a 200 success", resp.StatusCode, body)
		}

		// A duplicate or forged late arrival must be answered without
		// touching the channels again.
		for name, query := range map[string]url.Values{
			"another valid code": {"code": {"ac_second"}, "state": {state}},
			"forged state":       {"code": {"ac_second"}, "state": {"st_forged"}},
			"error injection":    {"error": {"<script>x</script>"}, "state": {state}},
		} {
			_, body := getCallback(t, server, query)
			if !strings.Contains(body, "already completed") {
				t.Errorf("%s: body = %q, want the static already-completed text", name, body)
			}
		}

		select {
		case code := <-codeCh:
			if code != "ac_first" {
				t.Errorf("delivered code = %q, want the first code ac_first", code)
			}
		default:
			t.Error("expected exactly one delivered code")
		}
		select {
		case err := <-errCh:
			t.Errorf("unexpected error after a successful login: %v", err)
		default:
		}
	})

	t.Run("first outcome wins and is never replaced", func(t *testing.T) {
		server, codeCh, errCh := newTestCallbackServer(t, state)
		// A local attacker without the state races ahead of the browser.
		resp, body := getCallback(t, server, url.Values{"error": {"access_denied"}, "state": {"st_forged"}})
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "State mismatch") {
			t.Fatalf("attacker request: status %d body %q, want a 400 state mismatch", resp.StatusCode, body)
		}

		// The legitimate browser redirect arrives afterwards.
		resp, body = getCallback(t, server, url.Values{"code": {"ac_real"}, "state": {state}})
		if !strings.Contains(body, "already completed") {
			t.Errorf("legitimate late request: body = %q, want the static already-completed text", body)
		}

		select {
		case err := <-errCh:
			if err.Error() != "oauth state mismatch" {
				t.Errorf("err = %v, want the first outcome (oauth state mismatch)", err)
			}
		default:
			t.Error("expected the first outcome (state mismatch) to be delivered")
		}
		select {
		case code := <-codeCh:
			t.Errorf("unexpected code delivered after completion: %q", code)
		default:
		}
	})
}

// TestCallbackMissingCodeWithValidState keeps the malformed-but-authenticated
// callback on its dedicated error path.
func TestCallbackMissingCodeWithValidState(t *testing.T) {
	server, codeCh, errCh := newTestCallbackServer(t, "st_nocode")
	resp, body := getCallback(t, server, url.Values{"state": {"st_nocode"}})

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(body, "Missing code parameter") {
		t.Errorf("body = %q, want the missing-code message", body)
	}
	select {
	case err := <-errCh:
		if err.Error() != "callback missing code" {
			t.Errorf("err = %v, want callback missing code", err)
		}
	default:
		t.Error("expected the missing code to fail the login")
	}
	select {
	case code := <-codeCh:
		t.Errorf("unexpected code delivered: %q", code)
	default:
	}
}

// TestLoginPKCEIgnoresUnsolicitedCallbackError drives the full login with the
// forged-callback attack from the issue: a local process hits the loopback
// listener before the real browser redirect, with attacker-chosen error text
// and no knowledge of the state. The login must fail closed with the state
// mismatch — not render the payload — and the token endpoint must never be
// contacted.
func TestLoginPKCEIgnoresUnsolicitedCallbackError(t *testing.T) {
	tokenHit := false
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenHit = true
		_, _ = w.Write([]byte(`{"access_token":"cpt_new","token_type":"Bearer","expires_in":1209600}`))
	}))
	defer tokenServer.Close()

	openBrowser := func(authorizeURL string) error {
		u, err := url.Parse(authorizeURL)
		if err != nil {
			return err
		}
		redirectURI := u.Query().Get("redirect_uri")
		// The attack: no state, hostile markup in error.
		resp, err := http.Get(redirectURI + "?error=" + url.QueryEscape(`<script>alert(document.domain)</script>`))
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}

	_, err := LoginPKCE(context.Background(), "http://authorize.invalid"+AuthorizePath, tokenServer.URL, FirstPartyClientID, openBrowser)
	if err == nil {
		t.Fatal("LoginPKCE succeeded despite an unsolicited forged callback")
	}
	if !strings.Contains(err.Error(), "state mismatch") {
		t.Errorf("err = %v, want the state mismatch failure", err)
	}
	if strings.ContainsAny(err.Error(), "<>") {
		t.Errorf("err = %v, want no reflected markup", err)
	}
	if tokenHit {
		t.Error("token endpoint was contacted despite the forged callback")
	}
}

// TestSanitizeOAuthErrorCode pins the terminal-safety reduction applied to
// server-supplied OAuth error codes.
func TestSanitizeOAuthErrorCode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"access_denied", "access_denied"},
		{"temporarily_unavailable", "temporarily_unavailable"},
		{"", ""},
		{"<script>alert(document.domain)</script>", "scriptalertdocument.domainscript"},
		{"\x1b[31mred\x1b[0m", "31mred0m"},
		{"a b c", "abc"},
		{strings.Repeat("x", 100), strings.Repeat("x", 64)},
	}
	for _, tc := range cases {
		if got := sanitizeOAuthErrorCode(tc.in); got != tc.want {
			t.Errorf("sanitizeOAuthErrorCode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
