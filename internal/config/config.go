// Package config loads and stores the cupthread CLI configuration.
//
// The config lives at ~/.config/cupthread/config.json (overridable via
// CUPTHREAD_CONFIG or XDG_CONFIG_HOME) and holds credentials plus the
// default workspace/app context. The file is created with 0600 permissions
// because it contains access tokens; UnsafePermissions and RepairPermissions
// let the CLI re-check that hygiene at load time and repair a mode that was
// loosened after the fact (issue #186).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
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
	// IssuedBaseURL is the API origin whose OAuth endpoints issued this
	// credential and alone can refresh or revoke it. It is pinned at login
	// and deliberately not re-stamped by later invocations, so a --base-url /
	// $CUPTHREAD_BASE_URL override can never divert the refresh token to
	// another host (issue #191). Empty means the production default, so
	// configs written before the field existed keep working unchanged.
	IssuedBaseURL string `json:"issuedBaseUrl,omitempty"`
}

// IssuerBaseURL resolves the API origin that issued this credential and must
// serve the credential protocol (token refresh, revocation): the stored
// IssuedBaseURL wins, the remembered config-level base URL is the fallback
// for credentials written before the field existed, and empty everywhere
// means the production default — exactly the resolution chain pre-field
// configs got without an override. authState may be nil.
func (authState *Auth) IssuerBaseURL(rememberedBaseURL string) string {
	if authState != nil && authState.IssuedBaseURL != "" {
		return strings.TrimRight(authState.IssuedBaseURL, "/")
	}
	if rememberedBaseURL != "" {
		return strings.TrimRight(rememberedBaseURL, "/")
	}
	return DefaultBaseURL
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

// StoresCredentials reports whether c holds an auth section with actual
// tokens (access or refresh). An auth section without either — the shape a
// hand-edited or partially cleared file can leave behind — counts as
// credential-free, so a loosened file mode is not worth warning about when
// there is nothing on disk to expose (issue #186).
func (c *Config) StoresCredentials() bool {
	if c == nil || c.Auth == nil {
		return false
	}
	return c.Auth.AccessToken != "" || c.Auth.RefreshToken != ""
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

// Save writes the config atomically with restrictive permissions: the
// document goes to a unique temporary file in the destination directory
// (created 0600 — the file holds access tokens), is synced to disk, and is
// renamed over the destination, so a reader always observes either the old
// or the new complete document and concurrent saves neither interleave nor
// fail on a shared temporary name (issue #139). Save is atomic but not
// serialized across processes: callers that mutate shared config state must
// go through Update (or hold LockConfig themselves) so independent changes
// cannot be lost.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has moved the file away
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// Snapshot returns a deep copy of c, for use as the baseline a later
// ApplyChanges diffs against. The copy is required because config mutations
// happen in place (map entries, the Auth pointer), which would otherwise
// drag the baseline along with the change it is meant to remember.
func (c *Config) Snapshot() *Config {
	if c == nil {
		return nil
	}
	snap := *c
	if c.Auth != nil {
		authCopy := *c.Auth
		snap.Auth = &authCopy
	}
	if c.Workspaces != nil {
		snap.Workspaces = make(map[string]*WorkspacePrefs, len(c.Workspaces))
		for id, prefs := range c.Workspaces {
			if prefs == nil {
				snap.Workspaces[id] = nil
				continue
			}
			prefsCopy := *prefs
			snap.Workspaces[id] = &prefsCopy
		}
	}
	return &snap
}

// ApplyChanges applies to c exactly the field-level changes that separate
// baseline from updated: a field on which updated differs from baseline is
// copied onto c, and fields they agree on keep whatever value c already
// holds — so a change another process persisted after baseline was taken
// survives instead of being clobbered by a stale whole-file snapshot.
// Per-workspace prefs are merged per workspace id, and ids present in
// baseline but dropped from updated are deleted from c. c, baseline and
// updated must be distinct objects (Snapshot produces suitable copies).
func (c *Config) ApplyChanges(baseline, updated *Config) {
	if c == nil || baseline == nil || updated == nil {
		return
	}
	if updated.DefaultWorkspace != baseline.DefaultWorkspace {
		c.DefaultWorkspace = updated.DefaultWorkspace
	}
	if updated.BaseURL != baseline.BaseURL {
		c.BaseURL = updated.BaseURL
	}
	if !authEqual(updated.Auth, baseline.Auth) {
		c.Auth = updated.Auth
	}
	for id, prefs := range updated.Workspaces {
		if prefsEqual(prefs, baseline.Workspaces[id]) {
			continue
		}
		if prefs == nil {
			delete(c.Workspaces, id)
			continue
		}
		c.WorkspacePrefsFor(id).DefaultApp = prefs.DefaultApp
	}
	for id := range baseline.Workspaces {
		if _, ok := updated.Workspaces[id]; !ok {
			delete(c.Workspaces, id)
		}
	}
	if len(c.Workspaces) == 0 {
		c.Workspaces = nil
	}
}

// Update is the serialized config-mutation path: it takes the advisory
// cross-process lock on <path>.lock, re-reads the on-disk config, applies
// to that fresh snapshot exactly the field-level changes between baseline
// and updated, saves atomically, and returns the merged config. Deriving
// the write from a baseline taken at load time — instead of persisting a
// possibly stale in-memory copy wholesale — means a change another CLI
// invocation commits while this one runs (a 'workspaces use' racing an
// 'apps use', or either racing a credential write) survives the last rename
// instead of being silently discarded (issue #139).
func Update(path string, baseline, updated *Config) (*Config, error) {
	lock, err := LockConfig(path)
	if err != nil {
		return nil, fmt.Errorf("lock config for update: %w", err)
	}
	defer func() { _ = lock.Close() }()
	disk, err := Load(path)
	if err != nil {
		return nil, fmt.Errorf("re-read config under lock: %w", err)
	}
	disk.ApplyChanges(baseline, updated)
	if err := disk.Save(path); err != nil {
		return nil, err
	}
	return disk, nil
}

// authEqual compares two Auth values by content; nil means absent.
func authEqual(a, b *Auth) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// prefsEqual compares two WorkspacePrefs values by content; nil means absent.
func prefsEqual(a, b *WorkspacePrefs) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
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

// EnvToken returns the token supplied via the environment, if any, trimmed
// of surrounding whitespace. It takes precedence over stored credentials so
// CI and agents can inject credentials without touching the config file.
// A value that is only whitespace counts as unset, so a stray trailing
// newline never shadows a valid stored credential.
func EnvToken() string {
	return strings.TrimSpace(os.Getenv("CUPTHREAD_TOKEN"))
}

// ValidateToken rejects credential values that cannot form a valid
// Authorization header: after surrounding-whitespace trimming, any embedded
// space, tab, line break, or other control character is either rejected by
// net/http at the transport layer ("invalid header field value") or trimmed
// inconsistently on its way to the server. Call sites wrap the error with
// the credential's source ($CUPTHREAD_TOKEN or --token). Tokens are opaque,
// so no format beyond "no whitespace/control characters" is enforced here.
func ValidateToken(token string) error {
	for _, r := range token {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("contains whitespace or control characters — use the raw cpt_… value with no quotes, line breaks, or padding")
		}
	}
	return nil
}
