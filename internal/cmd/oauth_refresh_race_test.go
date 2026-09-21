package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
)

// The tests in this file pin the cross-process safety of the transparent
// OAuth refresh (issue #62): the refresh token is single-use server-side and
// replaying a rotated one revokes the whole credential chain, so the token
// provider must hold a cross-process config lock, re-read the on-disk pair
// under it, and adopt a rotation another process already committed instead
// of replaying it.

const (
	staleAccess    = "cpt_stale_access"
	staleRefresh   = "cpt_stale_refresh"
	freshAccess    = "cpt_fresh_access"
	freshRefresh   = "cpt_fresh_refresh"
	tokenSetBody   = `{"access_token":"` + freshAccess + `","refresh_token":"` + freshRefresh + `","token_type":"bearer","expires_in":3600,"scope":"console"}`
	invalidGrantBd = `{"error":"invalid_grant","error_description":"Refresh token has been revoked"}`
)

// seedOAuthConfig writes an oauth config with the given pair to cfgPath.
func seedOAuthConfig(t *testing.T, cfgPath, access, refresh string, expires time.Time) {
	t.Helper()
	cfg := &config.Config{
		Auth: &config.Auth{
			Method:       "oauth",
			AccessToken:  access,
			RefreshToken: refresh,
			ExpiresAt:    expires.UTC().Format(time.RFC3339),
			ClientID:     auth.FirstPartyClientID,
		},
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

// raceTestApp builds an app whose token provider targets serverURL with the
// config loaded from cfgPath, mirroring what PersistentPreRunE assembles for
// a real invocation.
func raceTestApp(t *testing.T, cfgPath, serverURL string) *app {
	t.Helper()
	a := &app{out: output.New(io.Discard, output.FormatTable), cfgPath: cfgPath}
	var err error
	a.cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	a.client = a.buildClient()
	return a
}

// withRaceTestEnv points the base-URL flag at serverURL for the duration of
// the test and keeps the credential env vars from leaking in.
func withRaceTestEnv(t *testing.T, serverURL string) {
	t.Helper()
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")
	oldFlag := flagBaseURL
	flagBaseURL = serverURL
	t.Cleanup(func() { flagBaseURL = oldFlag })
}

// readDiskAuth loads the on-disk config and returns its auth section.
func readDiskAuth(t *testing.T, cfgPath string) *config.Auth {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config from disk: %v", err)
	}
	return cfg.Auth
}

// TestTokenProviderSkipsRefreshWhenDiskPairChanged loads a near-expiry pair,
// then rewrites the config file on disk with a fresh pair (simulating a
// rotation another process already persisted) and invokes the token
// provider: it must return the disk access token without POSTing to the
// token endpoint at all.
func TestTokenProviderSkipsRefreshWhenDiskPairChanged(t *testing.T) {
	var tokenPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			tokenPosts.Add(1)
			_, _ = w.Write([]byte(tokenSetBody))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withRaceTestEnv(t, server.URL)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedOAuthConfig(t, cfgPath, staleAccess, staleRefresh, time.Now().Add(-time.Minute))
	// Another process rotated and persisted the pair after our process
	// started: the disk now holds a newer pair than the one we loaded.
	seedOAuthConfig(t, cfgPath, "cpt_disk_access", "cpt_disk_refresh", time.Now().Add(time.Hour))

	a := raceTestApp(t, cfgPath, server.URL)
	token, err := a.client.Token(context.Background())
	if err != nil {
		t.Fatalf("token provider: %v", err)
	}
	if token != "cpt_disk_access" {
		t.Errorf("token = %q, want the disk access token cpt_disk_access", token)
	}
	if got := tokenPosts.Load(); got != 0 {
		t.Errorf("token endpoint POSTs = %d, want 0 (disk pair must be adopted, not replayed)", got)
	}
	if a.cfg.Auth == nil || a.cfg.Auth.RefreshToken != "cpt_disk_refresh" {
		t.Errorf("in-memory auth = %+v, want the adopted disk pair", a.cfg.Auth)
	}
}

// TestTokenProviderCrossProcessLockSingleRefresh drives two app instances
// sharing one config path into the refresh path concurrently: the config
// lock must serialize them so exactly one refresh POST is sent, with the
// winner rotating and the loser adopting the winner's persisted pair.
func TestTokenProviderCrossProcessLockSingleRefresh(t *testing.T) {
	var tokenPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			tokenPosts.Add(1)
			_, _ = w.Write([]byte(tokenSetBody))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withRaceTestEnv(t, server.URL)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedOAuthConfig(t, cfgPath, staleAccess, staleRefresh, time.Now().Add(-time.Minute))

	// Both "processes" load the same stale pair before either refreshes.
	var apps []*app
	for i := 0; i < 2; i++ {
		apps = append(apps, raceTestApp(t, cfgPath, server.URL))
	}

	start := make(chan struct{})
	type result struct {
		token string
		err   error
	}
	results := make(chan result, len(apps))
	var wg sync.WaitGroup
	for _, a := range apps {
		wg.Add(1)
		go func(a *app) {
			defer wg.Done()
			<-start
			token, err := a.client.Token(context.Background())
			results <- result{token: token, err: err}
		}(a)
	}
	close(start)
	wg.Wait()
	close(results)

	for range apps {
		res := <-results
		if res.err != nil {
			t.Fatalf("token provider: %v", res.err)
		}
		if res.token != freshAccess {
			t.Errorf("token = %q, want the rotated access token %q", res.token, freshAccess)
		}
	}
	if got := tokenPosts.Load(); got != 1 {
		t.Errorf("token endpoint POSTs = %d, want exactly 1", got)
	}
	disk := readDiskAuth(t, cfgPath)
	if disk == nil || disk.RefreshToken != freshRefresh {
		t.Errorf("on-disk auth = %+v, want the rotated pair persisted once", disk)
	}
}

// TestTokenProviderInvalidGrantAdoptsNewerDiskPair covers the interleaving
// where the winner persists its pair only after the loser's refresh POST is
// already in flight: the endpoint answers invalid_grant, but the provider
// must re-read the disk once, find the winner's still-valid pair, and return
// its access token without error.
func TestTokenProviderInvalidGrantAdoptsNewerDiskPair(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	var tokenPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			tokenPosts.Add(1)
			// The winning process persisted its fresh pair while our
			// refresh was in flight, then the server rejected our replay.
			seedOAuthConfig(t, cfgPath, "cpt_winner_access", "cpt_winner_refresh", time.Now().Add(time.Hour))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(invalidGrantBd))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withRaceTestEnv(t, server.URL)

	seedOAuthConfig(t, cfgPath, staleAccess, staleRefresh, time.Now().Add(-time.Minute))
	a := raceTestApp(t, cfgPath, server.URL)

	token, err := a.client.Token(context.Background())
	if err != nil {
		t.Fatalf("token provider: %v", err)
	}
	if token != "cpt_winner_access" {
		t.Errorf("token = %q, want the winner's access token cpt_winner_access", token)
	}
	if got := tokenPosts.Load(); got != 1 {
		t.Errorf("token endpoint POSTs = %d, want 1", got)
	}
	if a.cfg.Auth == nil || a.cfg.Auth.RefreshToken != "cpt_winner_refresh" {
		t.Errorf("in-memory auth = %+v, want the adopted winner pair", a.cfg.Auth)
	}
}

// TestTokenProviderInvalidGrantReportsRevokedChain pins the destructive
// interleaving: the refresh fails with invalid_grant and the disk is
// unchanged, so the credential chain really is revoked and the provider must
// say so explicitly instead of a bare re-login demand.
func TestTokenProviderInvalidGrantReportsRevokedChain(t *testing.T) {
	var tokenPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			tokenPosts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(invalidGrantBd))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withRaceTestEnv(t, server.URL)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedOAuthConfig(t, cfgPath, staleAccess, staleRefresh, time.Now().Add(-time.Minute))
	a := raceTestApp(t, cfgPath, server.URL)

	_, err := a.client.Token(context.Background())
	if err == nil {
		t.Fatal("expected an error when the credential chain is revoked and no newer disk pair exists")
	}
	if !strings.Contains(err.Error(), "revoked server-side") {
		t.Errorf("error = %v, want an explicit revoked-chain diagnosis", err)
	}
	if !strings.Contains(err.Error(), "run 'cupthread auth login' again") {
		t.Errorf("error = %v, want the re-login guidance", err)
	}
	if got := tokenPosts.Load(); got != 1 {
		t.Errorf("token endpoint POSTs = %d, want 1", got)
	}
}

// TestTokenProviderRefreshesOnceWhenDiskUnchanged is the regression case:
// with an expired in-memory pair and no concurrent writer, the provider
// refreshes exactly once, persists the rotated pair, and — through the
// merge-save — preserves non-auth fields another process may have on disk.
func TestTokenProviderRefreshesOnceWhenDiskUnchanged(t *testing.T) {
	var tokenPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			tokenPosts.Add(1)
			_, _ = w.Write([]byte(tokenSetBody))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withRaceTestEnv(t, server.URL)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedOAuthConfig(t, cfgPath, staleAccess, staleRefresh, time.Now().Add(-time.Minute))
	// A concurrent process set a default workspace after our process loaded
	// the config: the refresh's merge-save must not revert it.
	withDisk, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	withDisk.DefaultWorkspace = "ws_concurrent"
	if err := withDisk.Save(cfgPath); err != nil {
		t.Fatalf("write concurrent default workspace: %v", err)
	}

	a := raceTestApp(t, cfgPath, server.URL)
	token, err := a.client.Token(context.Background())
	if err != nil {
		t.Fatalf("token provider: %v", err)
	}
	if token != freshAccess {
		t.Errorf("token = %q, want %q", token, freshAccess)
	}
	if got := tokenPosts.Load(); got != 1 {
		t.Errorf("token endpoint POSTs = %d, want exactly 1", got)
	}

	// A second call takes the in-memory fast path: no further POST.
	if token, err := a.client.Token(context.Background()); err != nil || token != freshAccess {
		t.Fatalf("second token call = (%q, %v), want (%q, nil)", token, err, freshAccess)
	}
	if got := tokenPosts.Load(); got != 1 {
		t.Errorf("token endpoint POSTs after second call = %d, want still 1", got)
	}

	disk := readDiskAuth(t, cfgPath)
	if disk == nil || disk.RefreshToken != freshRefresh || disk.AccessToken != freshAccess {
		t.Errorf("on-disk auth = %+v, want the rotated pair persisted", disk)
	}
	diskCfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if diskCfg.DefaultWorkspace != "ws_concurrent" {
		t.Errorf("defaultWorkspace = %q, want ws_concurrent (merge-save must not clobber concurrent writes)", diskCfg.DefaultWorkspace)
	}
}

// TestTokenProviderNoRefreshWhenTokenValid pins the no-op path: an access
// token with more than a minute left is returned without touching the token
// endpoint or the config file.
func TestTokenProviderNoRefreshWhenTokenValid(t *testing.T) {
	var tokenPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			tokenPosts.Add(1)
			_, _ = w.Write([]byte(tokenSetBody))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withRaceTestEnv(t, server.URL)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedOAuthConfig(t, cfgPath, staleAccess, staleRefresh, time.Now().Add(10*time.Minute))
	a := raceTestApp(t, cfgPath, server.URL)

	token, err := a.client.Token(context.Background())
	if err != nil {
		t.Fatalf("token provider: %v", err)
	}
	if token != staleAccess {
		t.Errorf("token = %q, want the still-valid %q", token, staleAccess)
	}
	if got := tokenPosts.Load(); got != 0 {
		t.Errorf("token endpoint POSTs = %d, want 0", got)
	}
}
