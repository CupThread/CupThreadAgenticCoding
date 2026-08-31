// Package auth implements the CLI login flows against the CupThread OAuth
// server: Authorization Code + PKCE with a local loopback callback (primary)
// and the Device Authorization Grant (fallback for headless environments).
// The server-side contract is specified in SaaS/docs/CLI-OAuth.md.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// FirstPartyClientID is the pre-registered public client for the official CLI.
const FirstPartyClientID = "cupthread-cli"

// Endpoint paths on the API server.
const (
	AuthorizePath    = "/api/v1/oauth/authorize"
	TokenPath        = "/api/v1/oauth/token"
	DeviceAuthorizePath = "/api/v1/oauth/device/authorize"
	DeviceTokenPath  = "/api/v1/oauth/device/token"
)

// CallbackPath is the path registered for the CLI's loopback redirect URI.
// The port is chosen at runtime (loopback port variation is allowed by the
// OAuth spec for native apps, RFC 8252 §7.3).
const CallbackPath = "/cupthread/callback"

// TokenSet is a successful token endpoint response.
type TokenSet struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// Endpoints derives the OAuth endpoint URLs from the API base URL.
func Endpoints(baseURL string) (authorize, token, deviceAuthorize, deviceToken string) {
	base := strings.TrimRight(baseURL, "/")
	return base + AuthorizePath, base + TokenPath, base + DeviceAuthorizePath, base + DeviceTokenPath
}

// OpenBrowser opens url in the default browser, returning an error when no
// mechanism is available (the caller should then print the URL instead).
func OpenBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch {
	case commandExists("open"):
		cmd = exec.Command("open", rawURL)
	case commandExists("xdg-open"):
		cmd = exec.Command("xdg-open", rawURL)
	case commandExists("rundll32"):
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		return errors.New("no browser launcher available")
	}
	return cmd.Start()
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ─── PKCE (Authorization Code + local loopback callback) ─────────────────────

// LoginPKCE runs the browser-based login. authorizeURL/tokenURL come from
// Endpoints; openBrowser is called with the authorize URL (pass nil for
// OpenBrowser; when opening fails the URL is printed to stderr instead).
func LoginPKCE(ctx context.Context, authorizeURL, tokenURL, clientID string, openBrowser func(string) error) (*TokenSet, error) {
	verifier, err := randomToken(64)
	if err != nil {
		return nil, err
	}
	challenge := s256Challenge(verifier)
	state, err := randomToken(32)
	if err != nil {
		return nil, err
	}

	listener, err := listenLoopback()
	if err != nil {
		return nil, fmt.Errorf("start local callback server: %w", err)
	}
	defer listener.Close()
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", listener.Port, CallbackPath)

	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"full"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(CallbackPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("error") != "" {
			fmt.Fprintf(w, "Authorization was declined: %s. You can close this window.", r.URL.Query().Get("error"))
			errCh <- fmt.Errorf("authorization declined: %s", r.URL.Query().Get("error_description"))
			return
		}
		if got := r.URL.Query().Get("state"); got != state {
			fmt.Fprintf(w, "State mismatch. Please restart 'cupthread auth login'.")
			errCh <- errors.New("oauth state mismatch")
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			fmt.Fprintf(w, "Missing code parameter. Please restart 'cupthread auth login'.")
			errCh <- errors.New("callback missing code")
			return
		}
		fmt.Fprintf(w, "✓ Logged in. You can close this window and return to the terminal.")
		codeCh <- code
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	full := authorizeURL + "?" + q.Encode()
	opened := false
	if openBrowser == nil {
		openBrowser = OpenBrowser
	}
	if err := openBrowser(full); err == nil {
		opened = true
	}
	fmt.Fprintln(os.Stderr, "Open this URL in your browser to log in:")
	fmt.Fprintln(os.Stderr, full)
	if opened {
		fmt.Fprintln(os.Stderr, "(A browser window should have opened.)")
	}

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return nil, err
	case <-time.After(120 * time.Second):
		return nil, errors.New("timed out waiting for browser login (use 'cupthread auth login --device' on headless machines)")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return exchangeCode(ctx, tokenURL, clientID, code, redirectURI, verifier)
}

func exchangeCode(ctx context.Context, tokenURL, clientID, code, redirectURI, verifier string) (*TokenSet, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	return postToken(ctx, tokenURL, form)
}

// Refresh exchanges a refresh token for a fresh token pair (rotating).
func Refresh(ctx context.Context, tokenURL, clientID, refreshToken string) (*TokenSet, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	return postToken(ctx, tokenURL, form)
}

// ─── Device Authorization Grant (RFC 8628) ──────────────────────────────────

type deviceAuthorizeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// DeviceStart is the pending device-flow session shown to the user.
type DeviceStart struct {
	deviceCode     string
	tokenURL       string
	clientID       string
	UserCode       string
	VerificationURI string
	Interval       time.Duration
	ExpiresAt      time.Time
}

// StartDevice begins a device-flow login.
func StartDevice(ctx context.Context, deviceAuthorizeURL, tokenURL, clientID string) (*DeviceStart, error) {
	resp, err := postForm(ctx, deviceAuthorizeURL, url.Values{"client_id": {clientID}})
	if err != nil {
		return nil, err
	}
	var parsed deviceAuthorizeResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return nil, fmt.Errorf("decode device authorization: %w", err)
	}
	if parsed.DeviceCode == "" || parsed.UserCode == "" {
		return nil, errors.New("device authorization endpoint did not return device_code/user_code (not implemented server-side yet?)")
	}
	interval := time.Duration(parsed.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &DeviceStart{
		deviceCode:      parsed.DeviceCode,
		tokenURL:        tokenURL,
		clientID:        clientID,
		UserCode:        parsed.UserCode,
		VerificationURI: parsed.VerificationURI,
		Interval:        interval,
		ExpiresAt:       time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second),
	}, nil
}

// Wait polls until the user confirms, denies, or the code expires. Progress
// is written to stderr.
func (d *DeviceStart) Wait(ctx context.Context) (*TokenSet, error) {
	fmt.Fprintf(os.Stderr, "Waiting for authorization (code %s)...\n", d.UserCode)
	for {
		select {
		case <-time.After(d.Interval):
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Until(d.ExpiresAt)):
			return nil, errors.New("device code expired")
		}

		form := url.Values{
			"client_id":   {d.clientID},
			"device_code": {d.deviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}
		body, err := postForm(ctx, d.tokenURL, form)
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				switch apiErr.Code {
				case "authorization_pending":
					continue
				case "slow_down":
					d.Interval += 5 * time.Second
					continue
				case "access_denied":
					return nil, errors.New("authorization denied")
				case "expired_token":
					return nil, errors.New("device code expired")
				}
			}
			return nil, err
		}
		var set TokenSet
		if err := json.Unmarshal(body, &set); err != nil {
			return nil, fmt.Errorf("decode token response: %w", err)
		}
		return &set, nil
	}
}

// ─── Shared plumbing ─────────────────────────────────────────────────────────

type loopbackListener struct {
	*net.TCPListener
	Port int
}

// listenLoopback binds 127.0.0.1 on an OS-assigned port for the OAuth callback.
func listenLoopback() (*loopbackListener, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	tcp, ok := l.(*net.TCPListener)
	if !ok {
		l.Close()
		return nil, errors.New("listener is not a TCP listener")
	}
	return &loopbackListener{tcp, tcp.Addr().(*net.TCPAddr).Port}, nil
}

// APIError mirrors the OAuth error responses (RFC 6749 §5.2).
type APIError struct {
	Code string
	Body string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("oauth error %s", e.Code)
	}
	return fmt.Sprintf("oauth error: %s", e.Body)
}

func postToken(ctx context.Context, tokenURL string, form url.Values) (*TokenSet, error) {
	body, err := postForm(ctx, tokenURL, form)
	if err != nil {
		return nil, err
	}
	var set TokenSet
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if set.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}
	return &set, nil
}

func postForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var parsed struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		code := ""
		if json.Unmarshal(body, &parsed) == nil {
			code = parsed.Error
		}
		return nil, &APIError{Code: code, Body: strings.TrimSpace(string(body))}
	}
	return body, nil
}

func randomToken(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func s256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
