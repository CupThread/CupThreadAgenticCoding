package cmd

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
)

// The tests in this file pin the cross-process safety of ordinary config
// mutations (issue #139): saveConfig must hold the config lock, merge only
// this invocation's changes onto a fresh read of the on-disk file, and write
// through a unique temporary file, so two concurrent CLI invocations never
// lose each other's workspace/app defaults or credentials.

// raceApp builds an app pointed at cfgPath the way PersistentPreRunE does,
// including the load-time baseline snapshot saveConfig diffs against.
func raceApp(t *testing.T, cfgPath string) *app {
	t.Helper()
	a := &app{out: output.New(io.Discard, output.FormatTable), cfgPath: cfgPath}
	var err error
	a.cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	a.cfgBaseline = a.cfg.Snapshot()
	return a
}

// seedContextConfig writes the shared starting snapshot for the two-writer
// tests: one credential, one default workspace, one per-workspace app
// default.
func seedContextConfig(t *testing.T, cfgPath string) {
	t.Helper()
	seed := &config.Config{
		DefaultWorkspace: "ws_a",
		Workspaces:       map[string]*config.WorkspacePrefs{"ws_a": {DefaultApp: "app_old"}},
		Auth:             &config.Auth{Method: "token", AccessToken: "cpt_secret", TokenPrefix: "cpt_secret"},
	}
	if err := seed.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

// assertCleanConfigState verifies the on-disk outcome every race test
// expects: mode 0600, no leftover temporary files, and a loadable document.
func assertCleanConfigState(t *testing.T, cfgPath string) {
	t.Helper()
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if runtime.GOOS != "windows" {
		// Windows maps os.CreateTemp's 0600 mode to the read-only bit, so
		// Perm() reports 0666 there; the restrictive-perms guarantee is
		// unix-only.
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("config perms = %o, want 600", perm)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(cfgPath))
	if err != nil {
		t.Fatalf("read config directory: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") || strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("leftover temporary file in config directory: %s", entry.Name())
		}
	}
}

// TestConcurrentWorkspaceUseAndAppsUsePreserveBothChanges is the issue's
// headline reproduction: two invocations start from the same snapshot, one
// runs the mutation tail of 'workspaces use ws_b', the other the mutation
// tail of 'apps use app_new' — and the final config must carry both changes
// plus the untouched credential.
func TestConcurrentWorkspaceUseAndAppsUsePreserveBothChanges(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedContextConfig(t, cfgPath)

	wsApp, appsApp := raceApp(t, cfgPath), raceApp(t, cfgPath)

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		wsApp.cfg.DefaultWorkspace = "ws_b"
		if err := wsApp.saveConfig(); err != nil {
			t.Errorf("workspaces-use save: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		appsApp.cfg.WorkspacePrefsFor("ws_a").DefaultApp = "app_new"
		if err := appsApp.saveConfig(); err != nil {
			t.Errorf("apps-use save: %v", err)
		}
	}()
	close(start)
	wg.Wait()

	disk, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load final config: %v", err)
	}
	if disk.DefaultWorkspace != "ws_b" {
		t.Errorf("DefaultWorkspace = %q, want ws_b (lost to a concurrent save)", disk.DefaultWorkspace)
	}
	if disk.Workspaces["ws_a"].DefaultApp != "app_new" {
		t.Errorf("ws_a DefaultApp = %q, want app_new (lost to a concurrent save)", disk.Workspaces["ws_a"].DefaultApp)
	}
	if disk.Auth == nil || disk.Auth.AccessToken != "cpt_secret" {
		t.Errorf("Auth = %+v, want the seeded credential", disk.Auth)
	}
	assertCleanConfigState(t, cfgPath)
}

// TestLoginSavePreservesConcurrentWorkspaceDefault races a login (a fresh
// credential) against a 'workspaces use': the credential write must not
// discard the concurrently selected default workspace, and vice versa.
func TestLoginSavePreservesConcurrentWorkspaceDefault(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedContextConfig(t, cfgPath)

	loginApp, wsApp := raceApp(t, cfgPath), raceApp(t, cfgPath)

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		loginApp.cfg.Auth = &config.Auth{Method: "token", AccessToken: "cpt_new", TokenPrefix: "cpt_new"}
		if err := loginApp.saveConfig(); err != nil {
			t.Errorf("login save: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		wsApp.cfg.DefaultWorkspace = "ws_b"
		if err := wsApp.saveConfig(); err != nil {
			t.Errorf("workspaces-use save: %v", err)
		}
	}()
	close(start)
	wg.Wait()

	disk, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load final config: %v", err)
	}
	if disk.Auth == nil || disk.Auth.AccessToken != "cpt_new" {
		t.Errorf("Auth = %+v, want cpt_new", disk.Auth)
	}
	if disk.DefaultWorkspace != "ws_b" {
		t.Errorf("DefaultWorkspace = %q, want ws_b", disk.DefaultWorkspace)
	}
	assertCleanConfigState(t, cfgPath)
}

// TestSuccessiveSavesCarryOnlyNewerChanges pins the baseline adoption after
// a save: a first save persists the invocation's own change, a concurrent
// writer then lands a different field on disk, and a second save from the
// same invocation (the finishOAuthLogin pattern) must apply only its new
// change without reverting the concurrent write.
func TestSuccessiveSavesCarryOnlyNewerChanges(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedContextConfig(t, cfgPath)

	a := raceApp(t, cfgPath)

	// First save: this invocation stores a fresh credential.
	a.cfg.Auth = &config.Auth{Method: "token", AccessToken: "cpt_new", TokenPrefix: "cpt_new"}
	if err := a.saveConfig(); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// A concurrent invocation switches the per-workspace app default.
	concurrent, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load for concurrent write: %v", err)
	}
	concurrent.WorkspacePrefsFor("ws_a").DefaultApp = "app_concurrent"
	if err := concurrent.Save(cfgPath); err != nil {
		t.Fatalf("concurrent write: %v", err)
	}

	// Second save from the same invocation: context reconciliation drops
	// the saved default workspace. It must not revert the app default the
	// concurrent writer stored, nor re-litigate the already-persisted auth.
	a.cfg.DefaultWorkspace = ""
	if err := a.saveConfig(); err != nil {
		t.Fatalf("second save: %v", err)
	}

	disk, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load final config: %v", err)
	}
	if disk.DefaultWorkspace != "" {
		t.Errorf("DefaultWorkspace = %q, want empty (clear not applied)", disk.DefaultWorkspace)
	}
	if disk.Workspaces["ws_a"].DefaultApp != "app_concurrent" {
		t.Errorf("ws_a DefaultApp = %q, want app_concurrent (reverted by a stale save)", disk.Workspaces["ws_a"].DefaultApp)
	}
	if disk.Auth == nil || disk.Auth.AccessToken != "cpt_new" {
		t.Errorf("Auth = %+v, want cpt_new", disk.Auth)
	}
}
