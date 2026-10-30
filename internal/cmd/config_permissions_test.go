//go:build unix

package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
)

// credentialedConfig is the stored-token config `auth login` leaves behind.
const credentialedConfig = `{"auth":{"method":"token","accessToken":"cpt_secret","tokenPrefix":"cpt_secret"},"defaultWorkspace":"ws_1"}`

// writeFileMode seeds a fixture whose mode survives the process umask:
// os.WriteFile applies umask at create time, so a 0644 seed under umask 0077
// lands at 0600 and the permission contract under test never holds (issue
// #281). The follow-up os.Chmod is not umask-filtered.
func writeFileMode(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("seed mode %04o on %s: %v", mode, path, err)
	}
}

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

// TestGroupWorldAccessibleCredentialConfigWarnsAndRepairs pins the issue #186
// and #390 acceptance contract: a config that stores credentials and is
// readable OR writable by group/other makes PersistentPreRunE emit exactly
// one warning on stderr in table and --json modes (stdout stays
// parse-clean) and tightens the file back to 0600. The writable seeds are
// the #390 half: write access lets a local account repoint baseUrl so the
// next command leaks the stored token, which is as dangerous as read access.
func TestGroupWorldAccessibleCredentialConfigWarnsAndRepairs(t *testing.T) {
	loosened := []os.FileMode{0o644, 0o640, 0o620, 0o602}
	for _, loosen := range loosened {
		for _, mode := range []string{"table", "json"} {
			t.Run(fmt.Sprintf("%04o/%s", loosen, mode), func(t *testing.T) {
				cfgPath := filepath.Join(t.TempDir(), "config.json")
				writeFileMode(t, cfgPath, []byte(credentialedConfig), loosen)
				var args []string
				if mode == "json" {
					args = []string{"--json"}
				}
				stdout, stderr, perm := runPermissionCheckRun(t, cfgPath, args...)

				if n := strings.Count(stderr, "group/world-accessible"); n != 1 {
					t.Fatalf("stderr carries %d warnings, want exactly 1:\n%s", n, stderr)
				}
				if strings.Contains(stdout, "group/world-accessible") {
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
}

// TestGroupWorldAccessibleCredentialConfigRepairFailureDegradesToWarning
// covers the chmod-failure path: a repair that cannot go through (read-only
// directory, foreign ownership) must degrade to the warning-only path — the
// warning names the manual chmod, the command still succeeds, and the file is
// left exactly as it was.
func TestGroupWorldAccessibleCredentialConfigRepairFailureDegradesToWarning(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	writeFileMode(t, cfgPath, []byte(credentialedConfig), 0o644)
	orig := repairConfigPermissions
	repairConfigPermissions = func(string) error { return errors.New("permission denied") }
	defer func() { repairConfigPermissions = orig }()

	stdout, stderr, perm := runPermissionCheckRun(t, cfgPath)

	if n := strings.Count(stderr, "group/world-accessible"); n != 1 {
		t.Fatalf("stderr carries %d warnings, want exactly 1:\n%s", n, stderr)
	}
	for _, want := range []string{"chmod 600", "repair failed"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr warning does not mention %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stdout, "group/world-accessible") {
		t.Errorf("stdout carries the warning, want it on stderr only:\n%s", stdout)
	}
	if perm != 0o644 {
		t.Errorf("config perms after failed repair = %04o, want unchanged 644", perm)
	}
}

// TestGroupWorldAccessibleConfigWithoutCredentialsIsSilent keeps the check
// from producing noise: a loosened config with no stored tokens is not an
// exposure and must not be warned about or rewritten.
func TestGroupWorldAccessibleConfigWithoutCredentialsIsSilent(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	writeFileMode(t, cfgPath, []byte(`{"defaultWorkspace":"ws_1"}`), 0o644)
	stdout, stderr, perm := runPermissionCheckRun(t, cfgPath)

	if strings.Contains(stderr, "group/world-accessible") || strings.Contains(stdout, "group/world-accessible") {
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
	writeFileMode(t, cfgPath, []byte(credentialedConfig), 0o600)
	stdout, stderr, perm := runPermissionCheckRun(t, cfgPath)

	if strings.Contains(stderr, "group/world-accessible") || strings.Contains(stdout, "group/world-accessible") {
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
	if strings.Contains(stderr, "group/world-accessible") || strings.Contains(stdout, "group/world-accessible") {
		t.Errorf("missing config produced a warning:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// oauthRecoveryPair is the recovery-file payload writeRecoveryCredential
// leaves behind: the config.Auth struct verbatim (issue #299). The sweep
// under test never parses it — the file is credential-bearing by
// construction — but a realistic payload keeps the fixture honest.
const oauthRecoveryPair = `{"method":"oauth","accessToken":"cpt_x","refreshToken":"cpt_y","expiresAt":"2030-01-01T00:00:00Z","clientId":"cupthread-cli"}`

// TestRecoveryFileGroupWorldAccessibleWarnsAndRepairs pins the issue #401
// acceptance contract: a recovery file beside the config that is readable or
// writable by group/other makes PersistentPreRunE emit exactly one warning on
// stderr in table and --json modes (stdout stays parse-clean) and tighten the
// file back to 0600, exactly mirroring the config-file behavior — the
// recovery file holds the same full bearer+refresh pair and is auto-adopted
// as a live credential, so an exposure there is at least as serious. The
// config itself is seeded clean so the warning count isolates the new sweep.
func TestRecoveryFileGroupWorldAccessibleWarnsAndRepairs(t *testing.T) {
	loosened := []os.FileMode{0o644, 0o640, 0o620, 0o602}
	for _, loosen := range loosened {
		for _, mode := range []string{"table", "json"} {
			t.Run(fmt.Sprintf("%04o/%s", loosen, mode), func(t *testing.T) {
				cfgPath := filepath.Join(t.TempDir(), "config.json")
				writeFileMode(t, cfgPath, []byte(credentialedConfig), 0o600)
				recovery := cfgPath + ".recovery-1729000000000000000"
				writeFileMode(t, recovery, []byte(oauthRecoveryPair), loosen)
				var args []string
				if mode == "json" {
					args = []string{"--json"}
				}
				stdout, stderr, cfgPerm := runPermissionCheckRun(t, cfgPath, args...)

				if n := strings.Count(stderr, "group/world-accessible"); n != 1 {
					t.Fatalf("stderr carries %d warnings, want exactly 1:\n%s", n, stderr)
				}
				for _, want := range []string{"OAuth recovery file", recovery} {
					if !strings.Contains(stderr, want) {
						t.Errorf("stderr warning does not mention %q:\n%s", want, stderr)
					}
				}
				if strings.Contains(stdout, "group/world-accessible") {
					t.Errorf("stdout carries the warning, want it on stderr only:\n%s", stdout)
				}
				recPerm := filePermOrZero(t, recovery)
				if recPerm != 0o600 {
					t.Errorf("recovery file perms after run = %04o, want repaired 600", recPerm)
				}
				if cfgPerm != 0o600 {
					t.Errorf("config perms after run = %04o, want unchanged 600", cfgPerm)
				}
				if mode == "json" {
					unmarshalOneJSON(t, stdout) // structured stdout stays a single document
				}
			})
		}
	}
}

// TestLoosenedConfigAndRecoveryFileAreBothFlaggedAndRepaired covers the
// dotfiles-sync scenario both fixtures come from: when the same loosening hit
// the config and the recovery file beside it, each gets its own warning and
// its own repair — one sweep run, two exposures closed.
func TestLoosenedConfigAndRecoveryFileAreBothFlaggedAndRepaired(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	writeFileMode(t, cfgPath, []byte(credentialedConfig), 0o644)
	recovery := cfgPath + ".recovery-1729000000000000000"
	writeFileMode(t, recovery, []byte(oauthRecoveryPair), 0o662)

	stdout, stderr, cfgPerm := runPermissionCheckRun(t, cfgPath)

	if n := strings.Count(stderr, "group/world-accessible"); n != 2 {
		t.Fatalf("stderr carries %d warnings, want exactly 2:\n%s", n, stderr)
	}
	for _, want := range []string{"config file", "OAuth recovery file", cfgPath, recovery} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr warnings do not mention %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stdout, "group/world-accessible") {
		t.Errorf("stdout carries the warning, want it on stderr only:\n%s", stdout)
	}
	if cfgPerm != 0o600 {
		t.Errorf("config perms after run = %04o, want repaired 600", cfgPerm)
	}
	if recPerm := filePermOrZero(t, recovery); recPerm != 0o600 {
		t.Errorf("recovery file perms after run = %04o, want repaired 600", recPerm)
	}
}

// TestRecoveryFileFlaggedEvenWhenConfigStoresNoCredentials pins the sweep's
// gating: a recovery file is credential-bearing by construction, so the
// check must not consult the config's own StoresCredentials — a stale
// recovery file next to a since-logged-out (or credential-free) config is
// still a live adoptable bearer+refresh pair.
func TestRecoveryFileFlaggedEvenWhenConfigStoresNoCredentials(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	writeFileMode(t, cfgPath, []byte(`{"defaultWorkspace":"ws_1"}`), 0o600)
	recovery := cfgPath + ".recovery-1729000000000000000"
	writeFileMode(t, recovery, []byte(oauthRecoveryPair), 0o644)

	stdout, stderr, cfgPerm := runPermissionCheckRun(t, cfgPath)

	if n := strings.Count(stderr, "group/world-accessible"); n != 1 {
		t.Fatalf("stderr carries %d warnings, want exactly 1:\n%s", n, stderr)
	}
	if !strings.Contains(stderr, "OAuth recovery file") {
		t.Errorf("stderr warning does not name the recovery file:\n%s", stderr)
	}
	if strings.Contains(stderr, "config file") {
		t.Errorf("stderr warns about the credential-free config:\n%s", stderr)
	}
	if strings.Contains(stdout, "group/world-accessible") {
		t.Errorf("stdout carries the warning, want it on stderr only:\n%s", stdout)
	}
	if recPerm := filePermOrZero(t, recovery); recPerm != 0o600 {
		t.Errorf("recovery file perms after run = %04o, want repaired 600", recPerm)
	}
	if cfgPerm != 0o600 {
		t.Errorf("config perms after run = %04o, want unchanged 600", cfgPerm)
	}
}

// TestOwnerOnlyRecoveryFileIsSilent keeps the sweep from producing noise: a
// recovery file at the 0600 the CLI itself wrote stays warning-free.
func TestOwnerOnlyRecoveryFileIsSilent(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	writeFileMode(t, cfgPath, []byte(credentialedConfig), 0o600)
	recovery := cfgPath + ".recovery-1729000000000000000"
	writeFileMode(t, recovery, []byte(oauthRecoveryPair), 0o600)

	stdout, stderr, _ := runPermissionCheckRun(t, cfgPath)
	if strings.Contains(stderr, "group/world-accessible") || strings.Contains(stdout, "group/world-accessible") {
		t.Errorf("owner-only recovery file produced a warning:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// TestRecoveryFileRepairFailureDegradesToWarning covers the chmod-failure
// path of the recovery sweep: a repair that cannot go through (read-only
// directory, foreign ownership) degrades to the warning-only path — the
// warning names the manual chmod, the command still succeeds, and the file
// is left exactly as it was.
func TestRecoveryFileRepairFailureDegradesToWarning(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	writeFileMode(t, cfgPath, []byte(credentialedConfig), 0o600)
	recovery := cfgPath + ".recovery-1729000000000000000"
	writeFileMode(t, recovery, []byte(oauthRecoveryPair), 0o644)
	orig := repairConfigPermissions
	repairConfigPermissions = func(string) error { return errors.New("permission denied") }
	defer func() { repairConfigPermissions = orig }()

	stdout, stderr, _ := runPermissionCheckRun(t, cfgPath)

	if n := strings.Count(stderr, "group/world-accessible"); n != 1 {
		t.Fatalf("stderr carries %d warnings, want exactly 1:\n%s", n, stderr)
	}
	for _, want := range []string{"OAuth recovery file", "chmod 600", "repair failed"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr warning does not mention %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stdout, "group/world-accessible") {
		t.Errorf("stdout carries the warning, want it on stderr only:\n%s", stdout)
	}
	if recPerm := filePermOrZero(t, recovery); recPerm != 0o644 {
		t.Errorf("recovery file perms after failed repair = %04o, want unchanged 644", recPerm)
	}
}

// TestVanishedRecoveryFileBeforeRepairIsSilent pins the race tolerance of the
// sweep: a recovery file removed between the glob and the chmod (another
// process's successful login/logout purge) must not surface as a repair
// failure — it is the same not-an-error race removeRecoveryFiles tolerates.
func TestVanishedRecoveryFileBeforeRepairIsSilent(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	writeFileMode(t, cfgPath, []byte(credentialedConfig), 0o600)
	recovery := cfgPath + ".recovery-1729000000000000000"
	writeFileMode(t, recovery, []byte(oauthRecoveryPair), 0o644)
	orig := repairConfigPermissions
	repairConfigPermissions = func(string) error {
		return fmt.Errorf("chmod: %w", fs.ErrNotExist)
	}
	defer func() { repairConfigPermissions = orig }()

	stdout, stderr, _ := runPermissionCheckRun(t, cfgPath)
	if strings.Contains(stderr, "group/world-accessible") || strings.Contains(stdout, "group/world-accessible") {
		t.Errorf("vanished recovery file produced a warning:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// TestWriteRecoveryCredentialReassertsOwnerMode pins the write-time half of
// issue #401: the 0600 argument to OpenFile is filtered through the process
// umask at create time, so a umask that strips owner bits (0277 — an odd but
// possible dotfiles default) lands the recovery file at 0400 and the next
// invalid_grant would silently skip the only record of the live rotation.
// writeRecoveryCredential re-asserts the exact mode, which can only ever add
// back what the umask stripped and never opens group/world access. The test
// swaps the process umask and restores it before the fixture directory is
// removed, so it must stay serial (no t.Parallel).
func TestWriteRecoveryCredentialReassertsOwnerMode(t *testing.T) {
	dir, err := os.MkdirTemp("", "cupthread-recovery-umask")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() {
		// The hostile umask below strips the directory's owner-write bit,
		// which RemoveAll needs; restore the mode before removal.
		_ = os.Chmod(dir, 0o700)
		_ = os.RemoveAll(dir)
	})
	old := syscall.Umask(0o277)
	defer syscall.Umask(old)

	path, err := writeRecoveryCredential(filepath.Join(dir, "config.json"), &config.Auth{
		Method:       "oauth",
		AccessToken:  "cpt_x",
		RefreshToken: "cpt_y",
	})
	if err != nil {
		t.Fatalf("writeRecoveryCredential: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat recovery file: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("recovery file mode under umask 0277 = %04o, want re-asserted 600", got)
	}
}

// filePermOrZero reports the permission bits of path, or 0 when the file no
// longer exists (the caller decides whether that is the expectation).
func filePermOrZero(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", path, err)
		}
		return 0
	}
	return fi.Mode().Perm()
}
