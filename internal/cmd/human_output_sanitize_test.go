package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostileOSC8URL is the issue #183 attack payload: a protocol-legal JSON
// string value that decodes into an OSC 8 hyperlink to evil.example labeled
// "CupThread Security" followed by red SGR text, a forged success line via CR,
// and the C1 (ESC-free) form of the same sequences.
const hostileOSC8URL = "\x1b]8;;https://evil.example/verify\x1b\\CupThread Security\x1b]8;;\x1b\\" +
	" \x1b[31mACCOUNT COMPROMISED\x1b[0m\r✓ Forged success line\u009b31mC1 SGR\u009b0m"

// assertNoControlBytesStrict fails when s contains any C0 control (tab and
// newline included — the strict human-line contract), DEL, or C1 control
// outside the line separators the CLI itself appends.
func assertNoControlBytesStrict(t *testing.T, s string) {
	t.Helper()
	for _, line := range strings.Split(s, "\n") {
		for _, r := range line {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				t.Errorf("output line contains control rune %U:\n%q", r, s)
				return
			}
		}
	}
}

// TestBillingCheckoutAndPortalURLSanitized pins the issue #183 human-output
// contract: a hostile checkout/portal URL printed by the Printf human-line
// path reaches the terminal with zero control bytes, while --json output
// carries the URL byte-faithfully for programmatic consumers.
func TestBillingCheckoutAndPortalURLSanitized(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	cases := []struct {
		name       string
		path       string
		response   string
		cmd        []string
		jsonField  string
		visibleURL string
	}{
		{
			name:       "checkout",
			path:       "/api/v1/console/workspaces/ws_1/billing/checkout",
			response:   `{"id":"cs_1","workspaceId":"ws_1","tier":"pro","status":"open","checkoutUrl":` + mustQuoteJSON(t, hostileOSC8URL) + `,"polarConfigured":true}`,
			cmd:        []string{"billing", "checkout", "--workspace", "ws_1"},
			jsonField:  "checkoutUrl",
			visibleURL: "]8;;https://evil.example/verify\\CupThread Security",
		},
		{
			name:       "portal",
			path:       "/api/v1/console/workspaces/ws_1/billing/portal",
			response:   `{"portalUrl":` + mustQuoteJSON(t, hostileOSC8URL) + `,"polarConfigured":true}`,
			cmd:        []string{"billing", "portal", "--workspace", "ws_1"},
			jsonField:  "portalUrl",
			visibleURL: "]8;;https://evil.example/verify\\CupThread Security",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()

			out, err := runRoot(t, server.URL, tc.cmd...)
			if err != nil {
				t.Fatalf("%v: %v", tc.cmd, err)
			}
			assertNoControlBytesStrict(t, out)
			if !strings.Contains(out, tc.visibleURL) {
				t.Errorf("human output lost the visible URL text:\n%q", out)
			}
			if !strings.Contains(out, "Forged success line31mC1 SGR") {
				t.Errorf("human output lost the visible forged-line text:\n%q", out)
			}

			// Structured mode stays byte-faithful: the hostile value survives
			// the round-trip exactly (encoding/json escapes the controls).
			out, err = runRoot(t, server.URL, append(tc.cmd, "--json")...)
			if err != nil {
				t.Fatalf("%v --json: %v", tc.cmd, err)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
				t.Fatalf("decode --json output %q: %v", out, err)
			}
			if got, _ := payload[tc.jsonField].(string); got != hostileOSC8URL {
				t.Errorf("--json %s = %q, want the URL byte-faithful", tc.jsonField, got)
			}
		})
	}
}

// TestAppsCreateAppKeyAndIconLinesSanitized covers the apps surfaces from
// issue #183: the app-key line of `apps create` and the icon-URL line of
// `apps update --icon` interpolate server-derived strings into human lines,
// so both must render with zero control bytes.
func TestAppsCreateAppKeyAndIconLinesSanitized(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	t.Run("apps create app-key line", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/apps" {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"appId":"app_1","appKey":` + mustQuoteJSON(t, hostileOSC8URL) + `,"name":"App","slug":"app"}`))
		}))
		defer server.Close()

		out, err := runRoot(t, server.URL, "apps", "create", "--name", "App", "--workspace", "ws_1")
		if err != nil {
			t.Fatalf("apps create: %v", err)
		}
		assertNoControlBytesStrict(t, out)
		if !strings.Contains(out, "App key: ]8;;https://evil.example/verify") {
			t.Errorf("human output lost the visible app key:\n%q", out)
		}
	})

	t.Run("apps update --icon icon line", func(t *testing.T) {
		iconPath := filepath.Join(t.TempDir(), "icon.png")
		if err := os.WriteFile(iconPath, []byte("\x89PNG\r\n\x1a\nfake-image-bytes"), 0o600); err != nil {
			t.Fatalf("write icon: %v", err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/console/workspaces/ws_1/apps":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"apps":[{"appId":"app_1","appKey":"key_live_1","name":"App","slug":"app"}],"total":1}`))
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/console/workspaces/ws_1/apps/app_1/icon":
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"appId":"app_1","appKey":"key_live_1","iconUrl":` + mustQuoteJSON(t, hostileOSC8URL) + `}`))
			default:
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		out, err := runRoot(t, server.URL, "apps", "update", "app_1", "--icon", iconPath, "--workspace", "ws_1")
		if err != nil {
			t.Fatalf("apps update: %v", err)
		}
		assertNoControlBytesStrict(t, out)
		if !strings.Contains(out, "Icon: ]8;;https://evil.example/verify") {
			t.Errorf("human output lost the visible icon URL:\n%q", out)
		}
	})
}

// TestIntegrationsAuthURLSanitized covers the integrations auth-url surfaces
// (GitHub and the import providers): the authorize URL comes straight from
// the API response and must print sanitized in human mode.
func TestIntegrationsAuthURLSanitized(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/console/workspaces/ws_1/integrations/github/authorize" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":` + mustQuoteJSON(t, hostileOSC8URL) + `,"state":"st_1"}`))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "integrations", "github", "auth-url", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("integrations github auth-url: %v", err)
	}
	assertNoControlBytesStrict(t, out)
	if !strings.Contains(out, "]8;;https://evil.example/verify") {
		t.Errorf("human output lost the visible URL:\n%q", out)
	}
}

// TestAPIRequestHumanBodySanitized pins the issue #183 escape-hatch contract:
// the human-mode body passthrough runs through the sanitized human-line path,
// so a body that is not strictly valid JSON (raw control bytes inside a
// string) cannot carry them to the terminal — while --json stays
// byte-faithful for protocol-legal bodies.
func TestAPIRequestHumanBodySanitized(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	t.Run("non-strict JSON body with raw ESC", func(t *testing.T) {
		raw := []byte("{\"evil\":\"\x1b]8;;https://evil.example\x1b\\link\x1b]8;;\x1b\\ \x1b[31mred\x1b[0m\"}")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(raw)
		}))
		defer server.Close()

		out, err := runRoot(t, server.URL, "api", "request", "GET", "/api/v1/x")
		if err != nil {
			t.Fatalf("api request: %v", err)
		}
		assertNoControlBytesStrict(t, out)
		if !strings.Contains(out, `"evil":"]8;;https://evil.example\link]8;;\ [31mred[0m"`) {
			t.Errorf("human output lost the sanitized body text:\n%q", out)
		}
	})

	t.Run("legal JSON stays byte-faithful via --json", func(t *testing.T) {
		// The wire bytes carry the ESC as the \u001b escape (protocol-legal
		// JSON); a raw ESC byte in a JSON string is exactly the non-strict
		// case the other subtest covers.
		body := "{\"evil\":\"\\u001b[31mred\\u001b[0m\",\"n\":1699999999999999999}"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		defer server.Close()

		out, err := runRoot(t, server.URL, "api", "request", "GET", "/api/v1/x", "--json")
		if err != nil {
			t.Fatalf("api request --json: %v", err)
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, []byte(out)); err != nil {
			t.Fatalf("compact --json output %q: %v", out, err)
		}
		if buf.String() != body {
			t.Errorf("--json stdout = %q, want the raw body compacted to %q", buf.String(), body)
		}

		// The same body in human mode still prints without control bytes.
		out, err = runRoot(t, server.URL, "api", "request", "GET", "/api/v1/x")
		if err != nil {
			t.Fatalf("api request: %v", err)
		}
		assertNoControlBytesStrict(t, out)
	})
}

// TestDeviceProgressLinesSanitized covers the stderr progress lines of the
// device flow: the verification URI and user code come from the
// device-authorization endpoint and must be stripped before echoing.
func TestDeviceProgressLinesSanitized(t *testing.T) {
	stderr := captureStderr(t, func() {
		printDeviceProgress(hostileOSC8URL, "\x1b[31mCODE\x1b[0m\r✗ W(RGN-FAKE")
	})
	assertNoControlBytesStrict(t, stderr)
	for _, want := range []string{
		"First, open:  ]8;;https://evil.example/verify\\CupThread Security",
		"Enter code:   [31mCODE[0m✗ W(RGN-FAKE",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%q", want, stderr)
		}
	}
}

// captureStderr swaps os.Stderr for a pipe for the duration of fn and returns
// everything written to it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	fn()
	os.Stderr = old
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

// mustQuoteJSON encodes s as a protocol-legal JSON string literal for fixture
// bodies.
func mustQuoteJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal fixture string: %v", err)
	}
	return string(b)
}
