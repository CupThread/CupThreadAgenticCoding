package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
)

// The tests in this file pin the recovery contract of the transparent
// refresh (issue #299): the rotated pair is single-use server-side, so a
// failing merge-save must be retried, must fall back to a 0600 recovery
// file next to the config instead of losing the rotation, and the
// invalid_grant path must adopt such a recovery file instead of demanding
// 'auth login' for what is only a transient local disk error.

const (
	recoveredAccess  = "cpt_recovered_access"
	recoveredRefresh = "cpt_recovered_refresh"
	secondAccess     = "cpt_second_access"
	secondRefresh    = "cpt_second_refresh"
	tokenSetBody2    = `{"access_token":"` + secondAccess + `","refresh_token":"` + secondRefresh + `","token_type":"bearer","expires_in":3600,"scope":"console"}`
)

// forcePersistFailure points persistAuthPair at an always-failing stub and
// drops the retry sleeps, restoring both after the test.
func forcePersistFailure(t *testing.T, err error) {
	t.Helper()
	origSave := persistAuthPair
	persistAuthPair = func(*app) error { return err }
	origDelays := authSaveRetryDelays
	authSaveRetryDelays = nil
	t.Cleanup(func() {
		persistAuthPair = origSave
		authSaveRetryDelays = origDelays
	})
}

// listRecoveryFiles returns the recovery files next to cfgPath, sorted.
func listRecoveryFiles(t *testing.T, cfgPath string) []string {
	t.Helper()
	matches, err := filepath.Glob(cfgPath + ".recovery-*")
	if err != nil {
		t.Fatalf("glob recovery files: %v", err)
	}
	return matches
}

// writeRecoveryFile seeds a recovery file the way writeRecoveryCredential
// does (Auth JSON at <cfgPath>.recovery-<ts>), for tests that simulate a
// recovery left behind by a previous invocation.
func writeRecoveryFile(t *testing.T, cfgPath string, authState *config.Auth) string {
	t.Helper()
	return writeRecoveryFileAt(t, cfgPath, 111111111111111111, authState)
}

// writeRecoveryFileAt seeds a recovery file with an explicit timestamp
// suffix, for tests that need several candidates with a deterministic
// newest-first order.
func writeRecoveryFileAt(t *testing.T, cfgPath string, ts int64, authState *config.Auth) string {
	t.Helper()
	data, err := json.Marshal(authState)
	if err != nil {
		t.Fatalf("encode recovery fixture: %v", err)
	}
	path := fmt.Sprintf("%s.recovery-%d", cfgPath, ts)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write recovery fixture: %v", err)
	}
	return path
}

// TestRefreshSaveFailureWritesRecoveryCopy is the issue #299 regression: a
// merge-save that fails for good after a successful rotation must not lose
// the pair — it is written to a 0600 recovery file next to the config, the
// warning names that file on stderr in table and --json modes, the command
// still gets the minted access token, and no error is reported.
func TestRefreshSaveFailureWritesRecoveryCopy(t *testing.T) {
	for _, mode := range []string{"table", "json"} {
		t.Run(mode, func(t *testing.T) {
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
			seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-time.Minute))
			a := raceTestApp(t, cfgPath, server.URL)
			if mode == "json" {
				a.out = output.New(io.Discard, output.FormatJSON)
			}
			forcePersistFailure(t, errors.New("write config: no space left on device"))

			var token string
			var tokErr error
			stderr := captureStderr(t, func() {
				token, tokErr = a.client.Token(context.Background())
			})

			if tokErr != nil {
				t.Fatalf("token provider: %v", tokErr)
			}
			if token != freshAccess {
				t.Errorf("token = %q, want the minted %q (the command must complete with it)", token, freshAccess)
			}

			files := listRecoveryFiles(t, cfgPath)
			if len(files) != 1 {
				t.Fatalf("recovery files = %v, want exactly 1", files)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatalf("read recovery file: %v", err)
			}
			var saved config.Auth
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatalf("parse recovery file: %v", err)
			}
			if saved.Method != "oauth" || saved.AccessToken != freshAccess || saved.RefreshToken != freshRefresh {
				t.Errorf("recovery pair = %+v, want exactly the pair the token endpoint issued", saved)
			}
			if saved.ClientID != auth.FirstPartyClientID {
				t.Errorf("recovery clientId = %q, want %q", saved.ClientID, auth.FirstPartyClientID)
			}
			// The recovery file holds a full bearer pair: it must never be
			// created group/world-readable (the 0600 create mode can only be
			// tightened by the umask, never loosened).
			if runtime.GOOS != "windows" {
				fi, err := os.Stat(files[0])
				if err != nil {
					t.Fatalf("stat recovery file: %v", err)
				}
				if perm := fi.Mode().Perm(); perm&0o077 != 0 {
					t.Errorf("recovery file perms = %04o, want no group/world bits", perm)
				}
			}

			if !strings.Contains(stderr, files[0]) {
				t.Errorf("stderr does not name the recovery file:\n%s", stderr)
			}
			if !strings.Contains(stderr, "recovery copy") {
				t.Errorf("stderr does not explain the recovery:\n%s", stderr)
			}

			// The disk config is unchanged (the save failed), while the
			// in-memory pair is acknowledged so a later saveConfig in this
			// process cannot re-apply it over a concurrent credential
			// (issue #285).
			if disk := readDiskAuth(t, cfgPath); disk == nil || disk.RefreshToken != staleRefresh {
				t.Errorf("on-disk auth = %+v, want the stale pair (the save failed)", disk)
			}
			if a.cfgBaseline == nil || !reflect.DeepEqual(a.cfgBaseline.Auth, a.cfg.Auth) {
				t.Errorf("baseline auth = %+v, want it acknowledged to the rotated pair %+v", a.cfgBaseline.Auth, a.cfg.Auth)
			}
		})
	}
}

// TestRefreshSaveRetryThenSucceeds pins the retry: a merge-save that fails
// once and succeeds on the second attempt must leave the rotated pair on
// disk and no recovery file behind.
func TestRefreshSaveRetryThenSucceeds(t *testing.T) {
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
	seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-time.Minute))
	a := raceTestApp(t, cfgPath, server.URL)

	var attempts atomic.Int32
	origSave := persistAuthPair
	origDelays := authSaveRetryDelays
	persistAuthPair = func(x *app) error {
		if attempts.Add(1) == 1 {
			return errors.New("read config: transient EIO")
		}
		return origSave(x)
	}
	authSaveRetryDelays = []time.Duration{0} // second attempt, no sleep
	t.Cleanup(func() {
		persistAuthPair = origSave
		authSaveRetryDelays = origDelays
	})

	token, err := a.client.Token(context.Background())
	if err != nil {
		t.Fatalf("token provider: %v", err)
	}
	if token != freshAccess {
		t.Errorf("token = %q, want %q", token, freshAccess)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("save attempts = %d, want 2 (one transient failure, one success)", got)
	}
	disk := readDiskAuth(t, cfgPath)
	if disk == nil || disk.RefreshToken != freshRefresh || disk.AccessToken != freshAccess {
		t.Errorf("on-disk auth = %+v, want the rotated pair persisted by the retry", disk)
	}
	if files := listRecoveryFiles(t, cfgPath); len(files) != 0 {
		t.Errorf("recovery files = %v, want none (the save recovered on its own)", files)
	}
}

// TestInvalidGrantAdoptsRecoveryFile drives the two-invocation story end to
// end: the first invocation rotates but cannot persist (recovery file
// written), the second finds the consumed on-disk token, gets invalid_grant,
// adopts the recovery pair, persists it, and cleans the recovery file up —
// instead of demanding 'auth login' over a transient disk error. A garbage
// file matching the recovery naming must be skipped, not adopted.
func TestInvalidGrantAdoptsRecoveryFile(t *testing.T) {
	var tokenPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			if tokenPosts.Add(1) == 1 {
				_, _ = w.Write([]byte(tokenSetBody))
				return
			}
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
	seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-time.Minute))

	// First invocation: the rotation succeeds, persistence fails, the pair
	// survives only in the recovery file.
	origSave := persistAuthPair
	origDelays := authSaveRetryDelays
	persistAuthPair = func(*app) error { return errors.New("write config: input/output error") }
	authSaveRetryDelays = nil
	t.Cleanup(func() {
		persistAuthPair = origSave
		authSaveRetryDelays = origDelays
	})
	a1 := raceTestApp(t, cfgPath, server.URL)
	if _, err := a1.client.Token(context.Background()); err != nil {
		t.Fatalf("first invocation token provider: %v", err)
	}
	if files := listRecoveryFiles(t, cfgPath); len(files) != 1 {
		t.Fatalf("after first invocation, recovery files = %v, want exactly 1", files)
	}
	// The disk heals before the second invocation: saves go through again.
	persistAuthPair = origSave

	// Second invocation: fresh process, the disk still holds the consumed
	// pair; a garbage recovery file is also present.
	if err := os.WriteFile(cfgPath+".recovery-garbage", []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write garbage recovery file: %v", err)
	}
	a2 := raceTestApp(t, cfgPath, server.URL)
	token, err := a2.client.Token(context.Background())
	if err != nil {
		t.Fatalf("second invocation token provider: %v", err)
	}
	if token != freshAccess {
		t.Errorf("token = %q, want the recovered %q", token, freshAccess)
	}
	if got := tokenPosts.Load(); got != 2 {
		t.Errorf("token endpoint POSTs = %d, want 2 (rotation, then invalid_grant)", got)
	}
	disk := readDiskAuth(t, cfgPath)
	if disk == nil || disk.RefreshToken != freshRefresh || disk.AccessToken != freshAccess {
		t.Errorf("on-disk auth = %+v, want the adopted pair persisted", disk)
	}
	if files := listRecoveryFiles(t, cfgPath); len(files) != 0 {
		t.Errorf("recovery files = %v, want none (the adopted pair is persisted)", files)
	}
}

// TestRecoveryAdoptionUnsavedKeepsFileAndReturnsToken covers adoption while
// the disk is still broken: the adopted pair cannot be saved, so the command
// still completes with its access token, the recovery file stays as the only
// record, and the baseline acknowledges the adopted pair so a later
// saveConfig cannot re-apply it over a concurrent credential (issue #285).
func TestRecoveryAdoptionUnsavedKeepsFileAndReturnsToken(t *testing.T) {
	var tokenPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			if tokenPosts.Add(1) == 1 {
				_, _ = w.Write([]byte(tokenSetBody))
				return
			}
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
	seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-time.Minute))

	forcePersistFailure(t, errors.New("write config: quota exceeded"))
	a1 := raceTestApp(t, cfgPath, server.URL)
	if _, err := a1.client.Token(context.Background()); err != nil {
		t.Fatalf("first invocation token provider: %v", err)
	}

	a2 := raceTestApp(t, cfgPath, server.URL)
	var token string
	var tokErr error
	stderr := captureStderr(t, func() {
		token, tokErr = a2.client.Token(context.Background())
	})
	if tokErr != nil {
		t.Fatalf("second invocation token provider: %v", tokErr)
	}
	if token != freshAccess {
		t.Errorf("token = %q, want the recovered %q", token, freshAccess)
	}
	if !strings.Contains(stderr, "could not be saved") {
		t.Errorf("stderr does not warn about the unsaved adoption:\n%s", stderr)
	}
	files := listRecoveryFiles(t, cfgPath)
	if len(files) != 1 {
		t.Fatalf("recovery files = %v, want exactly the adopted one", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read recovery file: %v", err)
	}
	var saved config.Auth
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("parse recovery file: %v", err)
	}
	if saved.RefreshToken != freshRefresh {
		t.Errorf("recovery pair = %+v, want the adopted pair kept as the only record", saved)
	}
	if !reflect.DeepEqual(a2.cfgBaseline.Auth, a2.cfg.Auth) {
		t.Errorf("baseline auth = %+v, want it acknowledged to the adopted pair %+v", a2.cfgBaseline.Auth, a2.cfg.Auth)
	}
}

// TestRecoveryAdoptionExpiredPairRotatesAdopted pins the expired-recovery
// path: an adopted pair whose access token has already been spent cannot
// serve the request, so the provider rotates it right away under the held
// lock — the refresh POST must carry the adopted refresh token, not replay
// the consumed on-disk one, and the newly minted pair must be persisted.
func TestRecoveryAdoptionExpiredPairRotatesAdopted(t *testing.T) {
	var tokenPosts atomic.Int32
	var mu sync.Mutex
	var gotRefreshTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			_ = r.ParseForm()
			mu.Lock()
			gotRefreshTokens = append(gotRefreshTokens, r.FormValue("refresh_token"))
			mu.Unlock()
			tokenPosts.Add(1)
			if r.FormValue("refresh_token") == staleRefresh {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(invalidGrantBd))
				return
			}
			_, _ = w.Write([]byte(tokenSetBody2))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withRaceTestEnv(t, server.URL)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-2*time.Minute))
	writeRecoveryFile(t, cfgPath, &config.Auth{
		Method:        "oauth",
		AccessToken:   recoveredAccess,
		RefreshToken:  recoveredRefresh,
		ExpiresAt:     time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		ClientID:      auth.FirstPartyClientID,
		IssuedBaseURL: server.URL,
	})

	a := raceTestApp(t, cfgPath, server.URL)
	token, err := a.client.Token(context.Background())
	if err != nil {
		t.Fatalf("token provider: %v", err)
	}
	if token != secondAccess {
		t.Errorf("token = %q, want the freshly minted %q", token, secondAccess)
	}
	if got := tokenPosts.Load(); got != 2 {
		t.Errorf("token endpoint POSTs = %d, want 2 (invalid_grant, then rotation of the adopted pair)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gotRefreshTokens) != 2 || gotRefreshTokens[0] != staleRefresh || gotRefreshTokens[1] != recoveredRefresh {
		t.Errorf("refresh tokens POSTed = %v, want [stale (invalid_grant), recovered (rotated)]", gotRefreshTokens)
	}
	disk := readDiskAuth(t, cfgPath)
	if disk == nil || disk.RefreshToken != secondRefresh || disk.AccessToken != secondAccess {
		t.Errorf("on-disk auth = %+v, want the newly minted pair persisted", disk)
	}
	if files := listRecoveryFiles(t, cfgPath); len(files) != 0 {
		t.Errorf("recovery files = %v, want none (the rotation is persisted)", files)
	}
}

// TestStaleDiskPairNotAdoptedOverRecoveredMemory pins the freshness guard on
// the disk-adoption branch: when the in-memory pair is a recovered rotation
// that is newer than the stale on-disk pair, the provider must refresh the
// recovered token instead of resurrecting the consumed disk access token
// (issue #299).
func TestStaleDiskPairNotAdoptedOverRecoveredMemory(t *testing.T) {
	var tokenPosts atomic.Int32
	var mu sync.Mutex
	var gotRefreshTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			_ = r.ParseForm()
			mu.Lock()
			gotRefreshTokens = append(gotRefreshTokens, r.FormValue("refresh_token"))
			mu.Unlock()
			tokenPosts.Add(1)
			_, _ = w.Write([]byte(tokenSetBody))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	withRaceTestEnv(t, server.URL)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-2*time.Minute))
	a := raceTestApp(t, cfgPath, server.URL)
	// The in-process recovery of a previous step: a pair newer than the disk
	// one, near expiry so the provider takes the refresh path.
	a.cfg.Auth = &config.Auth{
		Method:        "oauth",
		AccessToken:   recoveredAccess,
		RefreshToken:  recoveredRefresh,
		ExpiresAt:     time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339),
		ClientID:      auth.FirstPartyClientID,
		IssuedBaseURL: server.URL,
	}

	token, err := a.client.Token(context.Background())
	if err != nil {
		t.Fatalf("token provider: %v", err)
	}
	if token != freshAccess {
		t.Errorf("token = %q, want %q", token, freshAccess)
	}
	if got := tokenPosts.Load(); got != 1 {
		t.Errorf("token endpoint POSTs = %d, want 1", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gotRefreshTokens) != 1 || gotRefreshTokens[0] != recoveredRefresh {
		t.Errorf("refresh tokens POSTed = %v, want exactly the recovered %q", gotRefreshTokens, recoveredRefresh)
	}
	if a.cfg.Auth.RefreshToken != freshRefresh {
		t.Errorf("in-memory auth = %+v, want the newly rotated pair (not the stale disk adoption)", a.cfg.Auth)
	}
}

// TestLoginPurgesStaleRecoveryFiles and TestLogoutPurgesStaleRecoveryFiles
// pin the cross-identity guard from the #299 review: a recovery file left by
// a previous chain must not survive a deliberate credential replacement
// (login) or de-provisioning (logout), or a later invalid_grant could adopt
// the wrong account's tokens.
func TestLoginPurgesStaleRecoveryFiles(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	server := meServer(t)
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-time.Minute))
	writeRecoveryFile(t, cfgPath, &config.Auth{
		Method:        "oauth",
		AccessToken:   recoveredAccess,
		RefreshToken:  recoveredRefresh,
		ExpiresAt:     time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		ClientID:      auth.FirstPartyClientID,
		IssuedBaseURL: server.URL,
	})

	if _, _, err := runRootCapture(t, cfgPath, server.URL, "auth", "login", "--token", "cpt_new_tok_12345", "--json"); err != nil {
		t.Fatalf("auth login: %v", err)
	}
	if files := listRecoveryFiles(t, cfgPath); len(files) != 0 {
		t.Errorf("recovery files = %v, want none after a successful login replaced the credential", files)
	}
}

func TestLogoutPurgesStaleRecoveryFiles(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedOAuthConfig(t, cfgPath, "https://api.cupthread.com", staleAccess, staleRefresh, time.Now().Add(-time.Minute))
	writeRecoveryFile(t, cfgPath, &config.Auth{
		Method:        "oauth",
		AccessToken:   recoveredAccess,
		RefreshToken:  recoveredRefresh,
		ExpiresAt:     time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		ClientID:      auth.FirstPartyClientID,
		IssuedBaseURL: "https://api.cupthread.com",
	})

	if _, _, err := runRootCapture(t, cfgPath, "http://127.0.0.1:1", "auth", "logout", "--json"); err != nil {
		t.Fatalf("auth logout: %v", err)
	}
	if files := listRecoveryFiles(t, cfgPath); len(files) != 0 {
		t.Errorf("recovery files = %v, want none after logout de-provisioned the credential", files)
	}
}

// TestRecoveryAdoptionSkipsOtherIssuer pins the issuer guard: a recovery
// file whose credential resolves to a different server than the one the
// failed refresh targeted must never be adopted, even when it is the newest
// candidate (issue #299 review).
func TestRecoveryAdoptionSkipsOtherIssuer(t *testing.T) {
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
	seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-time.Minute))
	// Newest recovery file, but for a credential issued by another server.
	writeRecoveryFile(t, cfgPath, &config.Auth{
		Method:        "oauth",
		AccessToken:   recoveredAccess,
		RefreshToken:  recoveredRefresh,
		ExpiresAt:     time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		ClientID:      auth.FirstPartyClientID,
		IssuedBaseURL: "https://other-server.example.com",
	})

	a := raceTestApp(t, cfgPath, server.URL)
	_, err := a.client.Token(context.Background())
	if err == nil {
		t.Fatal("expected an error when the only recovery file belongs to another issuer")
	}
	if !strings.Contains(err.Error(), "revoked server-side") {
		t.Errorf("error = %v, want the revoked-chain diagnosis (no cross-issuer adoption)", err)
	}
	if got := tokenPosts.Load(); got != 1 {
		t.Errorf("token endpoint POSTs = %d, want 1", got)
	}
	if a.cfg.Auth == nil || a.cfg.Auth.RefreshToken != staleRefresh {
		t.Errorf("in-memory auth = %+v, want the on-disk pair untouched (no cross-issuer adoption)", a.cfg.Auth)
	}
}

// TestRefreshRecoveryWriteFailureFailsWithoutToken covers the point where
// nothing can be persisted: when both the merge-save and the recovery file
// fail (a full, read-only or unwritable config directory), the provider must
// fail the command honestly instead of handing back a token it cannot
// preserve — the rotated pair is lost either way, and the error says so.
func TestRefreshRecoveryWriteFailureFailsWithoutToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based failure injection is a POSIX technique: Windows ignores directory mode bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("chmod-based failure injection needs a non-root user")
	}
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

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-time.Minute))
	// The lock file must exist before the directory goes read-only: the
	// refresh legitimately holds it while the save fails.
	lock, err := os.OpenFile(cfgPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("pre-create config lock: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("close config lock: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("read-only config dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	origDelays := authSaveRetryDelays
	authSaveRetryDelays = nil
	t.Cleanup(func() { authSaveRetryDelays = origDelays })

	a := raceTestApp(t, cfgPath, server.URL)
	var stderr string
	var token string
	var tokErr error
	stderr = captureStderr(t, func() {
		token, tokErr = a.client.Token(context.Background())
	})
	if tokErr == nil {
		t.Fatalf("token provider = (%q, nil), want an error when nothing could be persisted", token)
	}
	if !strings.Contains(tokErr.Error(), "save refreshed tokens") {
		t.Errorf("error = %v, want the save-failure diagnosis", tokErr)
	}
	if !strings.Contains(tokErr.Error(), "recovery copy failed") {
		t.Errorf("error = %v, want the failed-recovery diagnosis", tokErr)
	}
	if files := listRecoveryFiles(t, cfgPath); len(files) != 0 {
		t.Errorf("recovery files = %v, want none (the write failed)", files)
	}
	if strings.Contains(stderr, "recovery copy carrying the rotated pair") {
		t.Errorf("stderr claims a recovery copy exists:\n%s", stderr)
	}
}

// TestRecoveryRecursionNeverRevisitsACandidate pins the attempted-set bound
// from the #299 review: with two expired recovery candidates that the server
// rejects with invalid_grant, the recursion must try each candidate exactly
// once and terminate in the revoked-chain error — never alternate between
// the two candidates forever while holding the config lock (each rejected
// candidate is recorded before its retry, so a revisit is impossible).
func TestRecoveryRecursionNeverRevisitsACandidate(t *testing.T) {
	var mu sync.Mutex
	var gotRefreshTokens []string
	var tokenPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			_ = r.ParseForm()
			mu.Lock()
			gotRefreshTokens = append(gotRefreshTokens, r.FormValue("refresh_token"))
			mu.Unlock()
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
	// The disk pair sits a full five minutes behind the recovery candidates
	// (RFC3339 truncates to seconds, so a one-minute gap could still land
	// both on the same second and trip the documented same-second
	// freshness tie-break into adopting the stale disk pair).
	seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-5*time.Minute))
	// Two expired same-issuer recovery candidates; the 9-suffixed file is
	// the newest, so the recursion must try it first and never come back.
	expired := func(access, refresh string) *config.Auth {
		return &config.Auth{
			Method:        "oauth",
			AccessToken:   access,
			RefreshToken:  refresh,
			ExpiresAt:     time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
			ClientID:      auth.FirstPartyClientID,
			IssuedBaseURL: server.URL,
		}
	}
	writeRecoveryFileAt(t, cfgPath, 111111111111111111, expired(recoveredAccess, recoveredRefresh))
	writeRecoveryFileAt(t, cfgPath, 999999999999999999, expired(secondAccess, secondRefresh))

	a := raceTestApp(t, cfgPath, server.URL)
	_, err := a.client.Token(context.Background())
	if err == nil {
		t.Fatal("expected the revoked-chain error when every recovery candidate is rejected")
	}
	if !strings.Contains(err.Error(), "revoked server-side") {
		t.Errorf("error = %v, want the revoked-chain diagnosis after the candidates run out", err)
	}
	if got := tokenPosts.Load(); got != 3 {
		t.Errorf("token endpoint POSTs = %d, want 3 (the disk pair, then each candidate once)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{staleRefresh, secondRefresh, recoveredRefresh}
	if !reflect.DeepEqual(gotRefreshTokens, want) {
		t.Errorf("refresh tokens POSTed = %v, want %v (each candidate at most once)", gotRefreshTokens, want)
	}
}

// TestRecoveryAdoptionAdoptsLegacyPairWithoutPinnedIssuer pins the issuer
// fallback (#299 review): recovery records written before issuer pinning
// existed carry no IssuedBaseURL, and they must resolve against the failed
// credential's issuer — not against the default server — or a custom
// base-URL user would be told to log in again over what is only a transient
// disk error.
func TestRecoveryAdoptionAdoptsLegacyPairWithoutPinnedIssuer(t *testing.T) {
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
	seedOAuthConfig(t, cfgPath, server.URL, staleAccess, staleRefresh, time.Now().Add(-time.Minute))
	writeRecoveryFile(t, cfgPath, &config.Auth{
		Method:       "oauth",
		AccessToken:  recoveredAccess,
		RefreshToken: recoveredRefresh,
		ExpiresAt:    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		ClientID:     auth.FirstPartyClientID,
		// No IssuedBaseURL: a legacy record from before pinning existed.
	})

	a := raceTestApp(t, cfgPath, server.URL)
	token, err := a.client.Token(context.Background())
	if err != nil {
		t.Fatalf("token provider: %v", err)
	}
	if token != recoveredAccess {
		t.Errorf("token = %q, want the recovered %q (the legacy pair must be adopted)", token, recoveredAccess)
	}
	if got := tokenPosts.Load(); got != 1 {
		t.Errorf("token endpoint POSTs = %d, want 1 (adopt the usable pair, no second refresh)", got)
	}
	disk := readDiskAuth(t, cfgPath)
	if disk == nil || disk.RefreshToken != recoveredRefresh || disk.AccessToken != recoveredAccess {
		t.Errorf("on-disk auth = %+v, want the adopted legacy pair persisted", disk)
	}
	if files := listRecoveryFiles(t, cfgPath); len(files) != 0 {
		t.Errorf("recovery files = %v, want none (the adopted pair is persisted)", files)
	}
}

// TestLogoutWarnsWhenRecoveryCleanupFails covers the purge-failure surface
// (#299 review): a recovery entry that cannot be removed must be reported
// on stderr — a surviving entry keeps an adoptable bearer-and-refresh pair
// on disk — while logout itself still succeeds, because the credential
// de-provisioning it performs is already complete.
func TestLogoutWarnsWhenRecoveryCleanupFails(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seedOAuthConfig(t, cfgPath, "https://api.cupthread.com", staleAccess, staleRefresh, time.Now().Add(-time.Minute))
	// A non-empty directory wearing a recovery-file name is the portable
	// removal-failure injection: os.Remove rejects it on every platform
	// (ENOTEMPTY / ERROR_DIR_NOT_EMPTY).
	stuck := filepath.Join(filepath.Dir(cfgPath), filepath.Base(cfgPath)+".recovery-"+strings.Repeat("7", 18))
	if err := os.Mkdir(stuck, 0o700); err != nil {
		t.Fatalf("mkdir stuck recovery entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stuck, "pair.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("fill stuck recovery entry: %v", err)
	}

	_, stderr, err := runRootCapture(t, cfgPath, "http://127.0.0.1:1", "auth", "logout", "--json")
	if err != nil {
		t.Fatalf("auth logout: %v (a cleanup failure must not fail logout)", err)
	}
	if !strings.Contains(stderr, "could not be removed") || !strings.Contains(stderr, stuck) {
		t.Errorf("stderr does not warn about the surviving recovery entry:\n%s", stderr)
	}
	if fi, serr := os.Stat(stuck); serr != nil || !fi.IsDir() {
		t.Errorf("stuck recovery entry stat = (%v, %v), want it still present (the removal genuinely failed)", fi, serr)
	}
}
