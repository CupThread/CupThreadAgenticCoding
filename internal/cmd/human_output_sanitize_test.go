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

// TestNoResidualServerTextThroughPrintf is the regression guard: the #183/#275
// human-output contract keeps every server-derived value out of the raw Printf
// path. Grep the command sources for Printf call sites interpolating the
// server-derived expressions this sweep converted; a new call site cannot
// reintroduce the gap silently. (The substring "A.out.Printf(" does not match
// PrintfSafe lines, so converted sites are invisible to the guard.)
func TestNoResidualServerTextThroughPrintf(t *testing.T) {
	banned := []string{
		"appRec.Name", "appRec.AppID", "updated.AppID", "entry.Name", "entry.AppID",
		"resp.Name", "resp.Label", "resp.ID", "resp.Status", "resp.Channel",
		"resp.RepositoryID", "resp.NextCursor", "resp.Errors", "resp.Error", "resp.URL",
		"resp.TargetType", "resp.GithubDiscussionURL", "resp.UpdatedIds", "resp.SubmissionID",
		"resp.Job.", "resp.Member.ID", "resp.Workspace.", "resp.Integration.AccountLogin",
		"me.Email", "me.ClerkUserID", "ws.Name", "requestID",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read command sources: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		lines, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for i, line := range strings.Split(string(lines), "\n") {
			if !strings.Contains(line, "A.out.Printf(") {
				continue
			}
			for _, b := range banned {
				if strings.Contains(line, b) {
					t.Errorf("%s:%d interpolates server-derived %s through Printf; use A.out.PrintfSafe (issues #183/#275):\n%s",
						e.Name(), i+1, b, strings.TrimSpace(line))
				}
			}
		}
	}
}
