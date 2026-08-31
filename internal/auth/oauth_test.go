package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestS256Challenge(t *testing.T) {
	// RFC 7636 appendix B test vector.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := s256Challenge(verifier); got != want {
		t.Errorf("s256Challenge = %q, want %q", got, want)
	}
}

func TestEndpoints(t *testing.T) {
	base := "https://api.cupthread.com/"
	authorize, token, deviceAuthorize, deviceToken := Endpoints(base)
	for _, ep := range []string{authorize, token, deviceAuthorize, deviceToken} {
		if !strings.HasPrefix(ep, "https://api.cupthread.com/api/v1/oauth") {
			t.Errorf("endpoint %q does not hang off the base URL", ep)
		}
	}
	if authorize != "https://api.cupthread.com"+AuthorizePath {
		t.Errorf("authorize = %q", authorize)
	}
}

func TestRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("refresh_token") != "cpr_old" {
			t.Errorf("refresh_token = %q", r.Form.Get("refresh_token"))
		}
		_, _ = w.Write([]byte(`{"access_token":"cpt_new","refresh_token":"cpr_rotated","token_type":"Bearer","expires_in":1209600,"scope":"full"}`))
	}))
	defer server.Close()

	set, err := Refresh(context.Background(), server.URL, "cupthread-cli", "cpr_old")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if set.AccessToken != "cpt_new" || set.RefreshToken != "cpr_rotated" || set.ExpiresIn != 1209600 {
		t.Errorf("set = %+v", set)
	}
}

func TestPostTokenSurfacesOAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code expired"}`))
	}))
	defer server.Close()

	_, err := postToken(context.Background(), server.URL, url.Values{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_grant" {
		t.Fatalf("expected APIError invalid_grant, got %v", err)
	}
}

func TestStartDeviceRejectsMissingFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"unsupported_grant_type"}`))
	}))
	defer server.Close()

	if _, err := StartDevice(context.Background(), server.URL, server.URL, "cupthread-cli"); err == nil {
		t.Fatal("expected error when device_code/user_code missing")
	}
}
