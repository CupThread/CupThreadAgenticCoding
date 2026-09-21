package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
)

// The tests in this file pin the AGENTS.md machine-output contract for the
// setup commands (issue #68): with --json/--output yaml, auth login/logout,
// workspaces use, apps use and skills link print exactly one structured
// document on stdout, and everything else (progress, warnings, prompts)
// moves to stderr. Table mode stays byte-identical to the pre-#68 output.

// runRootCapture is runRoot capturing BOTH stdout and stderr, with an
// explicit config path. It resets the persistent flag globals so runs stay
// hermetic across the test binary.
func runRootCapture(t *testing.T, cfgPath, serverURL string, args ...string) (string, string, error) {
	t.Helper()

	oldStdout := os.Stdout
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	os.Stdout = outW

	oldStderr := os.Stderr
	errR, errW, pipeErr := os.Pipe()
	if pipeErr != nil {
		os.Stdout = oldStdout
		t.Fatalf("stderr pipe: %v", pipeErr)
	}
	os.Stderr = errW

	flagBaseURL = ""
	flagWorkspace = ""
	flagApp = ""
	flagJSON = false
	flagOutput = ""

	root := newRootCmd()
	full := append(append([]string{}, args...), "--base-url", serverURL, "--config", cfgPath)
	root.SetArgs(full)
	execErr := root.Execute()

	os.Stdout = oldStdout
	os.Stderr = oldStderr
	_ = outW.Close()
	_ = errW.Close()

	outBytes, readErr := io.ReadAll(outR)
	if readErr != nil {
		t.Fatalf("read stdout pipe: %v", readErr)
	}
	errBytes, readErr := io.ReadAll(errR)
	if readErr != nil {
		t.Fatalf("read stderr pipe: %v", readErr)
	}
	return string(outBytes), string(errBytes), execErr
}

// unmarshalOneJSON asserts stdout is exactly one JSON document and returns
// it decoded.
func unmarshalOneJSON(t *testing.T, out string) map[string]any {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v\nstdout:\n%s", err, out)
	}
	return doc
}

// assertNoTableOutput guards the "stdout is machine-readable" half of the
// contract: no human progress lines leak into the structured stream.
func assertNoTableOutput(t *testing.T, out string) {
	t.Helper()

	for _, marker := range []string{"✓", "⚠", "First, open:", "Enter code:"} {
		if strings.Contains(out, marker) {
			t.Errorf("structured stdout contains human marker %q:\n%s", marker, out)
		}
	}
}

// TestAuthLoginTokenJSON covers acceptance criterion 1: a token login with
// --json prints one JSON document naming the method, account and token
// prefix, and nothing else on stdout.
func TestAuthLoginTokenJSON(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	server := meServer(t)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, _, err := runRootCapture(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_json_tok", "--json")
	if err != nil {
		t.Fatalf("auth login --json: %v\n%s", err, out)
	}
	assertNoTableOutput(t, out)
	doc := unmarshalOneJSON(t, out)
	if doc["method"] != "token" {
		t.Errorf("method = %v, want token", doc["method"])
	}
	if doc["email"] != "dev@example.com" {
		t.Errorf("email = %v, want dev@example.com", doc["email"])
	}
	if doc["tokenPrefix"] != "cpt_json_tok" {
		t.Errorf("tokenPrefix = %v, want cpt_json_tok", doc["tokenPrefix"])
	}
	if doc["baseUrl"] != server.URL {
		t.Errorf("baseUrl = %v, want %s", doc["baseUrl"], server.URL)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.Auth == nil || cfg.Auth.AccessToken != "cpt_json_tok" {
		t.Errorf("stored auth = %+v, want the cpt_json_tok credential", cfg.Auth)
	}
}

// TestAuthLoginTokenTableByteIdentical pins acceptance criterion 7 for the
// login confirmation line: the human output is unchanged by the fix.
func TestAuthLoginTokenTableByteIdentical(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	server := meServer(t)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, _, err := runRootCapture(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_json_tok")
	if err != nil {
		t.Fatalf("auth login: %v\n%s", err, out)
	}
	want := "✓ Logged in as dev@example.com (token cpt_json_tok…) at " + server.URL + "\n"
	if out != want {
		t.Errorf("table output = %q, want %q", out, want)
	}
}

// TestAuthLoginDeviceJSON covers acceptance criterion 2 with a mocked device
// flow: the verification URI and user code appear on stderr (both streams of
// progress are prompt, never result), and stdout carries exactly one JSON
// document — the final login result, which echoes the URI and code.
func TestAuthLoginDeviceJSON(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	verifyURI := "https://cupthread.example/activate"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/oauth/device/authorize":
			_, _ = w.Write([]byte(`{"device_code":"dev_code_1","user_code":"ABCD-EFGH","verification_uri":"` + verifyURI + `","interval":1,"expires_in":120}`))
		case r.URL.Path == "/api/v1/oauth/token":
			_, _ = w.Write([]byte(`{"access_token":"cpt_device_tok_0000","refresh_token":"cpt_device_refresh","token_type":"bearer","expires_in":3600,"scope":"console"}`))
		default:
			_, _ = w.Write([]byte(meFixture))
		}
	}))
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, errOut, err := runRootCapture(t, cfgPath, server.URL, "auth", "login", "--device", "--json")
	if err != nil {
		t.Fatalf("auth login --device --json: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}

	if !strings.Contains(errOut, "First, open:  "+verifyURI+"\n") {
		t.Errorf("stderr missing verification URI line:\n%s", errOut)
	}
	if !strings.Contains(errOut, "Enter code:   ABCD-EFGH\n") {
		t.Errorf("stderr missing user code line:\n%s", errOut)
	}

	assertNoTableOutput(t, out)
	doc := unmarshalOneJSON(t, out)
	if doc["method"] != "device" {
		t.Errorf("method = %v, want device", doc["method"])
	}
	if doc["verificationUri"] != verifyURI {
		t.Errorf("verificationUri = %v, want %s", doc["verificationUri"], verifyURI)
	}
	if doc["userCode"] != "ABCD-EFGH" {
		t.Errorf("userCode = %v, want ABCD-EFGH", doc["userCode"])
	}
	if doc["email"] != "dev@example.com" {
		t.Errorf("email = %v, want dev@example.com", doc["email"])
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.Auth == nil || cfg.Auth.Method != "oauth" || cfg.Auth.RefreshToken != "cpt_device_refresh" {
		t.Errorf("stored auth = %+v, want the oauth token pair", cfg.Auth)
	}
}

// TestAuthLogoutJSON covers acceptance criterion 3 plus the cleared-context
// disclosure: the JSON payload names the config file and what inherited
// context was wiped with the credential.
func TestAuthLogoutJSON(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	pre := &config.Config{
		BaseURL:          "https://staging.cupthread.example",
		DefaultWorkspace: "ws_1",
		Workspaces:       map[string]*config.WorkspacePrefs{"ws_1": {DefaultApp: "app_1"}},
		Auth:             &config.Auth{Method: "token", AccessToken: "cpt_tok", TokenPrefix: "cpt_tok"},
	}
	if err := pre.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	out, _, err := runRootCapture(t, cfgPath, "http://127.0.0.1:1", "auth", "logout", "--json")
	if err != nil {
		t.Fatalf("auth logout --json: %v\n%s", err, out)
	}
	assertNoTableOutput(t, out)
	doc := unmarshalOneJSON(t, out)
	if doc["loggedOut"] != true {
		t.Errorf("loggedOut = %v, want true", doc["loggedOut"])
	}
	if doc["configPath"] != cfgPath {
		t.Errorf("configPath = %v, want %s", doc["configPath"], cfgPath)
	}
	wantCleared := []any{"default workspace ws_1", "per-workspace app defaults", "base URL"}
	if !reflect.DeepEqual(doc["cleared"], wantCleared) {
		t.Errorf("cleared = %v, want %v", doc["cleared"], wantCleared)
	}
}

// TestAuthLogoutTableByteIdentical pins criterion 7 for logout.
func TestAuthLogoutTableByteIdentical(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	out, _, err := runRootCapture(t, cfgPath, "http://127.0.0.1:1", "auth", "logout")
	if err != nil {
		t.Fatalf("auth logout: %v\n%s", err, out)
	}
	if want := "✓ Credentials removed from " + cfgPath + "\n"; out != want {
		t.Errorf("table output = %q, want %q", out, want)
	}
}

// TestWorkspacesUseJSON covers acceptance criterion 4: the resolved record
// (ID included) comes back as structured output when a slug was passed, and
// the default is persisted.
func TestWorkspacesUseJSON(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server := meServer(t)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, _, err := runRootCapture(t, cfgPath, server.URL, "workspaces", "use", "owned-co", "--json")
	if err != nil {
		t.Fatalf("workspaces use --json: %v\n%s", err, out)
	}
	assertNoTableOutput(t, out)
	doc := unmarshalOneJSON(t, out)
	ws, ok := doc["defaultWorkspace"].(map[string]any)
	if !ok {
		t.Fatalf("defaultWorkspace missing or not an object: %v", doc["defaultWorkspace"])
	}
	if ws["id"] != "ws_1" || ws["name"] != "Owned Co" || ws["slug"] != "owned-co" {
		t.Errorf("defaultWorkspace = %v, want the resolved ws_1 record", ws)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.DefaultWorkspace != "ws_1" {
		t.Errorf("persisted defaultWorkspace = %q, want ws_1", cfg.DefaultWorkspace)
	}
}

// TestWorkspacesUseTableByteIdentical pins criterion 7 for workspaces use.
func TestWorkspacesUseTableByteIdentical(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server := meServer(t)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, _, err := runRootCapture(t, cfgPath, server.URL, "workspaces", "use", "owned-co")
	if err != nil {
		t.Fatalf("workspaces use: %v\n%s", err, out)
	}
	if want := "✓ Default workspace: Owned Co (ws_1)\n"; out != want {
		t.Errorf("table output = %q, want %q", out, want)
	}
}

// TestAppsUseJSON covers acceptance criterion 5: the resolved app ID comes
// back as structured output when a slug was passed.
func TestAppsUseJSON(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(appListFixture))
	}))
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, _, err := runRootCapture(t, cfgPath, server.URL, "apps", "use", "ios", "-w", "ws_1", "--json")
	if err != nil {
		t.Fatalf("apps use --json: %v\n%s", err, out)
	}
	assertNoTableOutput(t, out)
	doc := unmarshalOneJSON(t, out)
	if doc["workspace"] != "ws_1" {
		t.Errorf("workspace = %v, want ws_1", doc["workspace"])
	}
	appRef, ok := doc["defaultApp"].(map[string]any)
	if !ok {
		t.Fatalf("defaultApp missing or not an object: %v", doc["defaultApp"])
	}
	if appRef["appId"] != "app_1" || appRef["name"] != "Acme iOS" || appRef["slug"] != "ios" {
		t.Errorf("defaultApp = %v, want the resolved app_1 record", appRef)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.Workspaces["ws_1"] == nil || cfg.Workspaces["ws_1"].DefaultApp != "app_1" {
		t.Errorf("persisted workspace prefs = %+v, want defaultApp app_1 for ws_1", cfg.Workspaces)
	}
}

// TestAppsUseTableByteIdentical pins criterion 7 for apps use.
func TestAppsUseTableByteIdentical(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(appListFixture))
	}))
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, _, err := runRootCapture(t, cfgPath, server.URL, "apps", "use", "ios", "-w", "ws_1")
	if err != nil {
		t.Fatalf("apps use: %v\n%s", err, out)
	}
	if want := "✓ Default app for ws_1: Acme iOS (app_1)\n"; out != want {
		t.Errorf("table output = %q, want %q", out, want)
	}
}

// TestSkillsLinkJSON covers acceptance criterion 6: instead of one line per
// agent directory, structured output is a single object with the linked
// count, the skill names, the three targets and the source mode.
func TestSkillsLinkJSON(t *testing.T) {
	source, target := skillsLinkFixture(t)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, _, err := runRootCapture(t, cfgPath, "http://127.0.0.1:1", "skills", "link", target, "--json")
	if err != nil {
		t.Fatalf("skills link --json: %v\n%s", err, out)
	}
	assertNoTableOutput(t, out)
	doc := unmarshalOneJSON(t, out)
	if doc["linked"] != float64(6) {
		t.Errorf("linked = %v, want 6 (2 skills × 3 targets)", doc["linked"])
	}
	if !reflect.DeepEqual(doc["skills"], []any{"cupthread-api", "cupthread-cli"}) {
		t.Errorf("skills = %v, want the two fake skills", doc["skills"])
	}
	if !reflect.DeepEqual(doc["targets"], []any{".agents/skills", ".claude/skills", ".zcode/skills"}) {
		t.Errorf("targets = %v, want the three agent dirs", doc["targets"])
	}
	if doc["source"] != "checkout" {
		t.Errorf("source = %v, want checkout", doc["source"])
	}
	for _, agentDir := range agentSkillDirs {
		assertSkillLink(t, filepath.Join(target, agentDir, "cupthread-api"), filepath.Join(source, "skills", "cupthread-api"))
	}
}

// TestSkillsLinkJSONEmbeddedSource pins the source field for the embedded
// (Homebrew / go install) mode, where skills are copied rather than linked.
func TestSkillsLinkJSONEmbeddedSource(t *testing.T) {
	_, target := skillsLinkFixture(t)
	t.Chdir(t.TempDir()) // leave the fake checkout: neither cwd nor the test binary is inside one

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, _, err := runRootCapture(t, cfgPath, "http://127.0.0.1:1", "skills", "link", target, "--json")
	if err != nil {
		t.Fatalf("skills link --json (embedded): %v\n%s", err, out)
	}
	doc := unmarshalOneJSON(t, out)
	if doc["source"] != "embedded" {
		t.Errorf("source = %v, want embedded", doc["source"])
	}
	// Embedded mode lists the six bundled skills, not the fake checkout's
	// two: 6 × 3 targets = 18.
	if doc["linked"] != float64(18) {
		t.Errorf("linked = %v, want 18", doc["linked"])
	}
	link := filepath.Join(target, ".agents/skills", "cupthread-api")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("stat %s: %v", link, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("embedded install produced a symlink, want a real copied directory")
	}
}

// TestLoginWarningsRoutedByMode pins the warning routing that keeps the
// structured stream parseable: reconcile warnings move to stderr in --json
// mode and stay on stdout in table mode.
func TestLoginWarningsRoutedByMode(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	seed := func(cfgPath string) {
		pre := &config.Config{
			DefaultWorkspace: "ws_stale",
			Workspaces:       map[string]*config.WorkspacePrefs{"ws_stale": {DefaultApp: "app_ghost"}},
		}
		if err := pre.Save(cfgPath); err != nil {
			t.Fatalf("seed config: %v", err)
		}
	}

	server := meServer(t)
	defer server.Close()

	// JSON mode: warnings go to stderr; stdout stays one document.
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seed(cfgPath)
	out, errOut, err := runRootCapture(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_json_tok", "--json")
	if err != nil {
		t.Fatalf("login --json with stale context: %v\n%s", err, out)
	}
	assertNoTableOutput(t, out)
	unmarshalOneJSON(t, out)
	for _, want := range []string{
		"⚠ Cleared saved default workspace ws_stale",
		"⚠ Dropped saved per-workspace app default(s) for ws_stale",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing warning %q:\n%s", want, errOut)
		}
	}

	// Table mode: warnings stay on stdout, byte-for-byte as before the fix.
	cfgPath = filepath.Join(t.TempDir(), "config.json")
	seed(cfgPath)
	out, _, err = runRootCapture(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_json_tok")
	if err != nil {
		t.Fatalf("login with stale context: %v\n%s", err, out)
	}
	if !strings.Contains(out, "⚠ Cleared saved default workspace ws_stale") {
		t.Errorf("table stdout missing reconcile warning:\n%s", out)
	}
}
