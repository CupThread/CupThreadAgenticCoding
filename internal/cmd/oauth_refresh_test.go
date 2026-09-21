package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTokenProviderRefreshIsBounded pins the command-level half of issue #56:
// an OAuth config with an expired access token makes every authenticated
// command transparently refresh against the token endpoint, so an endpoint
// that accepts the connection but never responds must surface as a refresh
// error within the refresh deadline instead of hanging forever.
//
// The command runs with an unbounded context — exactly what the real CLI
// passes via cobra's context.Background — so only the production bounds
// (oauthRefreshTimeout, shortened for the test, plus the auth client) can
// rescue it; on the regression path the guard below fails the test and the
// deferred stdout restore keeps the leaked goroutine from poisoning the rest
// of the package.
func TestTokenProviderRefreshIsBounded(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "") // keep the test hermetic

	oldTimeout := oauthRefreshTimeout
	oauthRefreshTimeout = 200 * time.Millisecond
	defer func() { oauthRefreshTimeout = oldTimeout }()

	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/oauth/token" {
			<-release
		}
	}))
	defer server.Close()
	defer close(release) // unblock the handler first so server.Close can finish

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	expiresAt := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	cfg := `{"auth":{"method":"oauth","accessToken":"cpt_expired","refreshToken":"cpr_stale","clientId":"cupthread-cli","expiresAt":"` + expiresAt + `"}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Swap stdout in the test goroutine so it is restored even when the guard
	// below fires while the command goroutine is still blocked.
	oldStdout := os.Stdout
	rp, wp, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = wp
	defer func() {
		os.Stdout = oldStdout
		_ = wp.Close()
		_, _ = io.Copy(io.Discard, rp)
		_ = rp.Close()
	}()

	root := newRootCmd()
	root.SetArgs([]string{"me", "--base-url", server.URL, "--config", cfgPath})

	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(context.Background()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a refresh error, got success")
		}
		if !strings.Contains(err.Error(), "refresh OAuth token") {
			t.Errorf("error = %v, want a transparent-refresh failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("command still blocked after 5s against a stalled token endpoint (refresh is unbounded?)")
	}
}
