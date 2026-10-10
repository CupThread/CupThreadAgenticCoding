//go:build unix

package cmd

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// credentialedConfig is the stored-token config `auth login` leaves behind.
const credentialedConfig = `{"auth":{"method":"token","accessToken":"cpt_secret","tokenPrefix":"cpt_secret"},"defaultWorkspace":"ws_1"}`

// runPermissionCheckRun executes `skills list` (a local, network-free
// command) against a config at cfgPath so PersistentPreRunE runs the
// permission check; the server is never contacted. It returns the captured
// stdout/stderr pair and the file's mode after the run — 0 when the file
// still does not exist.
func runPermissionCheckRun(t *testing.T, cfgPath string, args ...string) (string, string, os.FileMode) {
	t.Helper()
	server := httptest.NewServer(nil)
	t.Cleanup(server.Close)
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL, append([]string{"skills", "list"}, args...)...)
	if err != nil {
		t.Fatalf("skills list: %v", err)
	}
	fi, statErr := os.Stat(cfgPath)
	if statErr != nil {
		if !os.IsNotExist(statErr) {
			t.Fatalf("stat config after run: %v", statErr)
		}
		return stdout, stderr, 0
	}
	return stdout, stderr, fi.Mode().Perm()
}

// TestWorldReadableCredentialConfigWarnsAndRepairs pins the issue #186
// acceptance contract: a group/world-readable config that stores credentials
// makes PersistentPreRunE emit exactly one warning on stderr in table and
// --json modes (stdout stays parse-clean) and tightens the file back to 0600.
func TestWorldReadableCredentialConfigWarnsAndRepairs(t *testing.T) {
	for _, mode := range []string{"table", "json"} {
		t.Run(mode, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(cfgPath, []byte(credentialedConfig), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			var args []string
			if mode == "json" {
				args = []string{"--json"}
			}
			stdout, stderr, perm := runPermissionCheckRun(t, cfgPath, args...)

			if n := strings.Count(stderr, "group/world-readable"); n != 1 {
				t.Fatalf("stderr carries %d warnings, want exactly 1:\n%s", n, stderr)
			}
			if strings.Contains(stdout, "group/world-readable") {
				t.Errorf("stdout carries the warning, want it on stderr only:\n%s", stdout)
			}
			if perm != 0o600 {
				t.Errorf("config perms after run = %04o, want repaired 600", perm)
			}
			if mode == "json" {
				unmarshalOneJSON(t, stdout) // structured stdout stays a single document
			}
		})
	}
}

// TestWorldReadableCredentialConfigRepairFailureDegradesToWarning covers the
// chmod-failure path: a repair that cannot go through (read-only directory,
// foreign ownership) must degrade to the warning-only path — the warning
// names the manual chmod, the command still succeeds, and the file is left
// exactly as it was.
func TestWorldReadableCredentialConfigRepairFailureDegradesToWarning(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(credentialedConfig), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	orig := repairConfigPermissions
	repairConfigPermissions = func(string) error { return errors.New("permission denied") }
	defer func() { repairConfigPermissions = orig }()

	stdout, stderr, perm := runPermissionCheckRun(t, cfgPath)

	if n := strings.Count(stderr, "group/world-readable"); n != 1 {
		t.Fatalf("stderr carries %d warnings, want exactly 1:\n%s", n, stderr)
	}
	for _, want := range []string{"chmod 600", "repair failed"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr warning does not mention %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stdout, "group/world-readable") {
		t.Errorf("stdout carries the warning, want it on stderr only:\n%s", stdout)
	}
	if perm != 0o644 {
		t.Errorf("config perms after failed repair = %04o, want unchanged 644", perm)
	}
}

// TestWorldReadableConfigWithoutCredentialsIsSilent keeps the check from
// producing noise: a loosened config with no stored tokens is not an exposure
// and must not be warned about or rewritten.
func TestWorldReadableConfigWithoutCredentialsIsSilent(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"defaultWorkspace":"ws_1"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	stdout, stderr, perm := runPermissionCheckRun(t, cfgPath)

	if strings.Contains(stderr, "group/world-readable") || strings.Contains(stdout, "group/world-readable") {
		t.Errorf("credential-free config produced a warning:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if perm != 0o644 {
		t.Errorf("config perms after run = %04o, want unchanged 644", perm)
	}
}

// TestOwnerOnlyCredentialConfigIsSilent pins the no-behavior-change half:
// configs the CLI itself created (already 0600) stay warning-free.
func TestOwnerOnlyCredentialConfigIsSilent(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(credentialedConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	stdout, stderr, perm := runPermissionCheckRun(t, cfgPath)

	if strings.Contains(stderr, "group/world-readable") || strings.Contains(stdout, "group/world-readable") {
		t.Errorf("owner-only config produced a warning:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if perm != 0o600 {
		t.Errorf("config perms after run = %04o, want unchanged 600", perm)
	}
}

// TestMissingConfigFileIsSilent ensures the check never turns a first run
// (config not created yet) into an error or a warning.
func TestMissingConfigFileIsSilent(t *testing.T) {
	stdout, stderr, _ := runPermissionCheckRun(t, filepath.Join(t.TempDir(), "absent.json"))
	if strings.Contains(stderr, "group/world-readable") || strings.Contains(stdout, "group/world-readable") {
		t.Errorf("missing config produced a warning:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}
