package config

import (
	"os"
	"path/filepath"
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
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config file perms = %o, want 600", perm)
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
