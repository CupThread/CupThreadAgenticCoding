package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
)

// The tests in this file pin the first-run contract (issue #268): every
// command that persists config works on a machine where the config
// directory does not exist yet. The config paths here always sit inside a
// missing nested tree — a plain t.TempDir() config path can never catch the
// regression, because the seeding directory already exists (which is why CI
// missed it).

// assertStoredTokenLogin checks the config left by a first-run token login:
// the credential and remembered endpoint persisted, and (unix) the
// restrictive file modes Save and LockConfig promise — 0600 file, 0700
// directory.
func assertStoredTokenLogin(t *testing.T, cfgPath, serverURL, token string) {
	t.Helper()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after login: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config after login: %v", err)
	}
	if cfg.Auth == nil || cfg.Auth.AccessToken != token {
		t.Errorf("stored auth = %+v, want token %s", cfg.Auth, token)
	}
	if cfg.BaseURL != serverURL {
		t.Errorf("stored baseUrl = %q, want %q", cfg.BaseURL, serverURL)
	}
	if runtime.GOOS != "windows" {
		// Windows maps the modes to the read-only bit (see
		// TestSaveLeavesNoTempFilesAndKeepsMode); the restrictive-perms
		// guarantee is unix-only.
		if fi, err := os.Stat(cfgPath); err != nil {
			t.Fatalf("stat config: %v", err)
		} else if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("config perms = %o, want 600", perm)
		}
		if fi, err := os.Stat(filepath.Dir(cfgPath)); err != nil {
			t.Fatalf("stat config directory: %v", err)
		} else if perm := fi.Mode().Perm(); perm != 0o700 {
			t.Errorf("config directory perms = %o, want 700", perm)
		}
	}
}

// TestLoginTokenOnPristineConfigTree pins the core first-run fix (issue
// #268): the very first `auth login --token` on a machine without the
// config directory must persist the credential instead of dying on the
// config lock — previously the server-validated token was discarded with
// "open config lock …: no such file or directory".
func TestLoginTokenOnPristineConfigTree(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "") // keep the test hermetic
	t.Setenv("CUPTHREAD_BASE_URL", "")

	server := meServer(t)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "first", "run", "config.json")
	out, err := runRootCfg(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_first_run_01")
	if err != nil {
		t.Fatalf("first-run auth login: %v\n%s", err, out)
	}
	if !strings.Contains(out, "✓ Logged in as") {
		t.Errorf("login output missing success line:\n%s", out)
	}
	assertStoredTokenLogin(t, cfgPath, server.URL, "cpt_first_run_01")

	// The tree now exists; a second login must keep working — and must not
	// degrade the stored modes.
	out, err = runRootCfg(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_first_run_02")
	if err != nil {
		t.Fatalf("second auth login on the now-existing tree: %v\n%s", err, out)
	}
	assertStoredTokenLogin(t, cfgPath, server.URL, "cpt_first_run_02")
}

// TestLogoutOnPristineConfigTreeIsCleanNoOp pins the logout half of issue
// #268: with no config file on disk there is nothing to clear and nothing
// to persist, so logout exits 0 without creating the config file (or its
// directory) just to wipe it.
func TestLogoutOnPristineConfigTreeIsCleanNoOp(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "") // keep the test hermetic

	cfgPath := filepath.Join(t.TempDir(), "pristine", "config.json")
	assertConfigTreeStillAbsent := func(t *testing.T) {
		t.Helper()
		if _, err := os.Stat(cfgPath); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("config file created by logout on a pristine tree (err=%v)", err)
		}
		if _, err := os.Stat(filepath.Dir(cfgPath)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("config directory created by logout on a pristine tree (err=%v)", err)
		}
	}

	t.Run("table", func(t *testing.T) {
		// logout without --revoke never contacts the API, so the base URL
		// is never reached.
		out, err := runRootCfg(t, cfgPath, "http://127.0.0.1:1", "auth", "logout")
		if err != nil {
			t.Fatalf("pristine auth logout: %v\n%s", err, out)
		}
		if !strings.Contains(out, "✓ Already logged out") {
			t.Errorf("pristine logout output missing no-op line:\n%s", out)
		}
		assertConfigTreeStillAbsent(t)
	})

	t.Run("json", func(t *testing.T) {
		stdout, _, err := runRootCapture(t, cfgPath, "http://127.0.0.1:1", "auth", "logout", "--json")
		if err != nil {
			t.Fatalf("pristine auth logout --json: %v\n%s", err, stdout)
		}
		doc := unmarshalOneJSON(t, stdout)
		assertNoTableOutput(t, stdout)
		if doc["loggedOut"] != true {
			t.Errorf("loggedOut = %v, want true", doc["loggedOut"])
		}
		if doc["configPath"] != cfgPath {
			t.Errorf("configPath = %v, want %s", doc["configPath"], cfgPath)
		}
		if _, ok := doc["cleared"]; ok {
			t.Errorf("pristine logout reports cleared context: %v", doc["cleared"])
		}
		assertConfigTreeStillAbsent(t)
	})

	t.Run("revoke without stored credential", func(t *testing.T) {
		// --revoke on a pristine tree has nothing to revoke; the revoker
		// short-circuits before any network call and logout stays a no-op.
		stdout, stderr, err := runRootCapture(t, cfgPath, "http://127.0.0.1:1", "auth", "logout", "--revoke", "--json")
		if err != nil {
			t.Fatalf("pristine auth logout --revoke --json: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if !strings.Contains(stderr, "No stored credential to revoke") {
			t.Errorf("stderr missing no-credential note:\n%s", stderr)
		}
		assertConfigTreeStillAbsent(t)
	})
}
