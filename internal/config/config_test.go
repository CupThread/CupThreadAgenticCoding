package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if cfg.DefaultWorkspace != "" || cfg.Auth != nil {
		t.Fatalf("expected empty config, got %+v", cfg)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	original := &Config{
		DefaultWorkspace: "ws_123",
		BaseURL:          "http://127.0.0.1:8787",
		Workspaces: map[string]*WorkspacePrefs{
			"ws_123": {DefaultApp: "app_abc"},
		},
		Auth: &Auth{Method: "token", AccessToken: "cpt_secret", TokenPrefix: "cpt_secret"},
	}
	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" {
		// Windows maps os.CreateTemp's 0600 mode to the read-only bit, so
		// Perm() reports 0666 there; the restrictive-perms guarantee is
		// unix-only.
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("config file perms = %o, want 600", perm)
		}
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.DefaultWorkspace != "ws_123" {
		t.Errorf("DefaultWorkspace = %q, want ws_123", loaded.DefaultWorkspace)
	}
	if loaded.Auth == nil || loaded.Auth.AccessToken != "cpt_secret" {
		t.Errorf("Auth not round-tripped: %+v", loaded.Auth)
	}
	if loaded.Workspaces["ws_123"].DefaultApp != "app_abc" {
		t.Errorf("WorkspacePrefs not round-tripped: %+v", loaded.Workspaces)
	}
}

func TestWorkspacePrefsForCreatesEntry(t *testing.T) {
	cfg := &Config{}
	prefs := cfg.WorkspacePrefsFor("ws_x")
	if prefs == nil {
		t.Fatal("WorkspacePrefsFor returned nil")
	}
	prefs.DefaultApp = "app_y"
	if cfg.Workspaces["ws_x"].DefaultApp != "app_y" {
		t.Fatalf("map entry not stored: %+v", cfg.Workspaces)
	}
}

// TestLoadNormalizesNullWorkspacePrefs pins the fix for issue #309: a JSON
// null for a Workspaces entry decodes to a nil *WorkspacePrefs that cmd-layer
// readers dereference, so Load must restore the "key present ⇒ non-nil value"
// invariant at the single decode point, keep the surviving entries intact,
// and stop the next Save from re-emitting the null.
func TestLoadNormalizesNullWorkspacePrefs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := `{"defaultWorkspace":"ws_1",` +
		`"workspaces":{"ws_1":null,"ws_2":{"defaultApp":"app_9"}},` +
		`"auth":{"method":"token","accessToken":"cpt_x","tokenPrefix":"cpt_x"}}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Workspaces["ws_1"]; ok {
		t.Errorf("null entry survived Load: %+v", cfg.Workspaces)
	}
	if prefs := cfg.Workspaces["ws_2"]; prefs == nil || prefs.DefaultApp != "app_9" {
		t.Errorf("sibling entry damaged by normalization: %+v", cfg.Workspaces)
	}

	// The normalization is in-memory only, but it must stick: the next
	// persist (any auth login, workspaces use, token refresh) rewrites the
	// file from this object, and that rewrite must not carry the null.
	// (defaultWorkspace may legitimately still name ws_1; only the
	// workspaces map is checked here.)
	saved := filepath.Join(t.TempDir(), "saved.json")
	if err := cfg.Save(saved); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(saved)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	var onDisk Config
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatalf("parse saved config: %v", err)
	}
	if _, ok := onDisk.Workspaces["ws_1"]; ok {
		t.Errorf("saved config re-emits the null entry:\n%s", data)
	}
	roundTripped, err := Load(saved)
	if err != nil {
		t.Fatalf("reload saved config: %v", err)
	}
	if _, ok := roundTripped.Workspaces["ws_1"]; ok {
		t.Errorf("null entry reappeared after save/reload: %+v", roundTripped.Workspaces)
	}
	if prefs := roundTripped.Workspaces["ws_2"]; prefs == nil || prefs.DefaultApp != "app_9" {
		t.Errorf("sibling entry not round-tripped: %+v", roundTripped.Workspaces)
	}
}

func TestPathEnvOverride(t *testing.T) {
	t.Setenv("CUPTHREAD_CONFIG", "/tmp/custom-config.json")
	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got != "/tmp/custom-config.json" {
		t.Errorf("Path = %q, want /tmp/custom-config.json", got)
	}
}

func TestEnvToken(t *testing.T) {
	if config := EnvToken(); config != "" {
		t.Fatalf("expected empty env token, got %q", config)
	}
	t.Setenv("CUPTHREAD_TOKEN", "cpt_env")
	if EnvToken() != "cpt_env" {
		t.Fatal("EnvToken did not read $CUPTHREAD_TOKEN")
	}
}

func TestEnvTokenTrimsWhitespace(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"clean", "cpt_env", "cpt_env"},
		{"trailing CR", "cpt_env\r", "cpt_env"},
		{"trailing LF", "cpt_env\n", "cpt_env"},
		{"trailing CRLF", "cpt_env\r\n", "cpt_env"},
		{"trailing spaces", "cpt_env  ", "cpt_env"},
		{"leading space", " cpt_env", "cpt_env"},
		{"surrounding tab", "\tcpt_env\t", "cpt_env"},
		{"only whitespace", "  \r\n\t ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CUPTHREAD_TOKEN", tc.raw)
			if got := EnvToken(); got != tc.want {
				t.Errorf("EnvToken() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateToken(t *testing.T) {
	for _, ok := range []string{"", "cpt_x", "cpt_0123456789abcdef-_.ABCDEFGHIJKLMNOPQRSTUVWXYZ"} {
		if err := ValidateToken(ok); err != nil {
			t.Errorf("ValidateToken(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"cpt_x\ty", "cpt_x y", "cpt_\rx", "cpt_x\n", "cpt_x\u00a0y", "cpt_\x00x"} {
		err := ValidateToken(bad)
		if err == nil {
			t.Errorf("ValidateToken(%q) = nil, want error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "whitespace") {
			t.Errorf("ValidateToken(%q) error %q does not mention whitespace", bad, err)
		}
	}
}

func TestStoresCredentials(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
		want bool
	}{
		{"nil config", nil, false},
		{"no auth section", &Config{DefaultWorkspace: "ws_1"}, false},
		{"empty auth section", &Config{Auth: &Auth{}}, false},
		{"access token only", &Config{Auth: &Auth{AccessToken: "cpt_x"}}, true},
		{"refresh token only", &Config{Auth: &Auth{RefreshToken: "rt_x"}}, true},
		{"full oauth pair", &Config{Auth: &Auth{AccessToken: "cpt_x", RefreshToken: "rt_x"}}, true},
	}
	for _, tc := range cases {
		if got := tc.cfg.StoresCredentials(); got != tc.want {
			t.Errorf("%s: StoresCredentials() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
