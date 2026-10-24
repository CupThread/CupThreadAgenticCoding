package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// capturedRequest records what a redirect target actually received, so a
// test can prove the secret-bearing hop never happened.
type capturedRequest struct {
	method string
	body   string
	token  string
	appKey string
	wsID   string
}

// redirectFixture starts two servers: an origin that answers every request
// with the given 3xx pointing at the target, and the target itself, which
// records whatever arrives. The 3xx classes Go's default policy would follow
// all funnel here, so each test asserts on the target's state directly.
func redirectFixture(t *testing.T, status int) (origin *httptest.Server, hits *int, captured *capturedRequest) {
	t.Helper()

	captured = &capturedRequest{}
	hits = new(int)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		body, _ := io.ReadAll(r.Body)
		captured.method = r.Method
		captured.body = string(body)
		captured.token = r.Header.Get("X-User-Token")
		captured.appKey = r.Header.Get("X-App-Key")
		captured.wsID = r.Header.Get("X-Workspace-Id")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/harvest", status)
	}))
	t.Cleanup(origin.Close)
	return origin, hits, captured
}

// newCredentialClient builds a client whose every request carries the
// workspace/app/user credentials exactly as production does.
func newCredentialClient(baseURL string) *Client {
	client := New(baseURL)
	client.WorkspaceID = "ws_123"
	client.AppKey = "APPKEY123"
	client.UserToken = "usertoken-123"
	return client
}

// requireZeroRedirectHops fails the test when the Location target saw any
// traffic: the whole point of the policy is that the hop never happens.
func requireZeroRedirectHops(t *testing.T, hits *int, captured *capturedRequest) {
	t.Helper()
	if *hits != 0 {
		t.Fatalf("redirect target received %d request(s): method=%q body=%q X-User-Token=%q X-App-Key=%q X-Workspace-Id=%q; want 0 hops",
			*hits, captured.method, captured.body, captured.token, captured.appKey, captured.wsID)
	}
}

// requireRedirectRefusedAsAPIError pins the Do-path contract of issue #294:
// the 3xx itself comes back as a non-2xx APIError through the regular
// decode path instead of being chased.
func requireRedirectRefusedAsAPIError(t *testing.T, err error, status int) {
	t.Helper()
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError with Status %d", err, status)
	}
	if apiErr.Status != status {
		t.Errorf("APIError.Status = %d, want %d", apiErr.Status, status)
	}
}

// TestDoNeverFollowsRedirects covers every 3xx class Go would otherwise
// follow: a redirecting API origin must surface its status as an APIError
// and the Location target must never see the request or its credentials.
func TestDoNeverFollowsRedirects(t *testing.T) {
	for _, status := range []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			origin, hits, captured := redirectFixture(t, status)
			client := newCredentialClient(origin.URL)

			err := client.Do(context.Background(), "GET", "/api/v1/console/me", nil, nil, nil)
			requireRedirectRefusedAsAPIError(t, err, status)
			requireZeroRedirectHops(t, hits, captured)
		})
	}
}

// TestDoWithHeadersNeverFollowsRedirects covers the extra-headers path
// (comments list) whose custom X-App-Key/X-User-Token headers are exactly
// what Go's default redirect copier forwards cross-host.
func TestDoWithHeadersNeverFollowsRedirects(t *testing.T) {
	origin, hits, captured := redirectFixture(t, http.StatusFound)
	client := newCredentialClient(origin.URL)

	err := client.DoWithHeaders(context.Background(), "GET", "/api/v1/comments", nil,
		map[string]string{"X-Trace-Id": "trace-1"}, nil, nil)
	requireRedirectRefusedAsAPIError(t, err, http.StatusFound)
	requireZeroRedirectHops(t, hits, captured)
}

// TestBodyBearingPostNeverReplaysOn307And308 pins the worst leak class:
// 307/308 preserve the method and body, so the default policy would replay
// a mutation's full JSON payload to the redirect target.
func TestBodyBearingPostNeverReplaysOn307And308(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			origin, hits, captured := redirectFixture(t, status)
			client := newCredentialClient(origin.URL)

			err := client.Do(context.Background(), "POST", "/api/v1/changelog", nil,
				map[string]string{"secret": "body-value"}, nil)
			requireRedirectRefusedAsAPIError(t, err, status)
			requireZeroRedirectHops(t, hits, captured)
			if captured.body != "" {
				t.Errorf("redirect target captured body %q; want nothing", captured.body)
			}
		})
	}
}
