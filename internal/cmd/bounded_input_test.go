package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Tests for the client-side request-body caps (issue #142): every file/stdin
// input is read through a bounded reader against the route's SEC-36 limit, so
// oversized local input fails deterministically in-process — before any HTTP
// request is sent — instead of buffering an unbounded pipe or file.

// padBodyJSON builds a strict one-value JSON body {"pad":"xxx…"} of exactly
// size bytes (size >= 10).
func padBodyJSON(size int) string {
	return `{"pad":"` + strings.Repeat("x", size-10) + `"}`
}

// countingServer returns an httptest server that counts every request it
// observes, so tests can assert an oversized input never reached the network.
func countingServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	sent := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent++
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	return server, &sent
}

// TestReadBounded pins the reader semantics: input at or below the cap passes
// through unchanged, one byte over fails with InputTooLargeError, and the
// source is abandoned right after the cap is crossed instead of being drained.
func TestReadBounded(t *testing.T) {
	out, err := readBounded(strings.NewReader("012345678"), 10)
	if err != nil || string(out) != "012345678" {
		t.Fatalf("below-cap read = %q, %v", out, err)
	}

	out, err = readBounded(strings.NewReader("0123456789"), 10)
	if err != nil || string(out) != "0123456789" {
		t.Fatalf("at-cap read = %q, %v", out, err)
	}

	_, err = readBounded(strings.NewReader("0123456789A"), 10)
	var tooLarge *InputTooLargeError
	if !asInputTooLarge(err, &tooLarge) {
		t.Fatalf("over-cap read err = %v, want InputTooLargeError", err)
	}
	if tooLarge.Limit != 10 {
		t.Errorf("limit = %d, want 10", tooLarge.Limit)
	}
	if want := "input exceeds the 10 B request-body limit"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}

	// The bounded read consumes at most cap+1 bytes even from an endless
	// source: a huge pipe must be cut off, not buffered to exhaustion.
	endless := &endlessReader{}
	_, err = readBounded(endless, 16)
	if !asInputTooLarge(err, &tooLarge) {
		t.Fatalf("endless source err = %v, want InputTooLargeError", err)
	}
	if endless.consumed != 17 {
		t.Errorf("consumed = %d bytes, want exactly cap+1 = 17", endless.consumed)
	}
}

// endlessReader never returns EOF; it just counts how many bytes a reader
// pulled from it.
type endlessReader struct{ consumed int }

func (r *endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.consumed += len(p)
	return len(p), nil
}

func asInputTooLarge(err error, target **InputTooLargeError) bool {
	e, ok := err.(*InputTooLargeError)
	if ok {
		*target = e
	}
	return ok
}

func TestHumanBytes(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{maxConsoleBodyBytes, "1 MB"},
		{maxPublicBodyBytes, "256 KB"},
		{maxSecretBytes, "64 KB"},
		{5, "5 B"},
	} {
		if got := humanBytes(tc.n); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestBodyLimitForPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want int64
	}{
		{"/api/v1/public/apps/app_1/user", maxPublicBodyBytes},
		{"/api/v1/console/me", maxConsoleBodyBytes},
		{"/api/v1/feature-requests", maxConsoleBodyBytes},
	} {
		if got := bodyLimitForPath(tc.path); got != tc.want {
			t.Errorf("bodyLimitForPath(%q) = %d, want %d", tc.path, got, tc.want)
		}
	}
}

// TestAPIRequestInputOverLimitFailsBeforeSending covers the headline contract:
// a console-route body over 1 MB fails in-process with a structured
// input_too_large document, and the test server observes no request at all.
func TestAPIRequestInputOverLimitFailsBeforeSending(t *testing.T) {
	server, sent := countingServer(t)

	body := padBodyJSON(int(maxConsoleBodyBytes) + 1)
	out, err := runRoot(t, server.URL, "api", "request", "POST", "/api/v1/console/me",
		"--input", writeInputFile(t, body), "--json")
	if err == nil {
		t.Fatal("api request succeeded with an over-limit body, want a local failure")
	}
	if *sent != 0 {
		t.Errorf("server observed %d requests, want 0 — the body must fail before sending", *sent)
	}
	if !strings.Contains(err.Error(), "input exceeds the 1 MB request-body limit") {
		t.Errorf("error = %q, want it to name the 1 MB cap", err.Error())
	}
	var payload struct {
		Code  string `json:"code"`
		Limit int64  `json:"limit"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("stdout %q is not one JSON document: %v", out, err)
	}
	if payload.Code != "input_too_large" || payload.Limit != maxConsoleBodyBytes {
		t.Errorf("payload = %+v, want code input_too_large with limit %d", payload, maxConsoleBodyBytes)
	}
}

// TestAPIRequestInputOverLimitTableKeepsStdoutClean checks the table-mode
// counterpart: the human error goes to stderr and stdout stays parse-clean.
func TestAPIRequestInputOverLimitTableKeepsStdoutClean(t *testing.T) {
	server, sent := countingServer(t)

	body := padBodyJSON(int(maxConsoleBodyBytes) + 1)
	out, err := runRoot(t, server.URL, "api", "request", "POST", "/api/v1/console/me",
		"--input", writeInputFile(t, body))
	if err == nil {
		t.Fatal("api request succeeded with an over-limit body, want a local failure")
	}
	if *sent != 0 {
		t.Errorf("server observed %d requests, want 0", *sent)
	}
	if out != "" {
		t.Errorf("table mode stdout = %q, want it empty (detail belongs on stderr)", out)
	}
}

// TestAPIRequestInputAtLimitSendsUnchanged proves the boundary is inclusive:
// a body exactly at the cap — and one byte below — is buffered whole and sent
// byte-for-byte (issue #75's numeric passthrough must survive the bound).
func TestAPIRequestInputAtLimitSendsUnchanged(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	for _, size := range []int{int(maxConsoleBodyBytes) - 1, int(maxConsoleBodyBytes)} {
		var gotBody string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read request body: %v", err)
			}
			gotBody = string(data)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		t.Cleanup(server.Close)

		body := padBodyJSON(size)
		out, err := runRoot(t, server.URL, "api", "request", "POST", "/api/v1/console/me",
			"--input", writeInputFile(t, body), "--json")
		if err != nil {
			t.Fatalf("api request with %d-byte body: %v", size, err)
		}
		if gotBody != body {
			t.Errorf("%d-byte body was rewritten on the wire (got %d bytes)", size, len(gotBody))
		}
		if !strings.Contains(out, `"ok": true`) {
			t.Errorf("stdout = %q, want the response payload", out)
		}
	}
}

// TestAPIRequestPublicRouteUsesPublicLimit pins the route split: bodies bound
// for /api/v1/public/… are capped at 256 KB, not the console 1 MB.
func TestAPIRequestPublicRouteUsesPublicLimit(t *testing.T) {
	server, sent := countingServer(t)

	body := padBodyJSON(int(maxPublicBodyBytes) + 1)
	out, err := runRoot(t, server.URL, "api", "request", "POST", "/api/v1/public/apps/app_1/user",
		"--input", writeInputFile(t, body), "--json")
	if err == nil {
		t.Fatal("api request succeeded with an over-limit public body, want a local failure")
	}
	if *sent != 0 {
		t.Errorf("server observed %d requests, want 0", *sent)
	}
	var payload struct {
		Limit int64 `json:"limit"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("stdout %q is not one JSON document: %v", out, err)
	}
	if payload.Limit != maxPublicBodyBytes {
		t.Errorf("limit = %d, want the public 256 KB cap %d", payload.Limit, maxPublicBodyBytes)
	}
}

// TestSettingsSetInputOverLimitFailsBeforeRequest covers `apps settings set
// --input`: the oversized body fails locally before even the app lookup goes
// out, so no request is observed.
func TestSettingsSetInputOverLimitFailsBeforeRequest(t *testing.T) {
	server, sent := countingServer(t)

	body := padBodyJSON(int(maxConsoleBodyBytes) + 1)
	_, err := runRoot(t, server.URL, "apps", "settings", "set", "app_1",
		"--workspace", "ws_1", "--input", writeInputFile(t, body), "--json")
	if err == nil {
		t.Fatal("apps settings set succeeded with an over-limit body, want a local failure")
	}
	if *sent != 0 {
		t.Errorf("server observed %d requests, want 0 — the input read must precede the lookup", *sent)
	}
}

// TestImportsOptionsOverLimitFailsBeforeRequest covers `imports create
// --options` the same way.
func TestImportsOptionsOverLimitFailsBeforeRequest(t *testing.T) {
	server, sent := countingServer(t)

	body := padBodyJSON(int(maxConsoleBodyBytes) + 1)
	_, err := runRoot(t, server.URL, "imports", "create",
		"--workspace", "ws_1", "--app", "app_1", "--source", "github_issues",
		"--options", writeInputFile(t, body), "--json")
	if err == nil {
		t.Fatal("imports create succeeded with an over-limit options body, want a local failure")
	}
	if *sent != 0 {
		t.Errorf("server observed %d requests, want 0", *sent)
	}
}

// TestChangelogBodyFileOverLimitFailsBeforeRequest covers the changelog
// markdown body, from both a file and stdin ("-" / "@").
func TestChangelogBodyFileOverLimitFailsBeforeRequest(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		server, sent := countingServer(t)

		huge := strings.Repeat("# line\n", int(maxConsoleBodyBytes/7)+1)
		_, err := runRoot(t, server.URL, "changelog", "create", "--title", "t",
			"--body-file", writeInputFile(t, huge))
		if err == nil {
			t.Fatal("changelog create succeeded with an over-limit body file, want a local failure")
		}
		if *sent != 0 {
			t.Errorf("server observed %d requests, want 0", *sent)
		}
	})

	t.Run("stdin", func(t *testing.T) {
		server, sent := countingServer(t)

		huge := strings.Repeat("# line\n", int(maxConsoleBodyBytes/7)+1)
		_, err := runRootWithStdin(t, server.URL, huge, "changelog", "create", "--title", "t",
			"--body-file", "-")
		if err == nil {
			t.Fatal("changelog create succeeded with an over-limit stdin body, want a local failure")
		}
		if *sent != 0 {
			t.Errorf("server observed %d requests, want 0 — stdin must not bypass the cap", *sent)
		}
	})
}

// TestSignUserAttrsInputsBounded covers both sign-user-attrs inputs: the body
// is capped at the public route's 256 KB, the piped signing secret at 64 KB.
func TestSignUserAttrsInputsBounded(t *testing.T) {
	t.Run("body over 256 KB", func(t *testing.T) {
		server, sent := countingServer(t)

		body := padBodyJSON(int(maxPublicBodyBytes) + 1)
		_, err := runRoot(t, server.URL, "api", "sign-user-attrs", "--app-key", "app_1",
			"--input", writeInputFile(t, body), "--secret", "s", "--json")
		if err == nil {
			t.Fatal("sign-user-attrs succeeded with an over-limit body, want a local failure")
		}
		if *sent != 0 {
			t.Errorf("server observed %d requests, want 0", *sent)
		}
	})

	t.Run("secret over 64 KB", func(t *testing.T) {
		server, sent := countingServer(t)

		_, err := runRootWithStdin(t, server.URL, strings.Repeat("s", int(maxSecretBytes)+1),
			"api", "sign-user-attrs", "--app-key", "app_1",
			"--input", writeInputFile(t, `{"userToken":"u1"}`), "--secret", "-", "--json")
		if err == nil {
			t.Fatal("sign-user-attrs succeeded with an over-limit secret, want a local failure")
		}
		if *sent != 0 {
			t.Errorf("server observed %d requests, want 0", *sent)
		}
	})
}

// TestInputTooLargeYAMLIsOneDocument pins the structured contract in YAML
// mode: stdout carries exactly one parseable document, nothing else.
func TestInputTooLargeYAMLIsOneDocument(t *testing.T) {
	server, sent := countingServer(t)

	huge := strings.Repeat("# line\n", int(maxConsoleBodyBytes/7)+1)
	out, err := runRoot(t, server.URL, "changelog", "create", "--title", "t",
		"--body-file", writeInputFile(t, huge), "-o", "yaml")
	if err == nil {
		t.Fatal("changelog create succeeded with an over-limit body file, want a local failure")
	}
	if *sent != 0 {
		t.Errorf("server observed %d requests, want 0", *sent)
	}
	dec := yaml.NewDecoder(strings.NewReader(out))
	var doc struct {
		Error string `yaml:"error"`
		Code  string `yaml:"code"`
		Limit int64  `yaml:"limit"`
	}
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode stdout %q as YAML: %v", out, err)
	}
	var extra map[string]any
	if err := dec.Decode(&extra); err != io.EOF {
		t.Errorf("stdout carried a second document after %v: %v", doc, err)
	}
	if doc.Code != "input_too_large" || doc.Limit != maxConsoleBodyBytes {
		t.Errorf("document = %+v, want code input_too_large with limit %d", doc, maxConsoleBodyBytes)
	}
}

// TestLoginTokenStdinOverSecretCapFailsBeforeSending covers the piped
// credential contract for `auth login --token -` (issue #189): a newline-free
// stream over the 64 KB secret cap — the exact shape that used to buffer
// stdin without bound — fails locally with the structured input_too_large
// document, and the /console/me probe observes zero requests.
func TestLoginTokenStdinOverSecretCapFailsBeforeSending(t *testing.T) {
	server, sent := countingServer(t)

	oversized := strings.Repeat("x", int(maxSecretBytes)+1) // no newline anywhere
	out, err := runRootWithStdin(t, server.URL, oversized, "auth", "login", "--token", "-", "--json")
	if err == nil {
		t.Fatal("auth login succeeded with an over-limit piped token, want a local failure")
	}
	if *sent != 0 {
		t.Errorf("server observed %d requests, want 0 — the token read must precede the probe", *sent)
	}
	if !strings.Contains(err.Error(), "input exceeds the 64 KB request-body limit") {
		t.Errorf("error = %q, want it to name the 64 KB cap", err.Error())
	}
	var payload struct {
		Code  string `json:"code"`
		Limit int64  `json:"limit"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("stdout %q is not one JSON document: %v", out, err)
	}
	if payload.Code != "input_too_large" || payload.Limit != maxSecretBytes {
		t.Errorf("payload = %+v, want code input_too_large with limit %d", payload, maxSecretBytes)
	}
}

// TestPipedCredentialFlagsShareSecretCap asserts the 64 KB cap uniformly
// across every piped-credential flag: `auth login --token -`,
// `api sign-user-attrs --secret -` and `integrations github connect --token
// -` all fail locally before any request when stdin carries one byte over
// the cap (issue #189's uniformity contract).
func TestPipedCredentialFlagsShareSecretCap(t *testing.T) {
	oversized := strings.Repeat("x", int(maxSecretBytes)+1)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"auth login --token -", []string{"auth", "login", "--token", "-"}},
		{"api sign-user-attrs --secret -", []string{"api", "sign-user-attrs",
			"--app-key", "app_1", "--input", writeInputFile(t, `{"userToken":"u1"}`), "--secret", "-"}},
		{"integrations github connect --token -", []string{"integrations", "github", "connect", "--token", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, sent := countingServer(t)

			_, err := runRootWithStdin(t, server.URL, oversized, append(tc.args, "--json")...)
			if err == nil {
				t.Fatal("command succeeded with an over-limit piped secret, want a local failure")
			}
			if *sent != 0 {
				t.Errorf("server observed %d requests, want 0", *sent)
			}
			var tooLarge *InputTooLargeError
			if !asInputTooLarge(err, &tooLarge) || tooLarge.Limit != maxSecretBytes {
				t.Errorf("err = %v, want InputTooLargeError with limit %d", err, maxSecretBytes)
			}
		})
	}
}
