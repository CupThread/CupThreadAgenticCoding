package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
)

// runRootCfg is runRoot with an explicit config path, so tests can assert on
// whether a command wrote the file.
func runRootCfg(t *testing.T, cfgPath, serverURL string, args ...string) (string, error) {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	root := newRootCmd()
	full := append(append([]string{}, args...), "--base-url", serverURL, "--config", cfgPath)
	root.SetArgs(full)
	execErr := root.Execute()

	os.Stdout = oldStdout
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out), execErr
}

// TestLoginTokenFlagRejectsControlChars pins the fail-fast path: a --token
// that still carries embedded whitespace/control characters after trimming
// is rejected naming --token, with no HTTP request and no config write.
// (Trailing CR/LF are trimmed away and now log in cleanly — see
// TestLoginTokenFlagTrimsSurroundingSpace.)
func TestLoginTokenFlagRejectsControlChars(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(meFixture))
	}))
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	for _, bad := range []string{"cpt_x\ty", "cpt_x y", "cpt_\rx"} {
		t.Run(bad, func(t *testing.T) {
			_, err := runRootCfg(t, cfgPath, server.URL, "auth", "login", "--token", bad)
			if err == nil {
				t.Fatal("expected login to fail")
			}
			if !strings.Contains(err.Error(), "--token") || !strings.Contains(err.Error(), "whitespace") {
				t.Errorf("error = %q, want it to name --token and whitespace", err)
			}
			if strings.Contains(err.Error(), "net/http") {
				t.Errorf("error = %q leaks the net/http transport failure", err)
			}
		})
	}
	if requests != 0 {
		t.Errorf("server saw %d requests, want 0", requests)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		t.Error("config file written despite an invalid token")
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat config: %v", err)
	}
}

// TestLoginTokenFlagTrimsSurroundingSpace covers the flag branch trim: padded
// tokens reach the probe as exactly "Bearer cpt_x", login succeeds, and the
// stored credential is the trimmed value.
func TestLoginTokenFlagTrimsSurroundingSpace(t *testing.T) {
	var gotAuth []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(meFixture))
	}))
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	for _, raw := range []string{" cpt_x ", "cpt_x\r", "\tcpt_x\n", "cpt_x\r\n"} {
		gotAuth = nil
		out, err := runRootCfg(t, cfgPath, server.URL, "auth", "login", "--token", raw)
		if err != nil {
			t.Fatalf("login with padded token %q: %v\n%s", raw, err, out)
		}
		if len(gotAuth) != 1 || gotAuth[0] != "Bearer cpt_x" {
			t.Errorf("Authorization for %q = %v, want [Bearer cpt_x]", raw, gotAuth)
		}
		if !strings.Contains(out, "Logged in as dev@example.com") {
			t.Errorf("output for %q missing login confirmation:\n%s", raw, out)
		}
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.Auth == nil {
		t.Fatal("config has no auth after successful login")
	}
	if cfg.Auth.AccessToken != "cpt_x" || cfg.Auth.TokenPrefix != "cpt_x" {
		t.Errorf("stored auth = %+v, want the trimmed cpt_x token", cfg.Auth)
	}
}

// TestEnvTokenCRAuthenticatesCleanly pins the env-path trim: a
// $CUPTHREAD_TOKEN padded with CR/LF/spaces authenticates exactly like a
// clean token (Authorization is byte-identical, "Bearer cpt_x").
func TestEnvTokenCRAuthenticatesCleanly(t *testing.T) {
	for name, raw := range map[string]string{
		"clean":          "cpt_x",
		"trailing CR":    "cpt_x\r",
		"trailing LF":    "cpt_x\n",
		"trailing CRLF":  "cpt_x\r\n",
		"leading space":  " cpt_x",
		"trailing space": "cpt_x ",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CUPTHREAD_TOKEN", raw)
			var gotAuth string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(meFixture))
			}))
			defer server.Close()

			out, err := runRoot(t, server.URL, "me")
			if err != nil {
				t.Fatalf("me with env token %q: %v\n%s", raw, err, out)
			}
			if gotAuth != "Bearer cpt_x" {
				t.Errorf("Authorization = %q, want Bearer cpt_x", gotAuth)
			}
			if !strings.Contains(out, "dev@example.com") {
				t.Errorf("output missing identity:\n%s", out)
			}
		})
	}
}

// TestEnvTokenControlCharsFailFast pins the env-path fail-fast: an embedded
// control character yields a self-diagnosing error naming $CUPTHREAD_TOKEN
// instead of a net/http transport failure, before any request is sent.
func TestEnvTokenControlCharsFailFast(t *testing.T) {
	for _, bad := range []string{"cpt_\tx", "cpt_x\ty", "cpt_x y"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("CUPTHREAD_TOKEN", bad)
			var requests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(meFixture))
			}))
			defer server.Close()

			_, err := runRoot(t, server.URL, "me")
			if err == nil {
				t.Fatal("expected me to fail")
			}
			if !strings.Contains(err.Error(), "$CUPTHREAD_TOKEN") || !strings.Contains(err.Error(), "whitespace") {
				t.Errorf("error = %q, want it to name $CUPTHREAD_TOKEN and whitespace", err)
			}
			if strings.Contains(err.Error(), "net/http") {
				t.Errorf("error = %q leaks the net/http transport failure", err)
			}
			if requests != 0 {
				t.Errorf("server saw %d requests, want 0", requests)
			}
		})
	}
}
