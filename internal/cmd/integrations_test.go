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

// configMockServer serves the app listing lookupApp resolves against and
// captures the per-app github config PATCH body. requests counts every HTTP
// request (so tests can assert a failure preceded the app lookup), patches
// counts only the config PATCH, and gotBody carries its raw JSON.
func configMockServer(t *testing.T, requests, patches *int, gotBody *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/apps"):
			fmt.Fprintf(w, `{"apps":[{"appId":"app_1","appKey":"key_live_1","slug":"ios","name":"Acme iOS","allowPublic":true,"allowedPlatforms":["ios"],"maxAttachmentBytes":10485760,"githubSyncEnabled":true,"githubSyncStatusEnabled":true,"githubSyncCommentsEnabled":false,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z"}],"total":1}`)
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/apps/app_1/github"):
			*patches++
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read patch body: %v", err)
			}
			*gotBody = string(b)
			fmt.Fprintf(w, `{}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			fmt.Fprintf(w, `{}`)
		}
	}))
}

// configWireBody decodes the captured PATCH body as the JSON object the CLI
// must send.
func configWireBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode request body %q: %v", raw, err)
	}
	return body
}

// TestGitHubConfigWebhookSecretFromStdin covers the issue's core case:
// --webhook-secret - (and @) reads the secret from stdin, trims trailing
// whitespace, and sends it as githubWebhookSecret in the PATCH body.
func TestGitHubConfigWebhookSecretFromStdin(t *testing.T) {
	for _, form := range []string{"-", "@"} {
		t.Run(form, func(t *testing.T) {
			requests, patches, gotBody := 0, 0, ""
			server := configMockServer(t, &requests, &patches, &gotBody)
			defer server.Close()

			t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
			t.Setenv(webhookSecretEnvVar, "")
			withSignStdin(t, "  whsec_piped_value\n")
			if _, err := runRoot(t, server.URL, "integrations", "github", "config", "app_1",
				"--webhook-secret", form, "--workspace", "ws_1"); err != nil {
				t.Fatalf("config --webhook-secret %s: %v", form, err)
			}
			if patches != 1 {
				t.Fatalf("patches = %d, want 1", patches)
			}
			body := configWireBody(t, gotBody)
			if body["githubWebhookSecret"] != "whsec_piped_value" {
				t.Errorf("githubWebhookSecret = %v, want the trimmed stdin value", body["githubWebhookSecret"])
			}
		})
	}
}

// TestGitHubConfigWebhookSecretEnvFallback covers the environment fallback:
// with the flag omitted, the trimmed $CUPTHREAD_GITHUB_WEBHOOK_SECRET value
// is sent instead of being ignored.
func TestGitHubConfigWebhookSecretEnvFallback(t *testing.T) {
	requests, patches, gotBody := 0, 0, ""
	server := configMockServer(t, &requests, &patches, &gotBody)
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	t.Setenv(webhookSecretEnvVar, "  whsec_env_value\n")
	if _, err := runRoot(t, server.URL, "integrations", "github", "config", "app_1",
		"--workspace", "ws_1"); err != nil {
		t.Fatalf("config with env fallback: %v", err)
	}
	if patches != 1 {
		t.Fatalf("patches = %d, want 1", patches)
	}
	body := configWireBody(t, gotBody)
	if body["githubWebhookSecret"] != "whsec_env_value" {
		t.Errorf("githubWebhookSecret = %v, want the trimmed env value", body["githubWebhookSecret"])
	}
}

// TestGitHubConfigWebhookSecretAbsentWithoutEnv pins the no-source case: with
// the flag omitted and no environment variable, the githubWebhookSecret field
// is absent from the PATCH body (no accidental overwrite with "") while other
// flags still update.
func TestGitHubConfigWebhookSecretAbsentWithoutEnv(t *testing.T) {
	requests, patches, gotBody := 0, 0, ""
	server := configMockServer(t, &requests, &patches, &gotBody)
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	t.Setenv(webhookSecretEnvVar, "")
	if _, err := runRoot(t, server.URL, "integrations", "github", "config", "app_1",
		"--owner", "octocat", "--workspace", "ws_1"); err != nil {
		t.Fatalf("config without any secret source: %v", err)
	}
	body := configWireBody(t, gotBody)
	if _, present := body["githubWebhookSecret"]; present {
		t.Errorf("githubWebhookSecret = %v, want the field absent from the body", body["githubWebhookSecret"])
	}
	if body["githubOwner"] != "octocat" {
		t.Errorf("githubOwner = %v, want octocat", body["githubOwner"])
	}
}

// TestGitHubConfigWebhookSecretClearBeatsEnv pins the deliberate clear:
// --webhook-secret "" sends a JSON null that clears the stored secret even
// with $CUPTHREAD_GITHUB_WEBHOOK_SECRET exported — the env fallback must not
// resurrect a value the flag explicitly emptied.
func TestGitHubConfigWebhookSecretClearBeatsEnv(t *testing.T) {
	requests, patches, gotBody := 0, 0, ""
	server := configMockServer(t, &requests, &patches, &gotBody)
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	t.Setenv(webhookSecretEnvVar, "whsec_env_value")
	if _, err := runRoot(t, server.URL, "integrations", "github", "config", "app_1",
		"--webhook-secret", "", "--workspace", "ws_1"); err != nil {
		t.Fatalf("config with an explicit clear: %v", err)
	}
	body := configWireBody(t, gotBody)
	v, present := body["githubWebhookSecret"]
	if !present {
		t.Fatal("githubWebhookSecret is absent, want an explicit null clear")
	}
	if v != nil {
		t.Errorf("githubWebhookSecret = %v, want a JSON null clear", v)
	}
}

// TestGitHubConfigWebhookSecretInline keeps the original behavior working:
// an inline value is sent verbatim (issue #190 stays backwards compatible).
func TestGitHubConfigWebhookSecretInline(t *testing.T) {
	requests, patches, gotBody := 0, 0, ""
	server := configMockServer(t, &requests, &patches, &gotBody)
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	t.Setenv(webhookSecretEnvVar, "")
	if _, err := runRoot(t, server.URL, "integrations", "github", "config", "app_1",
		"--webhook-secret", "whsec_inline_value", "--workspace", "ws_1"); err != nil {
		t.Fatalf("config with an inline secret: %v", err)
	}
	body := configWireBody(t, gotBody)
	if body["githubWebhookSecret"] != "whsec_inline_value" {
		t.Errorf("githubWebhookSecret = %v, want the inline value", body["githubWebhookSecret"])
	}
}

// TestGitHubConfigWebhookSecretStdinTooLarge pins the issue's bound contract:
// stdin over the 64 KB piped-credential cap fails with the structured
// input_too_large document before any HTTP request — the app lookup included.
func TestGitHubConfigWebhookSecretStdinTooLarge(t *testing.T) {
	requests, patches, gotBody := 0, 0, ""
	server := configMockServer(t, &requests, &patches, &gotBody)
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	t.Setenv(webhookSecretEnvVar, "")
	withSignStdin(t, strings.Repeat("x", int(maxSecretBytes)+1))
	out, err := runRoot(t, server.URL, "integrations", "github", "config", "app_1",
		"--webhook-secret", "-", "--workspace", "ws_1", "--json")
	if err == nil {
		t.Fatal("config succeeded with an over-cap piped secret, want a local failure")
	}
	if requests != 0 {
		t.Errorf("requests = %d, want 0 — the cap must fail before the app lookup", requests)
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

// TestGitHubConfigWebhookSecretEmptyStdin errors instead of sending an empty
// secret when stdin carries only whitespace.
func TestGitHubConfigWebhookSecretEmptyStdin(t *testing.T) {
	requests, patches, gotBody := 0, 0, ""
	server := configMockServer(t, &requests, &patches, &gotBody)
	defer server.Close()

	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")
	t.Setenv(webhookSecretEnvVar, "")
	withSignStdin(t, "   \n")
	_, err := runRoot(t, server.URL, "integrations", "github", "config", "app_1",
		"--webhook-secret", "-", "--workspace", "ws_1")
	if err == nil {
		t.Fatal("expected an error for whitespace-only stdin")
	}
	if requests != 0 {
		t.Errorf("requests = %d, want 0", requests)
	}
	if !strings.Contains(err.Error(), "stdin carried no webhook secret") {
		t.Errorf("error %q missing stdin note", err)
	}
}

// TestGitHubConfigWebhookSecretFlagHelp pins the --help wording so the stdin
// form, the env fallback, and the clear semantics stay discoverable from the
// flag alone (issue #190's fix requirement 4).
func TestGitHubConfigWebhookSecretFlagHelp(t *testing.T) {
	cfg := newGitHubConfigCmd()
	f := cfg.Flags().Lookup("webhook-secret")
	if f == nil {
		t.Fatal("flag --webhook-secret not found on github config")
	}
	for _, want := range []string{`"-"`, `"@"`, "$CUPTHREAD_GITHUB_WEBHOOK_SECRET", `"" clears`} {
		if !strings.Contains(f.Usage, want) {
			t.Errorf("webhook-secret usage %q missing %q", f.Usage, want)
		}
	}
}
