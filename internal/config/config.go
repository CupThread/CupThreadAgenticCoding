// Package config loads and stores the cupthread CLI configuration.
//
// The config lives at ~/.config/cupthread/config.json (overridable via
// CUPTHREAD_CONFIG or XDG_CONFIG_HOME) and holds credentials plus the
// default workspace/app context. The file is created with 0600 permissions
// because it contains access tokens.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultBaseURL is the production CupThread API.
const DefaultBaseURL = "https://api.cupthread.com"

// Auth describes how the CLI authenticates against the API.
type Auth struct {
	// Method is "token" (personal access token) or "oauth".
	Method       string `json:"method"`
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	// ExpiresAt is when AccessToken expires (RFC 3339). Empty for PATs,
	// which do not expire client-side.
	ExpiresAt   string `json:"expiresAt,omitempty"`
	TokenPrefix string `json:"tokenPrefix,omitempty"`
	// ClientID identifies the OAuth application that issued the tokens.
	ClientID string `json:"clientId,omitempty"`
}

// WorkspacePrefs carries per-workspace CLI defaults.
type WorkspacePrefs struct {
	DefaultApp string `json:"defaultApp,omitempty"`
}

// Config is the on-disk CLI state.
type Config struct {
	DefaultWorkspace string                     `json:"defaultWorkspace,omitempty"`
	BaseURL          string                     `json:"baseUrl,omitempty"`
	Workspaces       map[string]*WorkspacePrefs `json:"workspaces,omitempty"`
	Auth             *Auth                      `json:"auth,omitempty"`
}

// Path returns the config file location. CUPTHREAD_CONFIG wins, then
// XDG_CONFIG_HOME, then ~/.config.
func Path() (string, error) {
	if p := os.Getenv("CUPTHREAD_CONFIG"); p != "" {
		return p, nil
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "cupthread", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "cupthread", "config.json"), nil
}

// Load reads the config at path. A missing file yields an empty config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes the config atomically with restrictive permissions.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// WorkspacePrefsFor returns (creating if needed) the prefs for a workspace.
func (c *Config) WorkspacePrefsFor(workspaceID string) *WorkspacePrefs {
	if c.Workspaces == nil {
		c.Workspaces = map[string]*WorkspacePrefs{}
	}
	prefs, ok := c.Workspaces[workspaceID]
	if !ok {
		prefs = &WorkspacePrefs{}
		c.Workspaces[workspaceID] = prefs
	}
	return prefs
}

// EnvToken returns the token supplied via the environment, if any.
// It takes precedence over stored credentials so CI and agents can inject
// credentials without touching the config file.
func EnvToken() string {
	return os.Getenv("CUPTHREAD_TOKEN")
}
