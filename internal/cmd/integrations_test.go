package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// connectMockServer captures the manual-token request (path + raw body) and
// answers with a minimal connected-integration envelope.
func connectMockServer(t *testing.T, requests *int, gotPath *string, gotBody *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests++
		*gotPath = r.URL.Path
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		*gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"integration":{"id":"int_1","provider":"github","accountLogin":"octocat"}}`)
	}))
}

// connectWireBody decodes the captured request body as the JSON object the
// CLI must send.
func connectWireBody(t *testing.T, raw string) map[string]string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode request body %q: %v", raw, err)
	}
	return body
}

// TestGitHubConnectTokenFromStdin covers the issue's core case: --token -
// (and @) reads the token from stdin, trims the trailing newline/whitespace,
// and sends exactly what an inline flag would have sent.
func TestGitHubConnectTokenFromStdin(t *testing.T) {
	for _, form := range []string{"-", "@"} {
		t.Run(form, func(t *testing.T) {
			requests, gotPath, gotBody := 0, "", ""
			server := connectMockServer(t, &requests, &gotPath, &gotBody)
			defer server.Close()

			t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
			t.Setenv("CUPTHREAD_GITHUB_TOKEN", "")
			withSignStdin(t, "  cpt_gh_pat_value\n")
			out, err := runRoot(t, server.URL, "integrations", "github", "connect",
				"--token", form, "--workspace", "ws_1")
			if err != nil {
				t.Fatalf("connect --token %s: %v", form, err)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
			if want := "/api/v1/console/workspaces/ws_1/integrations/github/manual-token"; gotPath != want {
				t.Errorf("path = %q, want %q", gotPath, want)
			}
			body := connectWireBody(t, gotBody)
			if body["token"] != "cpt_gh_pat_value" {
				t.Errorf("token = %q, want trimmed stdin value", body["token"])
			}
			if !strings.Contains(out, "Connected GitHub as octocat") {
				t.Errorf("output %q missing connected line", out)
			}
		})
	}
}

// TestProviderConnectEnvTokenFallback covers the per-provider environment
// fallback: with the flag empty, the token comes from
// $CUPTHREAD_<PROVIDER>_TOKEN and hits the provider's own endpoint.
func TestProviderConnectEnvTokenFallback(t *testing.T) {
	cases := []struct {
		provider string
		envValue string
		args     []string
	}{
		{"github", "cpt_gh_env_token", []string{"integrations", "github", "connect"}},
		{"linear", "lin_api_token", []string{"integrations", "linear", "connect"}},
		{"notion", "ntn_api_token", []string{"integrations", "notion", "connect"}},
		{"slack", "xoxb_slack_token", []string{"integrations", "slack", "connect"}},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			requests, gotPath, gotBody := 0, "", ""
			server := connectMockServer(t, &requests, &gotPath, &gotBody)
			defer server.Close()

			t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
			t.Setenv("CUPTHREAD_"+strings.ToUpper(tc.provider)+"_TOKEN", tc.envValue+"\n")
			args := append(tc.args, "--workspace", "ws_1")
			if _, err := runRoot(t, server.URL, args...); err != nil {
				t.Fatalf("%s connect via env: %v", tc.provider, err)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want 1", requests)
			}
			if want := "/api/v1/console/workspaces/ws_1/integrations/" + tc.provider + "/manual-token"; gotPath != want {
				t.Errorf("path = %q, want %q", gotPath, want)
			}
			body := connectWireBody(t, gotBody)
			if body["token"] != tc.envValue {
				t.Errorf("token = %q, want trimmed env value %q", body["token"], tc.envValue)
			}
		})
	}
}

// TestProviderConnectFlagBeatsEnv pins the precedence: with both sources set,
// the inline flag wins.
func TestProviderConnectFlagBeatsEnv(t *testing.T) {
	requests, gotPath, gotBody := 0, "", ""
	server := connectMockServer(t, &requests, &gotPath, &gotBody)
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	t.Setenv("CUPTHREAD_LINEAR_TOKEN", "lin_env_token")
	if _, err := runRoot(t, server.URL, "integrations", "linear", "connect",
		"--token", "lin_flag_token", "--workspace", "ws_1"); err != nil {
		t.Fatalf("connect with flag+env: %v", err)
	}
	body := connectWireBody(t, gotBody)
	if body["token"] != "lin_flag_token" {
		t.Errorf("token = %q, want the flag value", body["token"])
	}
}

// TestGitHubConnectTokenMissingNamesSources covers the no-source error: it
// names all three accepted forms and fires before any HTTP request.
func TestGitHubConnectTokenMissingNamesSources(t *testing.T) {
	requests, gotPath, gotBody := 0, "", ""
	server := connectMockServer(t, &requests, &gotPath, &gotBody)
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	t.Setenv("CUPTHREAD_GITHUB_TOKEN", "")
	_, err := runRoot(t, server.URL, "integrations", "github", "connect", "--workspace", "ws_1")
	if err == nil {
		t.Fatal("expected an error without any token source")
	}
	if requests != 0 {
		t.Errorf("requests = %d, want 0 (missing-token check must precede HTTP)", requests)
	}
	for _, want := range []string{
		"github token is required",
		"--token <value>",
		"--token -",
		"$CUPTHREAD_GITHUB_TOKEN",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// TestProviderConnectEmptyStdin errors instead of sending an empty token
// when stdin carries only whitespace.
func TestProviderConnectEmptyStdin(t *testing.T) {
	requests, gotPath, gotBody := 0, "", ""
	server := connectMockServer(t, &requests, &gotPath, &gotBody)
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	t.Setenv("CUPTHREAD_NOTION_TOKEN", "")
	withSignStdin(t, "   \n")
	_, err := runRoot(t, server.URL, "integrations", "notion", "connect",
		"--token", "-", "--workspace", "ws_1")
	if err == nil {
		t.Fatal("expected an error for whitespace-only stdin")
	}
	if requests != 0 {
		t.Errorf("requests = %d, want 0", requests)
	}
	if !strings.Contains(err.Error(), "stdin carried no notion token") {
		t.Errorf("error %q missing stdin note", err)
	}
}
