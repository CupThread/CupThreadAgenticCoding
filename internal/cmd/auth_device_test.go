package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/CupThread/CupThreadAgenticCoding/internal/config"
	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
)

// TestLoginDevicePollsDeviceTokenEndpoint pins the issue #266 wiring: the
// device-flow poll must hit the dedicated device token endpoint
// (/api/v1/oauth/device/token, the RFC 8628 §3.4/§3.5 endpoint the API
// contract documents) and never the regular token endpoint, which answers
// 400 unsupported_grant_type for the device grant. The stub mirrors the
// production shapes — the regular token endpoint refuses the device grant —
// so a regression to the old wiring fails the login instead of passing.
func TestLoginDevicePollsDeviceTokenEndpoint(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	var mu sync.Mutex
	hits := map[string]int{}
	var deviceGrant string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		if r.URL.Path == auth.DeviceTokenPath {
			_ = r.ParseForm()
			deviceGrant = r.Form.Get("grant_type")
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case auth.DeviceAuthorizePath:
			_, _ = w.Write([]byte(`{"device_code":"dev_code_wiring","user_code":"WIRE-0001","verification_uri":"https://cupthread.example/activate","interval":1,"expires_in":120}`))
		case auth.DeviceTokenPath:
			_, _ = w.Write([]byte(`{"access_token":"cpt_device_tok_0000","refresh_token":"cpt_device_refresh","token_type":"bearer","expires_in":3600,"scope":"console"}`))
		case auth.TokenPath:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"unsupported_grant_type","error_description":"Unsupported grant_type: urn:ietf:params:oauth:grant-type:device_code"}`))
		default:
			_, _ = w.Write([]byte(meFixture))
		}
	}))
	defer server.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	stdout, stderr, err := runRootCapture(t, cfgPath, server.URL, "auth", "login", "--device", "--json")
	if err != nil {
		t.Fatalf("login --device against the contract-shaped stub: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	doc := unmarshalOneJSON(t, stdout)
	if doc["method"] != "device" {
		t.Errorf("login method = %v, want device", doc["method"])
	}
	if doc["verificationUri"] != "https://cupthread.example/activate" {
		t.Errorf("verificationUri = %v, want the stub's URI echoed", doc["verificationUri"])
	}

	mu.Lock()
	defer mu.Unlock()
	if hits[auth.TokenPath] != 0 {
		t.Errorf("regular token endpoint received %d request(s), want 0 — the device grant does not belong there", hits[auth.TokenPath])
	}
	if hits[auth.DeviceAuthorizePath] != 1 {
		t.Errorf("device authorize endpoint received %d request(s), want 1", hits[auth.DeviceAuthorizePath])
	}
	if hits[auth.DeviceTokenPath] == 0 {
		t.Fatal("device token endpoint never polled; the flow did not follow the RFC 8628 contract")
	}
	if deviceGrant != "urn:ietf:params:oauth:grant-type:device_code" {
		t.Errorf("device-token poll grant_type = %q, want the device grant", deviceGrant)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.Auth == nil || cfg.Auth.Method != "oauth" || cfg.Auth.AccessToken != "cpt_device_tok_0000" || cfg.Auth.RefreshToken != "cpt_device_refresh" {
		t.Errorf("stored auth = %+v, want the device-flow-issued oauth pair", cfg.Auth)
	}
}

// TestFinishOAuthLoginRefusesEmptyAccessToken pins the issue #266 last line
// of defense: a TokenSet without an access token must neither be persisted
// nor celebrated — the command fails, stdout stays free of "✓", and a
// pre-existing stored credential (a working PAT) survives byte-identical on
// disk instead of being overwritten with an empty OAuth section.
func TestFinishOAuthLoginRefusesEmptyAccessToken(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "")
	t.Setenv("CUPTHREAD_BASE_URL", "")

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	pre := &config.Config{
		Auth: &config.Auth{Method: "token", AccessToken: "cpt_pretendpat_000", TokenPrefix: "cpt_pretendp"},
	}
	if err := pre.Save(cfgPath); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read seed config: %v", err)
	}

	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load seed config: %v", err)
	}

	oldA := A
	defer func() { A = oldA }()
	A = &app{out: output.New(os.Stdout, output.FormatTable), cfg: loaded, cfgPath: cfgPath}
	A.client = A.buildClient()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	loginErr := finishOAuthLogin(context.Background(), &auth.TokenSet{}, loginResult{Method: "device"})
	os.Stdout = oldStdout
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	outBytes, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}

	if loginErr == nil {
		t.Fatal("finishOAuthLogin accepted an access_token-less TokenSet, want a refusal")
	}
	if !strings.Contains(loginErr.Error(), "access token") {
		t.Errorf("error = %q, want it to name the missing access token", loginErr)
	}
	if strings.Contains(string(outBytes), "✓") {
		t.Errorf("stdout printed a success line for a failed login:\n%s", outBytes)
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config after refusal: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("config file changed on a refused login:\nbefore: %s\nafter:  %s", before, after)
	}
	var cfg config.Config
	if err := json.Unmarshal(after, &cfg); err != nil {
		t.Fatalf("parse config after refusal: %v", err)
	}
	if cfg.Auth == nil || cfg.Auth.Method != "token" || cfg.Auth.AccessToken != "cpt_pretendpat_000" {
		t.Errorf("stored auth = %+v, want the pre-existing PAT untouched", cfg.Auth)
	}
}
