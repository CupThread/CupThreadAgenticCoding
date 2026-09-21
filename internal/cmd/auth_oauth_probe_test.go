package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
)

// oauthProbeServer answers GET /api/v1/console/me with probeStatus for the
// first probeFailures calls and with the standard me fixture afterwards, so a
// test can log in through a transient outage and then run an authenticated
// command against the recovered server.
func oauthProbeServer(t *testing.T, probeStatus int, probeFailures int64) *httptest.Server {
	t.Helper()
	var meCalls atomic.Int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/console/me" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"unexpected path"}`))
			return
		}
		if meCalls.Add(1) <= probeFailures {
			w.WriteHeader(probeStatus)
			_, _ = w.Write([]byte(`{"error":"boom (transient)"}`))
			return
		}
		_, _ = w.Write([]byte(meFixture))
	}))
}

// deviceFlowServer mocks the device authorization grant end to end: the
// authorize endpoint issues a code with a 1s poll interval, the token
// endpoint immediately returns an approved pair, and the session probe always
// fails with probeStatus.
func deviceFlowServer(t *testing.T, probeStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case auth.DeviceAuthorizePath:
			_, _ = w.Write([]byte(`{"device_code":"dev_code_1","user_code":"ABCD-EFGH","verification_uri":"` + r.Host + `/verify","expires_in":600,"interval":1}`))
		case auth.TokenPath:
			_, _ = w.Write([]byte(`{"access_token":"cpt_dev_access_tok","refresh_token":"cpr_dev_refresh_tok","token_type":"bearer","expires_in":3600}`))
		case "/api/v1/console/me":
			w.WriteHeader(probeStatus)
			_, _ = w.Write([]byte(`{"error":"boom (transient)"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"unexpected path"}`))
		}
	}))
}

// installOAuthApp hand-builds the app singleton the way PersistentPreRunE
// does (config loaded from cfgPath, client built on top), so tests can drive
// finishOAuthLogin directly without walking the interactive flows.
func installOAuthApp(t *testing.T, cfgPath, serverURL string) {
	t.Helper()
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")
	oldFlag, oldA := flagBaseURL, A
	t.Cleanup(func() {
		flagBaseURL = oldFlag
		A = oldA
	})
	flagBaseURL = serverURL
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	A = &app{out: output.New(os.Stdout, output.FormatTable), cfg: cfg, cfgPath: cfgPath}
	A.client = A.buildClient()
}

// captureStdStream swaps *target (os.Stdout or os.Stderr) for a pipe and
// returns a stop function that restores the stream and returns everything
// written in between. The swap is also restored if the test fails before the
// stop function runs, so a fatal cannot poison later tests in the package.
func captureStdStream(t *testing.T, target **os.File) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := *target
	*target = w
	stopped := false
	t.Cleanup(func() {
		if stopped {
			return
		}
		*target = old
		_ = w.Close()
		_, _ = io.Copy(io.Discard, r)
		_ = r.Close()
	})
	return func() string {
		stopped = true
		*target = old
		if err := w.Close(); err != nil {
			t.Errorf("close capture pipe: %v", err)
		}
		data, err := io.ReadAll(r)
		if err != nil {
			t.Errorf("read captured stream: %v", err)
		}
		if err := r.Close(); err != nil {
			t.Errorf("close capture reader: %v", err)
		}
		return string(data)
	}
}

// readConfigFile parses the persisted config for assertions.
func readConfigFile(t *testing.T, cfgPath string) config.Config {
	t.Helper()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

// TestOAuthLoginProbeFailurePersistsIssuedPair pins the core of issue #69:
// when the post-login session check fails transiently after the OAuth pair
// was issued, the pair is already on disk, the command still succeeds, the
// warning goes to stderr, and saved workspace defaults from a previous login
// are cleared because they could not be verified. A follow-up authenticated
// command works with the persisted pair — no re-login needed.
func TestOAuthLoginProbeFailurePersistsIssuedPair(t *testing.T) {
	server := oauthProbeServer(t, http.StatusInternalServerError, 1)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	prev := &config.Config{
		DefaultWorkspace: "ws_stale",
		Workspaces:       map[string]*config.WorkspacePrefs{"ws_stale": {DefaultApp: "app_old"}},
	}
	if err := prev.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	stopStdout := captureStdStream(t, &os.Stdout)
	stopStderr := captureStdStream(t, &os.Stderr)
	installOAuthApp(t, cfgPath, server.URL)

	loginErr := finishOAuthLogin(context.Background(), &auth.TokenSet{
		AccessToken:  "cpt_oauth_access_tok",
		RefreshToken: "cpr_oauth_refresh_tok",
		ExpiresIn:    3600,
	})
	stdout := stopStdout()
	stderr := stopStderr()

	if loginErr != nil {
		t.Fatalf("finishOAuthLogin with failing probe: %v", loginErr)
	}
	if !strings.Contains(stderr, "warning: logged in, but could not verify the session yet") || !strings.Contains(stderr, "cupthread auth status") {
		t.Errorf("stderr = %q, want the advisory warning naming 'cupthread auth status'", stderr)
	}
	for _, want := range []string{"Cleared saved default workspace ws_stale", "Dropped saved per-workspace app default(s) for ws_stale"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, missing disclosure %q", stderr, want)
		}
	}
	if !strings.Contains(stdout, "✓ Logged in (OAuth") || strings.Contains(stdout, "Logged in as") {
		t.Errorf("stdout = %q, want the no-email success line", stdout)
	}

	cfg := readConfigFile(t, cfgPath)
	if cfg.Auth == nil || cfg.Auth.AccessToken != "cpt_oauth_access_tok" || cfg.Auth.RefreshToken != "cpr_oauth_refresh_tok" || cfg.Auth.Method != "oauth" {
		t.Errorf("stored auth = %+v, want the issued oauth pair on disk despite the probe failure", cfg.Auth)
	}
	if cfg.BaseURL != server.URL {
		t.Errorf("stored baseUrl = %q, want %q (remembered even when the probe fails)", cfg.BaseURL, server.URL)
	}
	if cfg.DefaultWorkspace != "" || cfg.Workspaces != nil {
		t.Errorf("stored context = (default %q, workspaces %v), want the unverified previous-login context cleared", cfg.DefaultWorkspace, cfg.Workspaces)
	}

	// The persisted pair authenticates the next command against the
	// recovered server — through the remembered base URL, with no flag.
	out, err := runRootWithCfgPath(t, cfgPath, "me")
	if err != nil {
		t.Fatalf("follow-up me after the failed-probe login: %v\n%s", err, out)
	}
	if !strings.Contains(out, "dev@example.com") {
		t.Errorf("follow-up me output missing identity:\n%s", out)
	}
}

// TestOAuthLoginProbeFailureFreshMachineWarnsOnly covers the first-login case
// with no prior context: the failure path must not invent disclosures, only
// the advisory warning, and still persist everything.
func TestOAuthLoginProbeFailureFreshMachineWarnsOnly(t *testing.T) {
	server := oauthProbeServer(t, http.StatusBadGateway, 1<<62)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")

	stopStdout := captureStdStream(t, &os.Stdout)
	stopStderr := captureStdStream(t, &os.Stderr)
	installOAuthApp(t, cfgPath, server.URL)

	loginErr := finishOAuthLogin(context.Background(), &auth.TokenSet{
		AccessToken:  "cpt_fresh_access_tok",
		RefreshToken: "cpr_fresh_refresh_tok",
		ExpiresIn:    3600,
	})
	stdout := stopStdout()
	stderr := stopStderr()

	if loginErr != nil {
		t.Fatalf("finishOAuthLogin with failing probe: %v", loginErr)
	}
	if !strings.Contains(stderr, "warning: logged in, but could not verify the session yet") {
		t.Errorf("stderr = %q, want the advisory warning", stderr)
	}
	if strings.Contains(stderr, "Cleared saved default workspace") || strings.Contains(stderr, "Dropped saved per-workspace") {
		t.Errorf("stderr = %q, want no context disclosures on a fresh machine", stderr)
	}
	if !strings.Contains(stdout, "✓ Logged in (OAuth") {
		t.Errorf("stdout = %q, want the success line", stdout)
	}

	cfg := readConfigFile(t, cfgPath)
	if cfg.Auth == nil || cfg.Auth.AccessToken != "cpt_fresh_access_tok" || cfg.Auth.RefreshToken != "cpr_fresh_refresh_tok" {
		t.Errorf("stored auth = %+v, want the issued pair on disk", cfg.Auth)
	}
	if cfg.BaseURL != server.URL {
		t.Errorf("stored baseUrl = %q, want %q", cfg.BaseURL, server.URL)
	}
}

// TestOAuthLoginSuccessKeepsVisibleContext pins the unchanged happy path: a
// probe-successful login keeps context the new account can see, warns
// nowhere, and still stores the pair with the email in the success line.
func TestOAuthLoginSuccessKeepsVisibleContext(t *testing.T) {
	server := oauthProbeServer(t, http.StatusInternalServerError, 0)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	prev := &config.Config{
		DefaultWorkspace: "ws_1",
		Workspaces:       map[string]*config.WorkspacePrefs{"ws_1": {DefaultApp: "app_kept"}},
	}
	if err := prev.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	stopStdout := captureStdStream(t, &os.Stdout)
	stopStderr := captureStdStream(t, &os.Stderr)
	installOAuthApp(t, cfgPath, server.URL)

	loginErr := finishOAuthLogin(context.Background(), &auth.TokenSet{
		AccessToken:  "cpt_ok_access_tok",
		RefreshToken: "cpr_ok_refresh_tok",
		ExpiresIn:    3600,
	})
	stdout := stopStdout()
	stderr := stopStderr()

	if loginErr != nil {
		t.Fatalf("finishOAuthLogin: %v", loginErr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty on the success path", stderr)
	}
	if !strings.Contains(stdout, "Logged in as dev@example.com") {
		t.Errorf("stdout = %q, want the email success line", stdout)
	}

	cfg := readConfigFile(t, cfgPath)
	if cfg.DefaultWorkspace != "ws_1" {
		t.Errorf("default workspace = %q, want ws_1 kept", cfg.DefaultWorkspace)
	}
	if prefs := cfg.Workspaces["ws_1"]; prefs == nil || prefs.DefaultApp != "app_kept" {
		t.Errorf("ws_1 prefs = %+v, want the saved app default kept", cfg.Workspaces["ws_1"])
	}
	if cfg.Auth == nil || cfg.Auth.RefreshToken != "cpr_ok_refresh_tok" {
		t.Errorf("stored auth = %+v, want the issued pair", cfg.Auth)
	}
}

// TestOAuthLoginSuccessReconcilesInvisibleContext pins the surgical
// reconcile on the happy path: only context the account cannot see is
// dropped (and the drop is persisted), visible defaults survive.
func TestOAuthLoginSuccessReconcilesInvisibleContext(t *testing.T) {
	server := oauthProbeServer(t, http.StatusInternalServerError, 0)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	prev := &config.Config{
		DefaultWorkspace: "ws_stale",
		Workspaces: map[string]*config.WorkspacePrefs{
			"ws_stale": {DefaultApp: "app_old"},
			"ws_1":     {DefaultApp: "app_kept"},
		},
	}
	if err := prev.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	stopStdout := captureStdStream(t, &os.Stdout)
	stopStderr := captureStdStream(t, &os.Stderr)
	installOAuthApp(t, cfgPath, server.URL)

	loginErr := finishOAuthLogin(context.Background(), &auth.TokenSet{
		AccessToken:  "cpt_rec_access_tok",
		RefreshToken: "cpr_rec_refresh_tok",
		ExpiresIn:    3600,
	})
	stdout := stopStdout()
	stopStderr()

	if loginErr != nil {
		t.Fatalf("finishOAuthLogin: %v", loginErr)
	}
	if !strings.Contains(stdout, "Cleared saved default workspace ws_stale") || !strings.Contains(stdout, "Dropped saved per-workspace app default(s) for ws_stale") {
		t.Errorf("stdout = %q, want the reconcile disclosures", stdout)
	}

	cfg := readConfigFile(t, cfgPath)
	if cfg.DefaultWorkspace != "" {
		t.Errorf("default workspace = %q, want cleared", cfg.DefaultWorkspace)
	}
	if _, ok := cfg.Workspaces["ws_stale"]; ok {
		t.Error("ws_stale prefs survived the reconcile, want dropped")
	}
	if prefs := cfg.Workspaces["ws_1"]; prefs == nil || prefs.DefaultApp != "app_kept" {
		t.Errorf("ws_1 prefs = %+v, want kept", cfg.Workspaces["ws_1"])
	}
}

// TestApplyTokenSetAloneDoesNotPersist pins the regression guard: storing a
// token pair on the in-memory config never writes the file by itself — the
// save happens exactly where login/refresh decide to (the transparent
// refresh's save-once rotation is pinned by TestRefreshUsesStoredBaseURL).
func TestApplyTokenSetAloneDoesNotPersist(t *testing.T) {
	installOAuthApp(t, filepath.Join(t.TempDir(), "config.json"), "http://127.0.0.1:1")

	A.applyTokenSet(&auth.TokenSet{
		AccessToken:  "cpt_guard_access_tok",
		RefreshToken: "cpr_guard_refresh_tok",
		ExpiresIn:    60,
	})

	if _, err := os.Stat(A.cfgPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config file exists after applyTokenSet (stat err %v), want no persist", err)
	}
}

// TestLoginTokenProbeRejectionPersistsNothing pins the --token path staying
// probe-then-save: the probe validates an untrusted PAT before the first
// persist, so a rejection leaves the config untouched (the accepted case is
// pinned by TestLoginTokenFlagTrimsSurroundingSpace).
func TestLoginTokenProbeRejectionPersistsNothing(t *testing.T) {
	server := oauthProbeServer(t, http.StatusUnauthorized, 1<<62)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	out, err := runRootCfg(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_probe_reject_tok")
	if err == nil {
		t.Fatalf("login with rejected token succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "token rejected") {
		t.Errorf("error = %v, want it to name the token rejection", err)
	}
	if _, statErr := os.Stat(cfgPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("config file exists after a rejected token (stat err %v), want nothing written", statErr)
	}
}

// TestDeviceFlowPersistsPairDespiteProbeFailure reproduces the issue's exact
// scenario end to end: a device-flow login approved in the browser whose
// session check then answers 500 must still exit 0 with the exchanged pair
// on disk and the warning on stderr.
func TestDeviceFlowPersistsPairDespiteProbeFailure(t *testing.T) {
	server := deviceFlowServer(t, http.StatusInternalServerError)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")

	oldStdout, oldStderr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW

	flagBaseURL = ""
	flagWorkspace = ""
	flagApp = ""
	flagJSON = false
	flagOutput = ""

	root := newRootCmd()
	root.SetArgs([]string{"auth", "login", "--device", "--base-url", server.URL, "--config", cfgPath})
	execErr := root.Execute()

	os.Stdout, os.Stderr = oldStdout, oldStderr
	_ = outW.Close()
	_ = errW.Close()
	stdoutData, readErr := io.ReadAll(outR)
	if readErr != nil {
		t.Fatalf("read captured stdout: %v", readErr)
	}
	stderrData, readErr := io.ReadAll(errR)
	if readErr != nil {
		t.Fatalf("read captured stderr: %v", readErr)
	}
	_ = outR.Close()
	_ = errR.Close()
	stdout, stderr := string(stdoutData), string(stderrData)

	if execErr != nil {
		t.Fatalf("device login against a failing session check: %v\nstdout:\n%s\nstderr:\n%s", execErr, stdout, stderr)
	}
	if !strings.Contains(stdout, "✓ Logged in (OAuth") || strings.Contains(stdout, "Logged in as") {
		t.Errorf("stdout = %q, want the no-email success line", stdout)
	}
	if !strings.Contains(stderr, "warning: logged in, but could not verify the session yet") || !strings.Contains(stderr, "cupthread auth status") {
		t.Errorf("stderr = %q, want the advisory warning", stderr)
	}

	cfg := readConfigFile(t, cfgPath)
	if cfg.Auth == nil || cfg.Auth.AccessToken != "cpt_dev_access_tok" || cfg.Auth.RefreshToken != "cpr_dev_refresh_tok" {
		t.Errorf("stored auth = %+v, want the exchanged pair on disk despite the probe failure", cfg.Auth)
	}
	if cfg.BaseURL != server.URL {
		t.Errorf("stored baseUrl = %q, want %q", cfg.BaseURL, server.URL)
	}
}
