package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// seedConfig writes cfg to path and fails the test on any error.
func seedConfig(t *testing.T, path string, cfg *Config) {
	t.Helper()
	if err := cfg.Save(path); err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

// readConfig loads the config at path and fails the test on any error.
func readConfig(t *testing.T, path string) *Config {
	t.Helper()
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

// baseSeedConfig is the shared initial snapshot for the update-race tests:
// one credential, one default workspace, one per-workspace app default.
func baseSeedConfig() *Config {
	return &Config{
		DefaultWorkspace: "ws_a",
		Workspaces:       map[string]*WorkspacePrefs{"ws_a": {DefaultApp: "app_old"}},
		Auth:             &Auth{Method: "token", AccessToken: "cpt_old", TokenPrefix: "cpt_old"},
	}
}

// assertNoTempFiles fails the test if the config directory holds leftover
// temporary files from a save.
func assertNoTempFiles(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read config directory: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") || strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("leftover temporary file in config directory: %s", entry.Name())
		}
	}
}

func TestSaveLeavesNoTempFilesAndKeepsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seedConfig(t, path, baseSeedConfig())

	if runtime.GOOS != "windows" {
		// Windows maps os.CreateTemp's 0600 mode to the read-only bit, so
		// Perm() reports 0666 there; the restrictive-perms guarantee is
		// unix-only.
		if perm := mustMode(t, path); perm != 0o600 {
			t.Fatalf("config perms = %o, want 600", perm)
		}
	}
	// The old shared-name temp file must not exist either — a stale
	// <config>.tmp next to the config would mean a save died mid-flight.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("shared-name temp file exists after save (err=%v)", err)
	}
	assertNoTempFiles(t, path)
	if got := readConfig(t, path); got.DefaultWorkspace != "ws_a" {
		t.Errorf("round-trip lost DefaultWorkspace: %+v", got)
	}
}

func mustMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// TestSaveConcurrentWritersAllSucceed hammers one config path with parallel
// saves: every save gets its own temporary file, so none may fail on a
// shared temp name and none may leave one behind.
func TestSaveConcurrentWritersAllSucceed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seedConfig(t, path, baseSeedConfig())

	const writers = 8
	var wg sync.WaitGroup
	errs := make([]error, writers)
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer wg.Done()
			cfg := baseSeedConfig()
			cfg.DefaultWorkspace = "ws_from_writer"
			errs[i] = cfg.Save(path)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent save %d failed: %v", i, err)
		}
	}
	assertNoTempFiles(t, path)
	if got := readConfig(t, path); got.DefaultWorkspace != "ws_from_writer" {
		t.Errorf("DefaultWorkspace = %q, want ws_from_writer", got.DefaultWorkspace)
	}
}

// TestUpdatePreservesConcurrentCredentialWrite pins the lost-update fix: a
// concurrent login commits a new credential after this invocation loaded the
// config, then this invocation saves a default-workspace change. The merged
// write must carry both — the old whole-file save discarded the credential.
func TestUpdatePreservesConcurrentCredentialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := baseSeedConfig()
	seedConfig(t, path, seed)

	baseline := seed.Snapshot()

	// Another invocation logs in with a fresh credential while ours runs.
	concurrent := seed.Snapshot()
	concurrent.Auth = &Auth{Method: "token", AccessToken: "cpt_new", TokenPrefix: "cpt_new"}
	seedConfig(t, path, concurrent)

	// Our invocation only switches the default workspace.
	updated := seed.Snapshot()
	updated.DefaultWorkspace = "ws_b"

	merged, err := Update(path, baseline, updated)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if merged.DefaultWorkspace != "ws_b" {
		t.Errorf("DefaultWorkspace = %q, want ws_b (our change lost)", merged.DefaultWorkspace)
	}
	if merged.Auth == nil || merged.Auth.AccessToken != "cpt_new" {
		t.Errorf("Auth = %+v, want the concurrent cpt_new credential (clobbered)", merged.Auth)
	}
	if disk := readConfig(t, path); disk.Auth == nil || disk.Auth.AccessToken != "cpt_new" {
		t.Errorf("on-disk Auth = %+v, want cpt_new", disk.Auth)
	}
}

// TestUpdatePreservesConcurrentWorkspaceDefault is the mirror case: the
// concurrent writer changes a per-workspace app default while we switch the
// default workspace; both must survive.
func TestUpdatePreservesConcurrentWorkspaceDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := baseSeedConfig()
	seedConfig(t, path, seed)

	baseline := seed.Snapshot()

	concurrent := seed.Snapshot()
	concurrent.WorkspacePrefsFor("ws_a").DefaultApp = "app_new"
	seedConfig(t, path, concurrent)

	updated := seed.Snapshot()
	updated.DefaultWorkspace = "ws_b"

	merged, err := Update(path, baseline, updated)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if merged.DefaultWorkspace != "ws_b" {
		t.Errorf("DefaultWorkspace = %q, want ws_b", merged.DefaultWorkspace)
	}
	if merged.Workspaces["ws_a"].DefaultApp != "app_new" {
		t.Errorf("ws_a DefaultApp = %q, want app_new (concurrent change lost)", merged.Workspaces["ws_a"].DefaultApp)
	}
}

// TestUpdateAppliesDroppedWorkspacePrefs checks that workspace ids this
// invocation dropped (context reconciliation on login) are removed even when
// a concurrent write touched other fields.
func TestUpdateAppliesDroppedWorkspacePrefs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := baseSeedConfig()
	seed.Workspaces["ws_ghost"] = &WorkspacePrefs{DefaultApp: "app_ghost"}
	seedConfig(t, path, seed)

	baseline := seed.Snapshot()

	// Concurrent write lands a fresh credential.
	concurrent := seed.Snapshot()
	concurrent.Auth = &Auth{Method: "token", AccessToken: "cpt_new", TokenPrefix: "cpt_new"}
	seedConfig(t, path, concurrent)

	// Our invocation drops the invisible workspace's prefs.
	updated := seed.Snapshot()
	delete(updated.Workspaces, "ws_ghost")

	merged, err := Update(path, baseline, updated)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, ok := merged.Workspaces["ws_ghost"]; ok {
		t.Errorf("ws_ghost prefs survived the drop: %+v", merged.Workspaces)
	}
	if merged.Workspaces["ws_a"].DefaultApp != "app_old" {
		t.Errorf("ws_a DefaultApp = %q, want app_old (unrelated change clobbered)", merged.Workspaces["ws_a"].DefaultApp)
	}
	if merged.Auth == nil || merged.Auth.AccessToken != "cpt_new" {
		t.Errorf("Auth = %+v, want cpt_new", merged.Auth)
	}
}

// TestUpdateTwoWritersFromSameBaseline is the barrier-based two-writer test
// from the issue: both writers start from the same on-disk snapshot, apply
// independent changes concurrently through Update, and both changes must be
// on disk afterwards — every run, not just on lucky interleavings.
func TestUpdateTwoWritersFromSameBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := baseSeedConfig()
	seedConfig(t, path, seed)
	baseline := seed.Snapshot()

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		updated := seed.Snapshot()
		updated.DefaultWorkspace = "ws_b"
		if _, err := Update(path, baseline, updated); err != nil {
			t.Errorf("workspace writer Update: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		updated := seed.Snapshot()
		updated.WorkspacePrefsFor("ws_a").DefaultApp = "app_new"
		if _, err := Update(path, baseline, updated); err != nil {
			t.Errorf("app writer Update: %v", err)
		}
	}()
	close(start)
	wg.Wait()

	disk := readConfig(t, path)
	if disk.DefaultWorkspace != "ws_b" {
		t.Errorf("DefaultWorkspace = %q, want ws_b", disk.DefaultWorkspace)
	}
	if disk.Workspaces["ws_a"].DefaultApp != "app_new" {
		t.Errorf("ws_a DefaultApp = %q, want app_new", disk.Workspaces["ws_a"].DefaultApp)
	}
	if disk.Auth == nil || disk.Auth.AccessToken != "cpt_old" {
		t.Errorf("Auth = %+v, want the seeded credential", disk.Auth)
	}
	assertNoTempFiles(t, path)
	if runtime.GOOS != "windows" {
		if perm := mustMode(t, path); perm != 0o600 {
			t.Errorf("config perms = %o, want 600", perm)
		}
	}
}

// TestApplyChangesFieldMatrix walks the field-level merge semantics: only
// fields where updated differs from baseline reach c; everything else keeps
// the value c (the fresh disk read) already holds.
func TestApplyChangesFieldMatrix(t *testing.T) {
	cases := []struct {
		name     string
		disk     *Config
		baseline *Config
		updated  *Config
		want     *Config
	}{
		{
			name:     "no changes keeps concurrent disk state",
			disk:     &Config{DefaultWorkspace: "ws_other"},
			baseline: &Config{DefaultWorkspace: "ws_a"},
			updated:  &Config{DefaultWorkspace: "ws_a"},
			want:     &Config{DefaultWorkspace: "ws_other"},
		},
		{
			name:     "changed scalar applies over concurrent value",
			disk:     &Config{DefaultWorkspace: "ws_other"},
			baseline: &Config{DefaultWorkspace: "ws_a"},
			updated:  &Config{DefaultWorkspace: "ws_b"},
			want:     &Config{DefaultWorkspace: "ws_b"},
		},
		{
			name:     "cleared scalar applies",
			disk:     &Config{BaseURL: "https://example.com"},
			baseline: &Config{BaseURL: "https://example.com"},
			updated:  &Config{},
			want:     &Config{},
		},
		{
			name:     "auth replace wins over concurrent write",
			disk:     &Config{Auth: &Auth{Method: "token", AccessToken: "cpt_disk"}},
			baseline: &Config{Auth: &Auth{Method: "token", AccessToken: "cpt_old"}},
			updated:  &Config{Auth: &Auth{Method: "token", AccessToken: "cpt_new"}},
			want:     &Config{Auth: &Auth{Method: "token", AccessToken: "cpt_new"}},
		},
		{
			name:     "auth clear applies",
			disk:     &Config{Auth: &Auth{Method: "oauth", AccessToken: "cpt_disk"}, DefaultWorkspace: "ws_a"},
			baseline: &Config{Auth: &Auth{Method: "oauth", AccessToken: "cpt_disk"}, DefaultWorkspace: "ws_a"},
			updated:  &Config{DefaultWorkspace: "ws_a"},
			want:     &Config{DefaultWorkspace: "ws_a"},
		},
		{
			name:     "unchanged auth keeps concurrent credential",
			disk:     &Config{Auth: &Auth{Method: "token", AccessToken: "cpt_disk"}},
			baseline: &Config{Auth: &Auth{Method: "token", AccessToken: "cpt_old"}},
			updated:  &Config{Auth: &Auth{Method: "token", AccessToken: "cpt_old"}},
			want:     &Config{Auth: &Auth{Method: "token", AccessToken: "cpt_disk"}},
		},
		{
			name:     "new workspace prefs apply, concurrent key preserved",
			disk:     &Config{Workspaces: map[string]*WorkspacePrefs{"ws_concurrent": {DefaultApp: "app_c"}}},
			baseline: &Config{},
			updated:  &Config{Workspaces: map[string]*WorkspacePrefs{"ws_new": {DefaultApp: "app_n"}}},
			want: &Config{Workspaces: map[string]*WorkspacePrefs{
				"ws_new":        {DefaultApp: "app_n"},
				"ws_concurrent": {DefaultApp: "app_c"},
			}},
		},
		{
			name:     "changed prefs value applies",
			disk:     &Config{Workspaces: map[string]*WorkspacePrefs{"ws_a": {DefaultApp: "app_disk"}}},
			baseline: &Config{Workspaces: map[string]*WorkspacePrefs{"ws_a": {DefaultApp: "app_old"}}},
			updated:  &Config{Workspaces: map[string]*WorkspacePrefs{"ws_a": {DefaultApp: "app_new"}}},
			want:     &Config{Workspaces: map[string]*WorkspacePrefs{"ws_a": {DefaultApp: "app_new"}}},
		},
		{
			name:     "dropped workspace id is deleted",
			disk:     &Config{Workspaces: map[string]*WorkspacePrefs{"ws_a": {DefaultApp: "app_old"}, "ws_ghost": {DefaultApp: "app_g"}}},
			baseline: &Config{Workspaces: map[string]*WorkspacePrefs{"ws_a": {DefaultApp: "app_old"}, "ws_ghost": {DefaultApp: "app_g"}}},
			updated:  &Config{Workspaces: map[string]*WorkspacePrefs{"ws_a": {DefaultApp: "app_old"}}},
			want:     &Config{Workspaces: map[string]*WorkspacePrefs{"ws_a": {DefaultApp: "app_old"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.disk
			got.ApplyChanges(tc.baseline, tc.updated)
			if got.DefaultWorkspace != tc.want.DefaultWorkspace {
				t.Errorf("DefaultWorkspace = %q, want %q", got.DefaultWorkspace, tc.want.DefaultWorkspace)
			}
			if got.BaseURL != tc.want.BaseURL {
				t.Errorf("BaseURL = %q, want %q", got.BaseURL, tc.want.BaseURL)
			}
			if !authEqual(got.Auth, tc.want.Auth) {
				t.Errorf("Auth = %+v, want %+v", got.Auth, tc.want.Auth)
			}
			if len(got.Workspaces) != len(tc.want.Workspaces) {
				t.Fatalf("Workspaces = %+v, want %+v", got.Workspaces, tc.want.Workspaces)
			}
			for id, prefs := range tc.want.Workspaces {
				if !prefsEqual(got.Workspaces[id], prefs) {
					t.Errorf("Workspaces[%s] = %+v, want %+v", id, got.Workspaces[id], prefs)
				}
			}
		})
	}
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	seed := baseSeedConfig()
	snap := seed.Snapshot()

	seed.WorkspacePrefsFor("ws_a").DefaultApp = "app_mutated"
	seed.Auth.AccessToken = "cpt_mutated"
	seed.Workspaces["ws_new"] = &WorkspacePrefs{DefaultApp: "app_new"}

	if snap.Workspaces["ws_a"].DefaultApp != "app_old" {
		t.Errorf("snapshot prefs followed the original mutation: %+v", snap.Workspaces["ws_a"])
	}
	if snap.Auth.AccessToken != "cpt_old" {
		t.Errorf("snapshot auth followed the original mutation: %+v", snap.Auth)
	}
	if _, ok := snap.Workspaces["ws_new"]; ok {
		t.Errorf("snapshot map shares storage with the original: %+v", snap.Workspaces)
	}

	// And the reverse direction: mutating the snapshot must not touch the
	// original either.
	snap2 := baseSeedConfig()
	snapCopy := snap2.Snapshot()
	snapCopy.WorkspacePrefsFor("ws_a").DefaultApp = "app_via_snapshot"
	if snap2.Workspaces["ws_a"].DefaultApp != "app_old" {
		t.Errorf("original followed the snapshot mutation: %+v", snap2.Workspaces["ws_a"])
	}
}
