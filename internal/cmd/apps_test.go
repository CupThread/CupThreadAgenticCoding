package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// publicConfigFixture is a PublicAppConfig payload exercising the fields from
// issue #2 (websiteUrl, hideSiteBranding).
const publicConfigFixture = `{
	"appId": "app_1",
	"appKey": "key_live_1",
	"workspaceSlug": "acme",
	"slug": "ios",
	"name": "Acme iOS",
	"storeUrl": null,
	"storeKind": null,
	"appStoreUrl": null,
	"googlePlayUrl": null,
	"websiteUrl": "https://acme.example.com",
	"iconUrl": null,
	"allowPublic": true,
	"hideSiteBranding": true,
	"allowedPlatforms": ["ios", "universal"],
	"maxAttachmentBytes": 10485760
}`

// runRoot executes the CLI against serverURL with a throwaway config and
// returns everything the command printed to stdout.
func runRoot(t *testing.T, serverURL string, args ...string) (string, error) {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	root := newRootCmd()
	full := append(append([]string{}, args...), "--base-url", serverURL, "--config", filepath.Join(t.TempDir(), "config.json"))
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

// TestAppsPublicConfigByAppKey covers the app-key variant of the public
// config endpoint and verifies the new websiteUrl/hideSiteBranding fields
// surface in table output.
func TestAppsPublicConfigByAppKey(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(publicConfigFixture))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "apps", "public-config", "key_live_1")
	if err != nil {
		t.Fatalf("public-config: %v", err)
	}
	if gotPath != "/api/v1/public/config/key_live_1" {
		t.Errorf("request path = %s", gotPath)
	}
	for _, want := range []string{"Website URL", "https://acme.example.com", "Hide site branding", "yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestAppsPublicConfigBySlugs covers the workspace/app slug variant.
func TestAppsPublicConfigBySlugs(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(publicConfigFixture))
	}))
	defer server.Close()

	if _, err := runRoot(t, server.URL, "apps", "public-config",
		"--workspace-slug", "acme", "--app-slug", "ios"); err != nil {
		t.Fatalf("public-config: %v", err)
	}
	if gotPath != "/api/v1/public/workspaces/acme/apps/ios/config" {
		t.Errorf("request path = %s", gotPath)
	}
}

// TestAppsPublicConfigJSON verifies machine-readable output keeps the raw
// field names from the API contract.
func TestAppsPublicConfigJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(publicConfigFixture))
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "apps", "public-config", "key_live_1", "--json")
	if err != nil {
		t.Fatalf("public-config: %v", err)
	}
	for _, want := range []string{`"websiteUrl": "https://acme.example.com"`, `"hideSiteBranding": true`} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON output missing %q:\n%s", want, out)
		}
	}
}

// TestAppsPublicConfigPrivateApp404 covers issue #33 (SEC-37): private apps
// fail closed with the same 404 {"error": "App not found"} as unknown app
// keys on both config routes, and the CLI interprets that 404 as
// "not found or not public" instead of a 200 body with allowPublic: false.
func TestAppsPublicConfigPrivateApp404(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error": "App not found"}`))
	}))
	defer server.Close()

	cases := []struct {
		name     string
		args     []string
		wantPath string
	}{
		{"by app key", []string{"apps", "public-config", "key_live_private"}, "/api/v1/public/config/key_live_private"},
		{"by slugs", []string{"apps", "public-config", "--workspace-slug", "acme", "--app-slug", "ios"}, "/api/v1/public/workspaces/acme/apps/ios/config"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runRoot(t, server.URL, tc.args...)
			if err == nil {
				t.Fatal("private app config should fail, got nil error")
			}
			if gotPath != tc.wantPath {
				t.Errorf("request path = %s, want %s", gotPath, tc.wantPath)
			}
			for _, want := range []string{"not found or not public", "HTTP 404"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
			if strings.Contains(out, "Acme iOS") {
				t.Errorf("no config table should print on 404, got:\n%s", out)
			}
		})
	}
}

// TestAppsPublicConfigOtherErrorsNotRelabeled keeps the fail-closed 404
// interpretation scoped to 404: other statuses surface the raw API error
// untouched.
func TestAppsPublicConfigOtherErrorsNotRelabeled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error": "Public feature requests are disabled for this app"}`))
	}))
	defer server.Close()

	_, err := runRoot(t, server.URL, "apps", "public-config", "key_live_private")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "not found or not public") {
		t.Errorf("403 should not be relabeled as not-found/not-public: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("error = %v, want HTTP 403", err)
	}
}

// TestAppsPublicConfigRequiresSelector verifies argument validation: exactly
// one of (positional app key) or (both slug flags) must be given.
func TestAppsPublicConfigRequiresSelector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should be made for invalid arguments")
	}))
	defer server.Close()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no arguments", []string{"apps", "public-config"}, "pass an app key"},
		{"key and slugs mixed", []string{"apps", "public-config", "key_live_1", "--workspace-slug", "acme", "--app-slug", "ios"}, "not both"},
		{"only workspace slug", []string{"apps", "public-config", "--workspace-slug", "acme"}, "pass an app key"},
		{"only app slug", []string{"apps", "public-config", "--app-slug", "ios"}, "pass an app key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runRoot(t, server.URL, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestAppsPublicConfigMissingAppKeepsPathEscaped checks that user input is
// URL-escaped when building the request path and that API 404s surface.
func TestAppsPublicConfigMissingAppKeepsPathEscaped(t *testing.T) {
	var gotRequestURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestURI = r.RequestURI
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"App not found"}`))
	}))
	defer server.Close()

	_, err := runRoot(t, server.URL, "apps", "public-config", "weird/key with spaces")
	if err == nil || !strings.Contains(err.Error(), "App not found") {
		t.Fatalf("error = %v, want API 404 message", err)
	}
	if gotRequestURI != "/api/v1/public/config/weird%2Fkey%20with%20spaces" {
		t.Errorf("request target = %q", gotRequestURI)
	}
}

// appListFixture answers lookupApp's GET /apps for the update-icon tests.
const appListFixture = `{"apps":[{"appId":"app_1","appKey":"key_live_1","slug":"ios","name":"Acme iOS","allowPublic":true,"allowedPlatforms":["ios"],"maxAttachmentBytes":10485760}],"total":1}`

// TestAppsUpdateIconUploadsToConsoleEndpoint verifies `apps update --icon`
// uploads via the console app-icon endpoint (not the public feedback image
// endpoint), declares the part content type from the filename, skips the
// follow-up metadata PUT when only --icon changed, and prints the icon URL.
func TestAppsUpdateIconUploadsToConsoleEndpoint(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	iconPath := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(iconPath, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotIconPath, gotIconMethod, gotPartType string
	var gotPUT int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/console/workspaces/ws_1/apps":
			_, _ = w.Write([]byte(appListFixture))
		case r.Method == http.MethodPost &&
			r.URL.Path == "/api/v1/console/workspaces/ws_1/apps/app_1/icon":
			gotIconMethod, gotIconPath = r.Method, r.URL.Path
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Errorf("file field: %v", err)
				return
			}
			defer file.Close()
			gotPartType = header.Header.Get("Content-Type")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"appId":"app_1","name":"Acme iOS","iconUrl":"https://cdn.example.com/icon.png"}`))
		default:
			if r.Method == http.MethodPut {
				gotPUT++
			}
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	out, err := runRoot(t, server.URL, "apps", "update", "app_1", "--icon", iconPath, "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("apps update --icon: %v", err)
	}
	if gotIconMethod != http.MethodPost ||
		gotIconPath != "/api/v1/console/workspaces/ws_1/apps/app_1/icon" {
		t.Errorf("icon request = %s %s", gotIconMethod, gotIconPath)
	}
	if gotPartType != "image/png" {
		t.Errorf("part content-type = %q, want image/png", gotPartType)
	}
	if gotPUT != 0 {
		t.Errorf("metadata PUT count = %d, want 0 (icon endpoint updates the record)", gotPUT)
	}
	for _, want := range []string{"Updated app app_1", "https://cdn.example.com/icon.png"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestAppsUpdateIcon415SurfacesUnsupportedType verifies the CLI maps a 415
// from the upload to the actionable "unsupported image type" failure instead
// of a generic error.
func TestAppsUpdateIcon415SurfacesUnsupportedType(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	iconPath := filepath.Join(t.TempDir(), "logo.svg")
	if err := os.WriteFile(iconPath, []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/console/workspaces/ws_1/apps":
			_, _ = w.Write([]byte(appListFixture))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/console/workspaces/ws_1/apps/app_1/icon":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnsupportedMediaType)
			_, _ = w.Write([]byte(`{"error":"SVG images are not supported. Upload a PNG, JPEG, WebP, or GIF image."}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	_, err := runRoot(t, server.URL, "apps", "update", "app_1", "--icon", iconPath, "--workspace", "ws_1")
	if err == nil {
		t.Fatal("apps update --icon: want error")
	}
	for _, want := range []string{"unsupported image type", "SVG images are not supported", "415"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// TestAppsUpdateIconClearStillUsesPUT verifies `--icon ""` keeps the old
// clearing semantics (iconUrl: null via the metadata PUT, no upload).
func TestAppsUpdateIconClearStillUsesPUT(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	var gotPUTBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/console/workspaces/ws_1/apps":
			_, _ = w.Write([]byte(appListFixture))
		case r.Method == http.MethodPut &&
			r.URL.Path == "/api/v1/console/workspaces/ws_1/apps/app_1":
			_ = json.NewDecoder(r.Body).Decode(&gotPUTBody)
			_, _ = w.Write([]byte(`{"appId":"app_1","name":"Acme iOS"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	if _, err := runRoot(t, server.URL, "apps", "update", "app_1", "--icon", "", "--workspace", "ws_1"); err != nil {
		t.Fatalf("apps update --icon '': %v", err)
	}
	if gotPUTBody["iconUrl"] != nil {
		t.Errorf("PUT body = %v, want iconUrl null", gotPUTBody)
	}
}
