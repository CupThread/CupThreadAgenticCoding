package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
)

// The tests in this file pin the issue #314 merge semantics of applyTokenSet:
// a token response overwrites only the fields it actually carries. A 200
// without refresh_token must keep the stored one (RFC 6749 §6) and a 200
// without expires_in must keep a still-future stored expiry — the
// transparent-refresh guard in buildClient keys on both fields, and blanking
// either silently and permanently disables the self-heal.

// noRotateTokenSetBody is a conforming-enough token response that carries no
// refresh_token and no expires_in — the degraded shape a nonconforming
// --base-url target can answer with.
const noRotateTokenSetBody = `{"access_token":"cpt_norotate_access","token_type":"bearer","scope":"full"}`

// TestApplyTokenSetPreservesCarriedFieldsOnly pins the merge semantics at the
// function level, for both callers (login at auth.go, transparent refresh at
// root.go): rotation still overwrites, omission keeps the stored field, and
// the stderr warning fires exactly when nothing still-future is stored.
func TestApplyTokenSetPreservesCarriedFieldsOnly(t *testing.T) {
	futureExpiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	pastExpiry := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)

	t.Run("keeps stored refresh token when response omits it", func(t *testing.T) {
		a := &app{out: output.New(io.Discard, output.FormatTable), cfg: &config.Config{Auth: &config.Auth{
			Method: "oauth", AccessToken: "cpt_old", RefreshToken: "cpr_keep", ExpiresAt: futureExpiry,
		}}}
		stderr := captureStderr(t, func() {
			a.applyTokenSet(&auth.TokenSet{AccessToken: "cpt_new"})
		})
		if a.cfg.Auth.RefreshToken != "cpr_keep" {
			t.Errorf("RefreshToken = %q, want the stored credential kept byte-identical", a.cfg.Auth.RefreshToken)
		}
		if a.cfg.Auth.ExpiresAt != futureExpiry {
			t.Errorf("ExpiresAt = %q, want the stored still-future expiry kept byte-identical", a.cfg.Auth.ExpiresAt)
		}
		if stderr != "" {
			t.Errorf("stderr = %q, want no warning while a still-future expiry is stored", stderr)
		}
	})

	t.Run("still rotates when the response carries a refresh token", func(t *testing.T) {
		a := &app{out: output.New(io.Discard, output.FormatTable), cfg: &config.Config{Auth: &config.Auth{
			Method: "oauth", AccessToken: "cpt_old", RefreshToken: "cpr_old", ExpiresAt: pastExpiry,
		}}}
		stderr := captureStderr(t, func() {
			a.applyTokenSet(&auth.TokenSet{AccessToken: "cpt_new", RefreshToken: "cpr_new", ExpiresIn: 3600})
		})
		if a.cfg.Auth.RefreshToken != "cpr_new" {
			t.Errorf("RefreshToken = %q, want the rotation applied", a.cfg.Auth.RefreshToken)
		}
		expiry, err := time.Parse(time.RFC3339, a.cfg.Auth.ExpiresAt)
		if err != nil || time.Until(expiry) < 59*time.Minute {
			t.Errorf("ExpiresAt = %q, want a freshly computed ~1h expiry (%v)", a.cfg.Auth.ExpiresAt, err)
		}
		if stderr != "" {
			t.Errorf("stderr = %q, want no warning when expires_in is advertised", stderr)
		}
	})

	t.Run("keeps a still-future stored expiry when expires_in is omitted", func(t *testing.T) {
		a := &app{out: output.New(io.Discard, output.FormatTable), cfg: &config.Config{Auth: &config.Auth{
			Method: "oauth", AccessToken: "cpt_old", RefreshToken: "cpr_keep", ExpiresAt: futureExpiry,
		}}}
		stderr := captureStderr(t, func() {
			a.applyTokenSet(&auth.TokenSet{AccessToken: "cpt_new", RefreshToken: "cpr_rotated"})
		})
		if a.cfg.Auth.ExpiresAt != futureExpiry {
			t.Errorf("ExpiresAt = %q, want the stored still-future expiry kept byte-identical", a.cfg.Auth.ExpiresAt)
		}
		if stderr != "" {
			t.Errorf("stderr = %q, want no warning while a still-future expiry is stored", stderr)
		}
	})

	t.Run("warns when nothing still-future is stored", func(t *testing.T) {
		for name, stored := range map[string]string{"stale expiry": pastExpiry, "blank expiry": ""} {
			t.Run(name, func(t *testing.T) {
				a := &app{out: output.New(io.Discard, output.FormatTable), cfg: &config.Config{Auth: &config.Auth{
					Method: "oauth", AccessToken: "cpt_old", RefreshToken: "cpr_keep", ExpiresAt: stored,
				}}}
				stderr := captureStderr(t, func() {
					a.applyTokenSet(&auth.TokenSet{AccessToken: "cpt_new"})
				})
				if a.cfg.Auth.ExpiresAt != stored {
					t.Errorf("ExpiresAt = %q, want it left as stored (%q)", a.cfg.Auth.ExpiresAt, stored)
				}
				if n := strings.Count(stderr, "warning:"); n != 1 {
					t.Errorf("stderr has %d warning lines, want exactly 1:\n%s", n, stderr)
				}
				if !strings.Contains(stderr, "omitted expires_in") {
					t.Errorf("stderr does not name the omitted expires_in:\n%s", stderr)
				}
			})
		}
	})
}

// TestRefreshKeepsCredentialWhenResponseOmitsFields is the issue #314
// regression: an expired credential transparently refreshed against a token
// endpoint that answers access_token only must keep the stored refresh token
// and expiry, so the second invocation still reaches the token endpoint
// instead of silently replaying a dead access token forever.
func TestRefreshKeepsCredentialWhenResponseOmitsFields(t *testing.T) {
	var tokenPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			tokenPosts.Add(1)
			_, _ = w.Write([]byte(noRotateTokenSetBody))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withRaceTestEnv(t, server.URL)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-time.Hour))

	// First invocation: the expired access token triggers the proactive
	// refresh; the degraded response must not wipe the stored credential.
	a := raceTestApp(t, cfgPath, server.URL)
	var token string
	var tokErr error
	stderr := captureStderr(t, func() {
		token, tokErr = a.client.Token(context.Background())
	})
	if tokErr != nil {
		t.Fatalf("token provider: %v", tokErr)
	}
	if token != "cpt_norotate_access" {
		t.Errorf("token = %q, want the minted access token", token)
	}
	if got := tokenPosts.Load(); got != 1 {
		t.Fatalf("token endpoint POSTs after first invocation = %d, want 1", got)
	}
	for _, state := range []struct {
		name string
		auth *config.Auth
	}{
		{"in-memory", a.cfg.Auth},
		{"on-disk", readDiskAuth(t, cfgPath)},
	} {
		if state.auth == nil {
			t.Fatalf("%s auth is nil", state.name)
		}
		if state.auth.RefreshToken != staleRefresh {
			t.Errorf("%s RefreshToken = %q, want %q kept byte-identical", state.name, state.auth.RefreshToken, staleRefresh)
		}
	}
	if n := strings.Count(stderr, "warning:"); n != 1 {
		t.Errorf("stderr has %d warning lines, want exactly 1 (the stored expiry is stale):\n%s", n, stderr)
	}

	// Second invocation: a fresh app re-loads the persisted config. The
	// refresh credential survived, so the guard must reach the token endpoint
	// again — the pre-#314 behavior blanked it and every later invocation
	// silently sent the dead access token with zero POSTs.
	a2 := raceTestApp(t, cfgPath, server.URL)
	var tokErr2 error
	stderr = captureStderr(t, func() {
		_, tokErr2 = a2.client.Token(context.Background())
	})
	if tokErr2 != nil {
		t.Fatalf("token provider (second invocation): %v", tokErr2)
	}
	if got := tokenPosts.Load(); got != 2 {
		t.Errorf("token endpoint POSTs after second invocation = %d, want 2 (the refresh guard must stay reachable)", got)
	}
	if disk := readDiskAuth(t, cfgPath); disk == nil || disk.RefreshToken != staleRefresh {
		t.Errorf("on-disk RefreshToken after second invocation = %+v, want %q kept byte-identical", disk, staleRefresh)
	}
	if n := strings.Count(stderr, "warning:"); n != 1 {
		t.Errorf("second invocation stderr has %d warning lines, want exactly 1:\n%s", n, stderr)
	}
}

// TestRefreshPreservesFutureExpiryQuietly covers the narrow-window shape: the
// guard also refreshes when the stored expiry is still future but inside the
// one-minute lead time, and there a response without expires_in must keep
// that still-future expiry without any warning.
func TestRefreshPreservesFutureExpiryQuietly(t *testing.T) {
	var tokenPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			tokenPosts.Add(1)
			_, _ = w.Write([]byte(noRotateTokenSetBody))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withRaceTestEnv(t, server.URL)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	soon := time.Now().Add(30 * time.Second)
	seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, soon)
	wantExpiry := soon.UTC().Format(time.RFC3339)

	a := raceTestApp(t, cfgPath, server.URL)
	var tokErr error
	stderr := captureStderr(t, func() {
		_, tokErr = a.client.Token(context.Background())
	})
	if tokErr != nil {
		t.Fatalf("token provider: %v", tokErr)
	}
	if tokenPosts.Load() != 1 {
		t.Fatalf("token endpoint POSTs = %d, want 1", tokenPosts.Load())
	}
	if a.cfg.Auth.ExpiresAt != wantExpiry {
		t.Errorf("in-memory ExpiresAt = %q, want %q kept byte-identical", a.cfg.Auth.ExpiresAt, wantExpiry)
	}
	if disk := readDiskAuth(t, cfgPath); disk == nil || disk.ExpiresAt != wantExpiry || disk.RefreshToken != staleRefresh {
		t.Errorf("on-disk auth = %+v, want ExpiresAt %q and RefreshToken %q kept byte-identical", disk, wantExpiry, staleRefresh)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want no warning while a still-future expiry is stored", stderr)
	}
}

// TestRefreshOmittedExpiresInWarnsOnlyInHumanMode pins the warning's mode
// split: the machine-readable stream must stay parse-clean, so --json gets no
// stderr warning where table mode gets exactly one.
func TestRefreshOmittedExpiresInWarnsOnlyInHumanMode(t *testing.T) {
	for _, mode := range []string{"table", "json"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/oauth/token" {
					_, _ = w.Write([]byte(noRotateTokenSetBody))
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			withRaceTestEnv(t, server.URL)

			cfgPath := filepath.Join(t.TempDir(), "config.json")
			seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-time.Hour))
			a := raceTestApp(t, cfgPath, server.URL)
			if mode == "json" {
				a.out = output.New(io.Discard, output.FormatJSON)
			}
			var tokErr error
			stderr := captureStderr(t, func() {
				_, tokErr = a.client.Token(context.Background())
			})
			if tokErr != nil {
				t.Fatalf("token provider: %v", tokErr)
			}
			want := 1
			if mode == "json" {
				want = 0
			}
			if n := strings.Count(stderr, "warning:"); n != want {
				t.Errorf("stderr has %d warning lines in %s mode, want %d:\n%s", n, mode, want, stderr)
			}
		})
	}
}

// TestLoginKeepsStoredCredentialWhenResponseOmitsFields pins the login
// caller of applyTokenSet (finishOAuthLogin): a token response without
// refresh_token/expires_in must not destroy a still-valid stored credential,
// and a degraded login over no stored credential warns in human mode.
func TestLoginKeepsStoredCredentialWhenResponseOmitsFields(t *testing.T) {
	server := oauthProbeServer(t, http.StatusOK, 0)
	defer server.Close()

	t.Run("keeps the stored refresh credential over a previous login", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		soon := time.Now().Add(time.Hour)
		seedOAuthConfig(t, cfgPath, server.URL, "cpt_old_access", "cpr_old_refresh", soon)
		wantExpiry := soon.UTC().Format(time.RFC3339)
		stopStdout := captureStdStream(t, &os.Stdout)
		stopStderr := captureStdStream(t, &os.Stderr)
		installOAuthApp(t, cfgPath, server.URL)

		if err := finishOAuthLogin(context.Background(), &auth.TokenSet{AccessToken: "cpt_login_access"}, loginResult{Method: "oauth"}); err != nil {
			t.Fatalf("finishOAuthLogin: %v", err)
		}
		stopStdout()
		stderr := stopStderr()

		disk := readConfigFile(t, cfgPath).Auth
		if disk == nil {
			t.Fatal("on-disk auth is nil after login")
		}
		if disk.RefreshToken != "cpr_old_refresh" {
			t.Errorf("on-disk RefreshToken = %q, want the stored credential kept byte-identical", disk.RefreshToken)
		}
		if disk.ExpiresAt != wantExpiry {
			t.Errorf("on-disk ExpiresAt = %q, want %q kept byte-identical", disk.ExpiresAt, wantExpiry)
		}
		if disk.AccessToken != "cpt_login_access" {
			t.Errorf("on-disk AccessToken = %q, want the freshly issued one", disk.AccessToken)
		}
		if stderr != "" {
			t.Errorf("stderr = %q, want no warning while a still-future expiry is stored", stderr)
		}
	})

	t.Run("warns when logging in with no stored credential", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		if err := (&config.Config{}).Save(cfgPath); err != nil {
			t.Fatalf("seed config: %v", err)
		}
		stopStdout := captureStdStream(t, &os.Stdout)
		stopStderr := captureStdStream(t, &os.Stderr)
		installOAuthApp(t, cfgPath, server.URL)

		if err := finishOAuthLogin(context.Background(), &auth.TokenSet{AccessToken: "cpt_login_access"}, loginResult{Method: "oauth"}); err != nil {
			t.Fatalf("finishOAuthLogin: %v", err)
		}
		stopStdout()
		stderr := stopStderr()

		if n := strings.Count(stderr, "warning:"); n != 1 {
			t.Errorf("stderr has %d warning lines, want exactly 1:\n%s", n, stderr)
		}
		if !strings.Contains(stderr, "omitted expires_in") {
			t.Errorf("stderr does not name the omitted expires_in:\n%s", stderr)
		}
	})
}
