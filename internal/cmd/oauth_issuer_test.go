package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
)

// The tests in this file pin the issuer pinning of issue #191: the OAuth
// credential protocol (transparent token refresh, logout --revoke) always
// targets the server that issued the stored credential, never whatever
// --base-url/$CUPTHREAD_BASE_URL names — sending the long-lived rotating
// refresh token to any other host is account-takeover territory.

// runRootCaptureArgs executes the CLI capturing both output streams with
// exactly the args given — no implicit --base-url — so tests control the
// override themselves. Flag globals are reset so runs stay hermetic.
func runRootCaptureArgs(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	oldStdout := os.Stdout
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	os.Stdout = outW
	oldStderr := os.Stderr
	errR, errW, err := os.Pipe()
	if err != nil {
		os.Stdout = oldStdout
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stderr = errW

	flagBaseURL = ""
	flagWorkspace = ""
	flagApp = ""
	flagJSON = false
	flagOutput = ""

	root := newRootCmd()
	root.SetArgs(args)
	execErr := root.Execute()

	os.Stdout = oldStdout
	os.Stderr = oldStderr
	_ = outW.Close()
	_ = errW.Close()

	outBytes, err := io.ReadAll(outR)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	errBytes, err := io.ReadAll(errR)
	if err != nil {
		t.Fatalf("read stderr pipe: %v", err)
	}
	return string(outBytes), string(errBytes), execErr
}

// seedIssuedOAuthConfig writes an expired OAuth pair issued by issuerURL to
// cfgPath. rememberBaseURL optionally sets the config-level remembered
// endpoint separately from the credential's pinned issuer; empty keeps the
// remembered endpoint equal to the issuer (what a real login stores).
func seedIssuedOAuthConfig(t *testing.T, cfgPath, issuerURL, rememberBaseURL string) {
	t.Helper()
	if rememberBaseURL == "" {
		rememberBaseURL = issuerURL
	}
	pre := &config.Config{
		BaseURL: rememberBaseURL,
		Auth: &config.Auth{
			Method:        "oauth",
			AccessToken:   "cpt_issuer_access",
			RefreshToken:  "cpt_issuer_refresh",
			ExpiresAt:     time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
			ClientID:      auth.FirstPartyClientID,
			IssuedBaseURL: issuerURL,
		},
	}
	if err := pre.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

// oauthPath reports whether a request path is part of the OAuth credential
// protocol — the paths the override host must never receive.
func oauthPath(path string) bool {
	return strings.HasPrefix(path, "/api/v1/oauth/")
}

// TestRefreshPinnedToIssuingServer is the issue's core acceptance case: an
// expired OAuth pair issued by server A, a command run with --base-url B —
// the refresh POST must land on A, B must never see an /api/v1/oauth/* path
// while still serving the console API request itself, the divergence warning
// must name both servers on stderr, and the rotated pair must keep A as its
// issuer.
func TestRefreshPinnedToIssuingServer(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	freshPair := `{"access_token":"cpt_fresh_access","refresh_token":"cpt_fresh_refresh","token_type":"bearer","expires_in":3600,"scope":"console"}`

	for _, mode := range []string{"table", "json"} {
		t.Run(mode, func(t *testing.T) {
			var issuerOAuth, issuerOther []string
			issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if oauthPath(r.URL.Path) {
					issuerOAuth = append(issuerOAuth, r.Method+" "+r.URL.Path)
					if r.URL.Path == auth.TokenPath {
						if err := r.ParseForm(); err != nil {
							t.Errorf("parse refresh form: %v", err)
						}
						if got := r.Form.Get("refresh_token"); got != "cpt_issuer_refresh" {
							t.Errorf("issuer refresh_token = %q, want cpt_issuer_refresh", got)
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(freshPair))
						return
					}
					w.WriteHeader(http.StatusOK)
					return
				}
				issuerOther = append(issuerOther, r.URL.Path)
			}))
			defer issuer.Close()

			var overridePaths []string
			override := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				overridePaths = append(overridePaths, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(meFixture))
			}))
			defer override.Close()

			cfgPath := filepath.Join(t.TempDir(), "config.json")
			seedIssuedOAuthConfig(t, cfgPath, issuer.URL, "")

			args := []string{"me", "--base-url", override.URL, "--config", cfgPath}
			if mode == "json" {
				args = append(args, "--json")
			}
			stdout, stderr, err := runRootCaptureArgs(t, args...)
			if err != nil {
				t.Fatalf("me with override: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}

			// The issuer served the refresh; the override never saw an OAuth
			// path but did serve the console API call itself.
			if len(issuerOAuth) != 1 || issuerOAuth[0] != "POST "+auth.TokenPath {
				t.Errorf("issuer saw %v, want exactly [POST %s]", issuerOAuth, auth.TokenPath)
			}
			if len(issuerOther) != 0 {
				t.Errorf("issuer saw unexpected non-OAuth requests %v", issuerOther)
			}
			for _, p := range overridePaths {
				if oauthPath(p) {
					t.Errorf("override host received OAuth path %q; paths seen: %v", p, overridePaths)
				}
			}
			if len(overridePaths) != 1 || overridePaths[0] != "/api/v1/console/me" {
				t.Errorf("override paths = %v, want [/api/v1/console/me]", overridePaths)
			}

			// One divergence warning on stderr naming both servers, never on
			// the machine-readable stream.
			wantWarning := "API requests target " + override.URL + ", but the stored OAuth credential was issued by " + issuer.URL
			if !strings.Contains(stderr, wantWarning) {
				t.Errorf("stderr missing divergence warning %q:\n%s", wantWarning, stderr)
			}
			if strings.Contains(stdout, "warning:") {
				t.Errorf("stdout carries the divergence warning:\n%s", stdout)
			}

			// The rotated pair persisted with the issuer unchanged.
			disk, err := config.Load(cfgPath)
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if disk.Auth == nil || disk.Auth.RefreshToken != "cpt_fresh_refresh" {
				t.Errorf("on-disk auth = %+v, want the rotated pair", disk.Auth)
			}
			if disk.Auth != nil && disk.Auth.IssuedBaseURL != issuer.URL {
				t.Errorf("on-disk issuedBaseUrl = %q, want %q", disk.Auth.IssuedBaseURL, issuer.URL)
			}
		})
	}
}

// TestRefreshBackcompatWithoutIssuedBaseURL pins the fallback chain for
// configs written before the issuer was pinned: with no issuedBaseUrl, the
// refresh resolves from the remembered config base URL — so an override
// still cannot divert it — and from the production default when that is
// empty too, exactly like the pre-field behavior without an override.
func TestRefreshBackcompatWithoutIssuedBaseURL(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	var issuerTokenPosts int
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == auth.TokenPath {
			issuerTokenPosts++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"cpt_backcompat_access","refresh_token":"cpt_backcompat_refresh","token_type":"bearer","expires_in":3600,"scope":"console"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer issuer.Close()

	var overridePaths []string
	override := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		overridePaths = append(overridePaths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(meFixture))
	}))
	defer override.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	// Pre-field config shape: no issuedBaseUrl anywhere, the remembered
	// config base URL is the issuing server.
	seedIssuedOAuthConfig(t, cfgPath, "", issuer.URL)

	if _, stderr, err := runRootCaptureArgs(t, "me", "--base-url", override.URL, "--config", cfgPath); err != nil {
		t.Fatalf("me with override and pre-field config: %v\n%s", err, stderr)
	}
	if issuerTokenPosts != 1 {
		t.Errorf("issuer token POSTs = %d, want 1 (refresh must follow the remembered base URL)", issuerTokenPosts)
	}
	for _, p := range overridePaths {
		if oauthPath(p) {
			t.Errorf("override host received OAuth path %q; paths seen: %v", p, overridePaths)
		}
	}
}

// TestRefreshInvalidGrantArbitratedByIssuer pins that only the issuer's
// answer decides the credential's fate: the override host serves a
// would-succeed token endpoint, the issuer answers invalid_grant — the
// refresh must fail with the revoked-chain diagnosis anyway, proving the
// override never adjudicates (nor even sees) the refresh.
func TestRefreshInvalidGrantArbitratedByIssuer(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == auth.TokenPath {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Refresh token has been revoked"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer issuer.Close()

	var overrideTokenPosts int
	override := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if oauthPath(r.URL.Path) {
			overrideTokenPosts++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"cpt_override_access","refresh_token":"cpt_override_refresh","token_type":"bearer","expires_in":3600,"scope":"console"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(meFixture))
	}))
	defer override.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedIssuedOAuthConfig(t, cfgPath, issuer.URL, "")

	_, stderr, err := runRootCaptureArgs(t, "me", "--base-url", override.URL, "--config", cfgPath)
	if err == nil {
		t.Fatal("expected the refresh to fail when the issuer answers invalid_grant")
	}
	if !strings.Contains(err.Error(), "revoked server-side") {
		t.Errorf("error = %v, want the revoked-chain diagnosis reserved for the issuer's invalid_grant", err)
	}
	if overrideTokenPosts != 0 {
		t.Errorf("override token POSTs = %d, want 0 (the override must never arbitrate the refresh)", overrideTokenPosts)
	}
	if !strings.Contains(stderr, "issued by "+issuer.URL) {
		t.Errorf("stderr missing divergence warning naming the issuer:\n%s", stderr)
	}
}

// TestOAuthIssuerDivergenceWarning gates the stderr warning: it fires once
// per invocation only when an explicit --base-url/$CUPTHREAD_BASE_URL
// override disagrees with the stored OAuth credential's issuer, and stays
// quiet when the credential is inactive (env token), not an OAuth login, or
// when no override/identity override is present.
func TestOAuthIssuerDivergenceWarning(t *testing.T) {
	issuerBase := "https://issuer.cupthread.example"
	overrideBase := "https://override.cupthread.example"

	seed := func(t *testing.T, method string) string {
		t.Helper()
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		pre := &config.Config{
			BaseURL: issuerBase,
		}
		if method == "oauth" {
			pre.Auth = &config.Auth{
				Method:        "oauth",
				AccessToken:   "cpt_warn_access",
				RefreshToken:  "cpt_warn_refresh",
				ExpiresAt:     time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
				ClientID:      auth.FirstPartyClientID,
				IssuedBaseURL: issuerBase,
			}
		} else {
			pre.Auth = &config.Auth{
				Method:      "token",
				AccessToken: "cpt_warn_pat",
				TokenPrefix: "cpt_warn_pat",
			}
		}
		if err := pre.Save(cfgPath); err != nil {
			t.Fatalf("seed config: %v", err)
		}
		return cfgPath
	}

	t.Run("override diverges", func(t *testing.T) {
		t.Setenv("CUPTHREAD_TOKEN", "")
		t.Setenv("CUPTHREAD_BASE_URL", "")
		cfgPath := seed(t, "oauth")
		_, stderr, err := runRootCaptureArgs(t, "auth", "status", "--base-url", overrideBase, "--config", cfgPath)
		if err != nil {
			t.Fatalf("auth status: %v", err)
		}
		if !strings.Contains(stderr, "API requests target "+overrideBase+", but the stored OAuth credential was issued by "+issuerBase) {
			t.Errorf("stderr missing divergence warning:\n%s", stderr)
		}
	})
	t.Run("env override diverges", func(t *testing.T) {
		t.Setenv("CUPTHREAD_TOKEN", "")
		t.Setenv("CUPTHREAD_BASE_URL", overrideBase)
		cfgPath := seed(t, "oauth")
		_, stderr, err := runRootCaptureArgs(t, "auth", "status", "--config", cfgPath)
		if err != nil {
			t.Fatalf("auth status: %v", err)
		}
		if !strings.Contains(stderr, "API requests target "+overrideBase) {
			t.Errorf("stderr missing divergence warning for the env override:\n%s", stderr)
		}
	})
	t.Run("no override", func(t *testing.T) {
		t.Setenv("CUPTHREAD_TOKEN", "")
		t.Setenv("CUPTHREAD_BASE_URL", "")
		cfgPath := seed(t, "oauth")
		_, stderr, err := runRootCaptureArgs(t, "auth", "status", "--config", cfgPath)
		if err != nil {
			t.Fatalf("auth status: %v", err)
		}
		if strings.Contains(stderr, "warning:") {
			t.Errorf("stderr carries a divergence warning without an override:\n%s", stderr)
		}
	})
	t.Run("override equals issuer", func(t *testing.T) {
		t.Setenv("CUPTHREAD_TOKEN", "")
		t.Setenv("CUPTHREAD_BASE_URL", "")
		cfgPath := seed(t, "oauth")
		_, stderr, err := runRootCaptureArgs(t, "auth", "status", "--base-url", issuerBase, "--config", cfgPath)
		if err != nil {
			t.Fatalf("auth status: %v", err)
		}
		if strings.Contains(stderr, "warning:") {
			t.Errorf("stderr carries a divergence warning for the issuing server itself:\n%s", stderr)
		}
	})
	t.Run("personal access token", func(t *testing.T) {
		t.Setenv("CUPTHREAD_TOKEN", "")
		t.Setenv("CUPTHREAD_BASE_URL", "")
		cfgPath := seed(t, "token")
		_, stderr, err := runRootCaptureArgs(t, "auth", "status", "--base-url", overrideBase, "--config", cfgPath)
		if err != nil {
			t.Fatalf("auth status: %v", err)
		}
		if strings.Contains(stderr, "warning:") {
			t.Errorf("stderr carries a divergence warning for a PAT credential:\n%s", stderr)
		}
	})
	t.Run("env token keeps stored credential inactive", func(t *testing.T) {
		t.Setenv("CUPTHREAD_TOKEN", "cpt_env_inactive")
		t.Setenv("CUPTHREAD_BASE_URL", "")
		cfgPath := seed(t, "oauth")
		_, stderr, err := runRootCaptureArgs(t, "auth", "status", "--base-url", overrideBase, "--config", cfgPath)
		if err != nil {
			t.Fatalf("auth status: %v", err)
		}
		if strings.Contains(stderr, "warning:") {
			t.Errorf("stderr carries a divergence warning while $CUPTHREAD_TOKEN is active:\n%s", stderr)
		}
	})
	t.Run("no stored credential", func(t *testing.T) {
		t.Setenv("CUPTHREAD_TOKEN", "")
		t.Setenv("CUPTHREAD_BASE_URL", "")
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		_, stderr, err := runRootCaptureArgs(t, "auth", "status", "--base-url", overrideBase, "--config", cfgPath)
		if err != nil {
			t.Fatalf("auth status: %v", err)
		}
		if strings.Contains(stderr, "warning:") {
			t.Errorf("stderr carries a divergence warning without a credential:\n%s", stderr)
		}
	})
}

// TestAuthStatusIssuedBaseURLFromCredential pins the truthful "Credential
// issued for" line: it derives from the credential's pinned issuer even when
// the remembered config base URL disagrees, falling back to the remembered
// value for credentials written before the field existed.
func TestAuthStatusIssuedBaseURLFromCredential(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	const issuer = "https://issuer.cupthread.example"
	const remembered = "http://127.0.0.1:1" // unroutable: nothing may mistake it for the issuer

	t.Run("pinned issuer wins", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		pre := &config.Config{
			BaseURL: remembered,
			Auth: &config.Auth{
				Method:        "oauth",
				AccessToken:   "cpt_status_access",
				ExpiresAt:     time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
				IssuedBaseURL: issuer,
			},
		}
		if err := pre.Save(cfgPath); err != nil {
			t.Fatalf("seed config: %v", err)
		}

		table, _, err := runRootCaptureArgs(t, "auth", "status", "--config", cfgPath)
		if err != nil {
			t.Fatalf("auth status: %v", err)
		}
		if !strings.Contains(table, "Credential issued for") || !strings.Contains(table, issuer) {
			t.Errorf("table output missing pinned issuer %q:\n%s", issuer, table)
		}
		if strings.Contains(table, "Credential issued for "+remembered) {
			t.Errorf("table output names the remembered endpoint as the issuer:\n%s", table)
		}

		stdout, _, err := runRootCaptureArgs(t, "auth", "status", "--json", "--config", cfgPath)
		if err != nil {
			t.Fatalf("auth status --json: %v", err)
		}
		doc := unmarshalOneJSON(t, stdout)
		if doc["issuedBaseUrl"] != issuer {
			t.Errorf("issuedBaseUrl = %v, want %q", doc["issuedBaseUrl"], issuer)
		}
	})
	t.Run("pre-field credential falls back to remembered endpoint", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		pre := &config.Config{
			BaseURL: remembered,
			Auth: &config.Auth{
				Method:      "oauth",
				AccessToken: "cpt_status_access",
			},
		}
		if err := pre.Save(cfgPath); err != nil {
			t.Fatalf("seed config: %v", err)
		}

		table, _, err := runRootCaptureArgs(t, "auth", "status", "--config", cfgPath)
		if err != nil {
			t.Fatalf("auth status: %v", err)
		}
		if !strings.Contains(table, "Credential issued for") || !strings.Contains(table, remembered) {
			t.Errorf("table output missing remembered-endpoint fallback:\n%s", table)
		}
	})
	t.Run("default issuer not shown", func(t *testing.T) {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		pre := &config.Config{
			Auth: &config.Auth{
				Method:      "oauth",
				AccessToken: "cpt_status_access",
			},
		}
		if err := pre.Save(cfgPath); err != nil {
			t.Fatalf("seed config: %v", err)
		}

		stdout, _, err := runRootCaptureArgs(t, "auth", "status", "--json", "--config", cfgPath)
		if err != nil {
			t.Fatalf("auth status --json: %v", err)
		}
		doc := unmarshalOneJSON(t, stdout)
		if _, ok := doc["issuedBaseUrl"]; ok {
			t.Errorf("issuedBaseUrl = %v, want the field omitted for a default-issued credential", doc["issuedBaseUrl"])
		}
	})
}
