package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
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

// digJSONPath walks a decoded --json payload by keys (string) and array
// indices (int) and returns the value at that path.
func digJSONPath(t *testing.T, payload any, path ...any) any {
	t.Helper()
	cur := payload
	for i, seg := range path {
		switch k := seg.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("--json dig segment %d (%q): value is %T, want object", i, k, cur)
			}
			cur, ok = m[k]
			if !ok {
				t.Fatalf("--json dig segment %d: key %q missing in %v", i, k, m)
			}
		case int:
			l, ok := cur.([]any)
			if !ok {
				t.Fatalf("--json dig segment %d (%d): value is %T, want array", i, k, cur)
			}
			if k >= len(l) {
				t.Fatalf("--json dig segment %d: index %d out of range (%d items)", i, k, len(l))
			}
			cur = l[k]
		default:
			t.Fatalf("--json dig segment %d: unsupported path element %v", i, seg)
		}
	}
	return cur
}

// TestResidualServerTextSanitizedInHumanLines pins the issue #275 residual
// sweep: every command that interpolates a server-derived value into a human
// line outside a table renders it through PrintfSafe (or the per-value
// stripper), so a hostile API payload reaches the terminal with zero control
// bytes — while --json keeps the value byte-faithful for programmatic
// consumers. One case per call site listed in the issue, plus the two
// connect sites interpolating the same accountLogin value the issue flags.
func TestResidualServerTextSanitizedInHumanLines(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	hostile := mustQuoteJSON(t, hostileOSC8URL)
	optionsFile := writeInputFile(t, `{"owner":"o","repo":"r"}`)
	reply := func(t *testing.T, w http.ResponseWriter, body string) {
		t.Helper()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
	appsWithHostileName := `{"apps":[{"appId":"app_1","appKey":"key_live_1","slug":"app","name":` + hostile + `}],"total":1}`
	changelogEntry := `{"id":` + hostile + `,"appId":"app_1","title":"T","body":"B","linkedRequests":[],"subscriberCount":0,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}`

	cases := []struct {
		name    string
		handler func(t *testing.T, w http.ResponseWriter, r *http.Request)
		cmd     []string
		dig     []any
	}{
		{
			name: "apps create app name",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/apps" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"appId":"app_1","appKey":"key_live_1","slug":"app","name":`+hostile+`}`)
			},
			cmd: []string{"apps", "create", "--name", "App", "--workspace", "ws_1"},
			dig: []any{"name"},
		},
		{
			name: "apps use app name",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/console/workspaces/ws_1/apps" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, appsWithHostileName)
			},
			cmd: []string{"apps", "use", "app_1", "--workspace", "ws_1"},
			dig: []any{"defaultApp", "name"},
		},
		{
			name: "apps settings show app name",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/console/workspaces/ws_1/settings" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"workspace":{"id":"ws_1","name":"W","slug":"w"},"apps":[{"appId":"app_1","name":`+hostile+`,"settings":{"appId":"app_1"}}]}`)
			},
			cmd: []string{"apps", "settings", "show", "--workspace", "ws_1"},
			dig: []any{"apps", 0, "name"},
		},
		{
			name: "apps public-changelog next cursor",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/public/apps/app_1/changelog" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"entries":[],"hasMore":true,"nextCursor":`+hostile+`}`)
			},
			cmd: []string{"apps", "public-changelog", "app_1"},
			dig: []any{"nextCursor"},
		},
		{
			name: "apps public-feature-requests next cursor",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/feature-requests" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"requests":[],"total":0,"hasMore":true,"nextCursor":`+hostile+`}`)
			},
			cmd: []string{"apps", "public-feature-requests", "app_1"},
			dig: []any{"nextCursor"},
		},
		{
			name: "workspaces create workspace name",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"workspace":{"id":"ws_9","name":`+hostile+`,"slug":"w"},"membership":{"id":"m_1","role":"owner"}}`)
			},
			cmd: []string{"workspaces", "create", "--name", "Ws"},
			dig: []any{"workspace", "name"},
		},
		{
			name: "workspaces use workspace name",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/console/me" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"clerkUserId":"u_1","email":"dev@example.com","emailVerified":true,"workspaces":[{"workspace":{"id":"ws_1","name":`+hostile+`,"slug":"w"},"membership":{"id":"m_1","clerkUserId":"u_1","role":"owner"}}]}`)
			},
			cmd: []string{"workspaces", "use", "ws_1"},
			dig: []any{"defaultWorkspace", "name"},
		},
		{
			name: "workspaces members add status",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/members" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"member":null,"invitation":null,"status":`+hostile+`}`)
			},
			cmd: []string{"workspaces", "members", "add", "--clerk-user-id", "user_1", "--workspace", "ws_1"},
			dig: []any{"status"},
		},
		{
			name: "columns create column name",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/apps/app_1/columns" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"id":"col_1","appId":"app_1","name":`+hostile+`}`)
			},
			cmd: []string{"columns", "create", "--name", "Col", "--app", "app_1", "--workspace", "ws_1"},
			dig: []any{"name"},
		},
		{
			name: "versions create version label",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/apps/app_1/versions" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"id":"ver_1","appId":"app_1","label":`+hostile+`}`)
			},
			cmd: []string{"versions", "create", "--label", "1.0", "--app", "app_1", "--workspace", "ws_1"},
			dig: []any{"label"},
		},
		{
			name: "me identity fields",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/console/me" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"clerkUserId":`+hostile+`,"email":"dev@example.com","emailVerified":true,"workspaces":[]}`)
			},
			cmd: []string{"me"},
			dig: []any{"clerkUserId"},
		},
		{
			name: "integrations github sync error strings",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/console/workspaces/ws_1/apps":
					reply(t, w, appsWithHostileName)
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/console/workspaces/ws_1/apps/app_1/github/sync":
					reply(t, w, `{"syncedCount":0,"updatedRequests":[],"errors":[`+hostile+`]}`)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			},
			cmd: []string{"integrations", "github", "sync", "app_1", "--workspace", "ws_1"},
			dig: []any{"errors", 0},
		},
		{
			name: "integrations provider status account login",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/console/workspaces/ws_1/integrations/linear" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"integration":{"id":"int_1","workspaceId":"ws_1","provider":"linear","accountLogin":`+hostile+`},"oauthConfigured":true}`)
			},
			cmd: []string{"integrations", "linear", "status", "--workspace", "ws_1"},
			dig: []any{"integration", "accountLogin"},
		},
		{
			name: "integrations github connect account login",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/integrations/github/manual-token" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"integration":{"id":"int_1","workspaceId":"ws_1","provider":"github","accountLogin":`+hostile+`}}`)
			},
			cmd: []string{"integrations", "github", "connect", "--token", "ghp_fake", "--workspace", "ws_1"},
			dig: []any{"accountLogin"},
		},
		{
			name: "integrations linear connect account login",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/integrations/linear/manual-token" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"integration":{"id":"int_2","workspaceId":"ws_1","provider":"linear","accountLogin":`+hostile+`}}`)
			},
			cmd: []string{"integrations", "linear", "connect", "--token", "lini_fake", "--workspace", "ws_1"},
			dig: []any{"accountLogin"},
		},
		{
			name: "integrations github categories repository id",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/console/workspaces/ws_1/integrations/github/repos/o/r/categories" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"repositoryId":`+hostile+`,"categories":[]}`)
			},
			cmd: []string{"integrations", "github", "categories", "--owner", "o", "--repo", "r", "--workspace", "ws_1"},
			dig: []any{"repositoryId"},
		},
		{
			name: "features forward target type and url",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/console/workspaces/ws_1/feature-requests":
					reply(t, w, `{"requests":[{"id":"fr_1"}],"total":1,"hasMore":false}`)
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/console/workspaces/ws_1/apps/app_1/features/fr_1/github/forward":
					reply(t, w, `{"success":true,"targetType":`+hostile+`,"id":"gh_1","url":`+hostile+`}`)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			},
			cmd: []string{"features", "forward", "fr_1", "--target", "discussion", "--app", "app_1", "--workspace", "ws_1"},
			dig: []any{"url"},
		},
		{
			name: "imports create job id",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/apps/app_1/imports" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"job":{"id":`+hostile+`,"workspaceId":"ws_1","appId":"app_1","source":"github_issues","mode":"preview","status":"completed","candidates":[{}],"attempts":0},"drain":{"processed":0,"succeeded":0,"failed":0}}`)
			},
			cmd: []string{"imports", "create", "--workspace", "ws_1", "--app", "app_1", "--source", "github_issues", "--options", optionsFile},
			dig: []any{"job", "id"},
		},
		{
			name: "imports rerun job id",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/imports/job_1/rerun" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"job":{"id":`+hostile+`,"workspaceId":"ws_1","appId":"app_1","source":"github_issues","mode":"preview","status":"queued","attempts":1}}`)
			},
			cmd: []string{"imports", "rerun", "job_1", "--workspace", "ws_1"},
			dig: []any{"job", "id"},
		},
		{
			name: "changelog create entry id",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/changelog" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, changelogEntry)
			},
			cmd: []string{"changelog", "create", "--title", "T", "--body", "B", "--workspace", "ws_1", "--app", "app_1"},
			dig: []any{"id"},
		},
		{
			name: "changelog update entry id",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.Path != "/api/v1/console/workspaces/ws_1/changelog/entry_1" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, changelogEntry)
			},
			cmd: []string{"changelog", "update", "entry_1", "--title", "T", "--workspace", "ws_1"},
			dig: []any{"id"},
		},
		{
			name: "changelog publish entry id",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/changelog/entry_1/publish" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, changelogEntry)
			},
			cmd: []string{"changelog", "publish", "entry_1", "--workspace", "ws_1"},
			dig: []any{"id"},
		},
		{
			name: "notifications set channel",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.Path != "/api/v1/console/workspaces/ws_1/notification-prefs" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"workspaceId":"ws_1","channel":`+hostile+`,"eventMask":["vote.created"],"enabled":true}`)
			},
			cmd: []string{"notifications", "prefs", "set", "--channel", "inbox", "--enable", "--workspace", "ws_1"},
			dig: []any{"channel"},
		},
		{
			name: "inbox bulk-triage updated ids",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/submissions/bulk-triage" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"updatedCount":1,"updatedIds":[`+hostile+`]}`)
			},
			cmd: []string{"inbox", "bulk-triage", "sub_1", "--triage-status", "resolved", "--workspace", "ws_1"},
			dig: []any{"updatedIds", 0},
		},
		{
			name: "inbox retry discussion url",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/submissions/sub_1/retry" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reply(t, w, `{"submissionId":"sub_1","forwardedToGithub":true,"githubDiscussionUrl":`+hostile+`}`)
			},
			cmd: []string{"inbox", "retry", "sub_1", "--workspace", "ws_1"},
			dig: []any{"githubDiscussionUrl"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tc.handler(t, w, r)
			}))
			defer server.Close()

			out, err := runRoot(t, server.URL, tc.cmd...)
			if err != nil {
				t.Fatalf("%v: %v", tc.cmd, err)
			}
			assertNoControlBytesStrict(t, out)
			if !strings.Contains(out, "]8;;https://evil.example/verify") || !strings.Contains(out, "CupThread Security") {
				t.Errorf("human output lost the visible payload text:\n%q", out)
			}

			// Structured mode stays byte-faithful: the hostile value survives
			// the round-trip exactly (encoding/json escapes the controls).
			structured, err := runRoot(t, server.URL, append(append([]string{}, tc.cmd...), "--json")...)
			if err != nil {
				t.Fatalf("%v --json: %v", tc.cmd, err)
			}
			var payload any
			if err := json.Unmarshal([]byte(strings.TrimSpace(structured)), &payload); err != nil {
				t.Fatalf("decode --json output %q: %v", structured, err)
			}
			if got, _ := digJSONPath(t, payload, tc.dig...).(string); got != hostileOSC8URL {
				t.Errorf("--json at %v = %q, want the payload byte-faithful", tc.dig, got)
			}
		})
	}
}

// TestInboxRetryServerErrorSanitizedOnStderr covers the inbox retry special
// case (issue #275): the free-form server error is returned as the command
// error, so cobra prints it to stderr raw — it must pass through the strict
// stripper first. In --json mode the same response is a structured success
// (the error is a field, not a failure), and the field stays byte-faithful.
func TestInboxRetryServerErrorSanitizedOnStderr(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/console/workspaces/ws_1/submissions/sub_1/retry" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"submissionId":"sub_1","forwardedToGithub":false,"error":` + mustQuoteJSON(t, hostileOSC8URL) + `}`))
	}))
	defer server.Close()

	_, stderr, err := runRootCapture(t, filepath.Join(t.TempDir(), "config.json"), server.URL,
		"inbox", "retry", "sub_1", "--workspace", "ws_1")
	if err == nil {
		t.Fatal("inbox retry with a partial-failure payload should fail")
	}
	assertNoControlBytesStrict(t, stderr)
	if !strings.Contains(stderr, "retry failed: ]8;;https://evil.example/verify\\CupThread Security") {
		t.Errorf("stderr lost the sanitized error text:\n%q", stderr)
	}

	stdout, _, err := runRootCapture(t, filepath.Join(t.TempDir(), "config.json"), server.URL,
		"inbox", "retry", "sub_1", "--workspace", "ws_1", "--json")
	if err != nil {
		t.Fatalf("inbox retry --json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
		t.Fatalf("decode --json output %q: %v", stdout, err)
	}
	if got, _ := payload["error"].(string); got != hostileOSC8URL {
		t.Errorf("--json error = %q, want the payload byte-faithful", got)
	}
}

// bannedPrintfExpressions names the server-derived expressions the
// #183/#275/#280 sweeps converted off the raw Printf path. The regression
// guard fails on any raw Printf call site that interpolates one again; new
// call sites must use PrintfSafe.
var bannedPrintfExpressions = []string{
	"appRec.Name", "appRec.AppID", "updated.AppID", "entry.Name", "entry.AppID",
	"resp.Name", "resp.Label", "resp.ID", "resp.Status", "resp.Channel",
	"resp.RepositoryID", "resp.NextCursor", "resp.Errors", "resp.Error", "resp.URL",
	"resp.TargetType", "resp.GithubDiscussionURL", "resp.UpdatedIds", "resp.SubmissionID",
	"resp.Job.", "resp.Member.ID", "resp.Workspace.", "resp.Integration.AccountLogin",
	"me.Email", "me.ClerkUserID", "ws.Name", "requestID",
	// Issue #280 additions: the login/logout success lines interpolate the
	// echoed account email, the prefix of a server-issued access token and
	// the persisted workspace context cleared at logout.
	"res.Email", "res.TokenPrefix", "A.cfg.Auth.TokenPrefix", "A.cfg.DefaultWorkspace",
}

// scanRawPrintfInterpolations scans dir's non-test Go sources for raw
// .out.Printf call sites interpolating any banned expression and returns one
// violation string per hit. It parses each file and inspects complete call
// expressions rather than individual lines, so a call whose format string and
// banned argument sit on separate lines is still caught (CodeRabbit review of
// #283). It matches every receiver casing (both A.out and a.out — the
// lowercase receiver slipped past the #275 sweep, issue #280) and never
// matches PrintfSafe: the safe call is a different selector, invisible to the
// guard by construction, not by luck.
func scanRawPrintfInterpolations(dir string, banned []string) ([]string, error) {
	var violations []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, e.Name(), src, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Printf" {
				return true
			}
			recv, ok := sel.X.(*ast.SelectorExpr)
			if !ok || recv.Sel.Name != "out" {
				return true
			}
			callText := string(src[fset.Position(call.Lparen).Offset : fset.Position(call.Rparen).Offset+1])
			for _, b := range banned {
				if strings.Contains(callText, b) {
					violations = append(violations, fmt.Sprintf(
						"%s:%d interpolates server-derived %s through Printf; use PrintfSafe (issues #183/#275/#280):\n%s",
						e.Name(), fset.Position(call.Pos()).Line, b, strings.TrimSpace(callText)))
					break
				}
			}
			return true
		})
	}
	return violations, nil
}

// TestNoResidualServerTextThroughPrintf is the regression guard: the #183/#275
// human-output contract keeps every server-derived value out of the raw Printf
// path. Scan the command sources for Printf call sites interpolating the
// server-derived expressions these sweeps converted; a new call site cannot
// reintroduce the gap silently.
func TestNoResidualServerTextThroughPrintf(t *testing.T) {
	violations, err := scanRawPrintfInterpolations(".", bannedPrintfExpressions)
	if err != nil {
		t.Fatalf("scan command sources: %v", err)
	}
	for _, v := range violations {
		t.Error(v)
	}
}

// TestPrintfScanHelperCatchesPlantedViolations keeps the guard's scanner
// itself from rotting (issue #280): the scanner must flag raw Printf call
// sites on both receiver casings — including a call whose banned argument
// sits on a continuation line — skip PrintfSafe call sites (single- and
// multi-line) and skip raw calls that carry no banned expression.
func TestPrintfScanHelperCatchesPlantedViolations(t *testing.T) {
	dir := t.TempDir()
	src := `package cmd

import "fmt"

func planted() {
	fmt.Println("outside the guard")
	A.out.Printf("ok %s", resp.Name)
	a.out.Printf("ok %s", res.TokenPrefix)
	A.out.PrintfSafe("ok %s", resp.Name)
	A.out.PrintfSafe("ok %s",
		resp.Name)
	A.out.Printf("hello %s",
		me.Email)
	A.out.Printf("benign %d", 3)
}
`
	if err := os.WriteFile(filepath.Join(dir, "planted.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write planted source: %v", err)
	}
	violations, err := scanRawPrintfInterpolations(dir, bannedPrintfExpressions)
	if err != nil {
		t.Fatalf("scan planted dir: %v", err)
	}
	if len(violations) != 3 {
		t.Fatalf("violations = %v, want exactly the three raw banned call sites", violations)
	}
	for _, want := range []string{
		"planted.go:7 interpolates server-derived resp.Name",
		"planted.go:8 interpolates server-derived res.TokenPrefix",
		"planted.go:12 interpolates server-derived me.Email",
	} {
		found := false
		for _, v := range violations {
			if strings.Contains(v, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("violations missing %q:\n%v", want, violations)
		}
	}
}

// hostileLoginEmail is the issue #280 attack payload: a /console/me email that
// renders an OSC 8 phishing link (BEL-terminated), forges a success line via
// CR, and repeats the sequence in the C1 (ESC-free) form — everything a
// hostile API can smuggle through the login confirmation line.
const hostileLoginEmail = "dev\x1b]8;;http://evil.example\x07click\x1b]8;;\x07@example.com\rFAKE-LINE\u009b31mC1 SGR\u009b0m"

// hostileLoginEmailVisible is the strict-stripped remnant: the visible payload
// text survives (so the human still sees what the server sent), every control
// byte is gone.
const hostileLoginEmailVisible = "dev]8;;http://evil.exampleclick]8;;@example.comFAKE-LINE31mC1 SGR0m"

// hostileWorkspaceID is the logout counterpart: a persisted defaultWorkspace
// id carrying the same OSC 8 + CR payload, echoed by the "Cleared saved
// context" logout line (issue #280).
const hostileWorkspaceID = "ws_\x1b]8;;http://evil.example\x07click\x1b]8;;\x07ws\rFAKE-LINE"

// hostileWorkspaceIDVisible is the strict-stripped remnant of the hostile
// workspace id.
const hostileWorkspaceIDVisible = "ws_]8;;http://evil.exampleclick]8;;wsFAKE-LINE"

// hostileMEServer serves the issue #280 hostile /console/me payload: the
// account email decodes into the OSC 8 + CR attack string.
func hostileMEServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/console/me" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":` + mustQuoteJSON(t, hostileLoginEmail) + `,"emailVerified":true,"workspaces":[]}`))
	}))
}

// TestAuthLoginTokenSuccessLineSanitized pins the issue #280 contract for the
// most security-sensitive line the CLI renders: `auth login --token` echoes
// the server-provided account email through PrintfSafe, so the hostile email
// reaches the terminal with zero control bytes instead of an OSC 8 link and a
// CR-forged success line. The same value printed by `me` was stripped in
// #275; the two commands must agree on identical server data. --json output
// stays byte-faithful for programmatic consumers.
func TestAuthLoginTokenSuccessLineSanitized(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	server := hostileMEServer(t)
	defer server.Close()

	stdout, _, err := runRootCapture(t, filepath.Join(t.TempDir(), "config.json"), server.URL,
		"auth", "login", "--token", "cpt_issue280token0")
	if err != nil {
		t.Fatalf("auth login --token: %v\nstdout:\n%s", err, stdout)
	}
	assertNoControlBytesStrict(t, stdout)
	if !strings.Contains(stdout, "Logged in as "+hostileLoginEmailVisible) {
		t.Errorf("human line lost the visible email remnant:\n%q", stdout)
	}
	if !strings.Contains(stdout, "cpt_issue280") {
		t.Errorf("human line lost the token prefix:\n%q", stdout)
	}

	stdout, _, err = runRootCapture(t, filepath.Join(t.TempDir(), "config.json"), server.URL,
		"auth", "login", "--token", "cpt_issue280token0", "--json")
	if err != nil {
		t.Fatalf("auth login --token --json: %v\nstdout:\n%s", err, stdout)
	}
	doc := unmarshalOneJSON(t, stdout)
	if doc["email"] != hostileLoginEmail {
		t.Errorf("--json email = %q, want the payload byte-faithful", doc["email"])
	}
}

// TestAuthLoginDeviceSuccessLineSanitized pins the same contract for the
// device flow: both the device-authorization response (verification URI,
// user code — progress on stderr) and the /console/me probe (email — the
// final confirmation line on stdout) come from the API, so every human-mode
// rendering of them must carry zero control bytes, while --json echoes the
// verification URI byte-faithfully.
func TestAuthLoginDeviceSuccessLineSanitized(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case auth.DeviceAuthorizePath:
			_, _ = w.Write([]byte(`{"device_code":"dev_code_280","user_code":"ISSUE-2800","verification_uri":` + mustQuoteJSON(t, hostileLoginEmail) + `,"interval":1,"expires_in":120}`))
		case auth.DeviceTokenPath:
			_, _ = w.Write([]byte(`{"access_token":"cpt_device_280_ok","refresh_token":"cpt_device_280_re","token_type":"bearer","expires_in":3600,"scope":"console"}`))
		case "/api/v1/console/me":
			_, _ = w.Write([]byte(`{"email":` + mustQuoteJSON(t, hostileLoginEmail) + `,"emailVerified":true,"workspaces":[]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr, err := runRootCapture(t, filepath.Join(t.TempDir(), "config.json"), server.URL,
		"auth", "login", "--device")
	if err != nil {
		t.Fatalf("auth login --device: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	assertNoControlBytesStrict(t, stdout)
	assertNoControlBytesStrict(t, stderr)
	if !strings.Contains(stdout, "Logged in as "+hostileLoginEmailVisible) {
		t.Errorf("device login line lost the visible email remnant:\n%q", stdout)
	}
	if !strings.Contains(stderr, "First, open:  "+hostileLoginEmailVisible) {
		t.Errorf("device progress lost the visible URI remnant:\n%q", stderr)
	}

	stdout, _, err = runRootCapture(t, filepath.Join(t.TempDir(), "config.json"), server.URL,
		"auth", "login", "--device", "--json")
	if err != nil {
		t.Fatalf("auth login --device --json: %v\nstdout:\n%s", err, stdout)
	}
	doc := unmarshalOneJSON(t, stdout)
	if doc["verificationUri"] != hostileLoginEmail {
		t.Errorf("--json verificationUri = %q, want the payload byte-faithful", doc["verificationUri"])
	}
	if doc["email"] != hostileLoginEmail {
		t.Errorf("--json email = %q, want the payload byte-faithful", doc["email"])
	}
}

// TestAuthLogoutClearedContextLineSanitized pins the logout side of issue
// #280: the "Cleared saved context" line echoes the persisted
// defaultWorkspace id, which a hostile API wrote into the config at an
// earlier login, so the human line must render its stripped remnant only;
// --json keeps the hostile value byte-faithful in the cleared array. Logout
// performs no network I/O without --revoke, so the base URL points at an
// unroutable address to prove the contract is local.
func TestAuthLogoutClearedContextLineSanitized(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")

	seed := func(t *testing.T) string {
		t.Helper()
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		pre := &config.Config{
			DefaultWorkspace: hostileWorkspaceID,
			Auth:             &config.Auth{Method: "token", AccessToken: "cpt_pat_280", TokenPrefix: "cpt_pat_280"},
		}
		if err := pre.Save(cfgPath); err != nil {
			t.Fatalf("seed config: %v", err)
		}
		return cfgPath
	}

	stdout, _, err := runRootCapture(t, seed(t), "http://127.0.0.1:1", "auth", "logout")
	if err != nil {
		t.Fatalf("auth logout: %v\nstdout:\n%s", err, stdout)
	}
	assertNoControlBytesStrict(t, stdout)
	if !strings.Contains(stdout, "Cleared saved context from the previous login: default workspace "+hostileWorkspaceIDVisible) {
		t.Errorf("logout line lost the visible cleared-context remnant:\n%q", stdout)
	}

	stdout, _, err = runRootCapture(t, seed(t), "http://127.0.0.1:1", "auth", "logout", "--json")
	if err != nil {
		t.Fatalf("auth logout --json: %v\nstdout:\n%s", err, stdout)
	}
	doc := unmarshalOneJSON(t, stdout)
	cleared, ok := doc["cleared"].([]any)
	if !ok || len(cleared) == 0 {
		t.Fatalf("--json cleared = %v, want one entry naming the hostile workspace", doc["cleared"])
	}
	if cleared[0] != "default workspace "+hostileWorkspaceID {
		t.Errorf("--json cleared[0] = %q, want the payload byte-faithful", cleared[0])
	}
}
