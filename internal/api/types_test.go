package api

import (
	"encoding/json"
	"testing"
)

const publicAppConfigFixture = `{
	"appId": "app_1",
	"appKey": "key_live_1",
	"workspaceSlug": "acme",
	"slug": "ios",
	"name": "Acme iOS",
	"storeUrl": "https://apps.apple.com/app/acme",
	"storeKind": "app_store",
	"appStoreUrl": "https://apps.apple.com/app/acme",
	"googlePlayUrl": null,
	"websiteUrl": "https://acme.example.com",
	"iconUrl": "https://img.cupthread.com/acme.png",
	"allowPublic": true,
	"hideSiteBranding": true,
	"allowedPlatforms": ["ios", "universal"],
	"maxAttachmentBytes": 10485760,
	"allowAnonymousRoadmap": true,
	"allowAnonymousVote": true,
	"allowAnonymousFeedback": true,
	"allowAnonymousChangelog": false
}`

// TestPublicAppConfigDecodesFullSchema mirrors the OpenAPI PublicAppConfig
// schema, with emphasis on the websiteUrl and hideSiteBranding fields added
// by issue #2.
func TestPublicAppConfigDecodesFullSchema(t *testing.T) {
	var cfg PublicAppConfig
	if err := json.Unmarshal([]byte(publicAppConfigFixture), &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.AppID != "app_1" || cfg.AppKey != "key_live_1" || cfg.WorkspaceSlug != "acme" {
		t.Errorf("identity fields = %+v", cfg)
	}
	if cfg.WebsiteURL == nil || *cfg.WebsiteURL != "https://acme.example.com" {
		t.Errorf("WebsiteURL = %v, want https://acme.example.com", cfg.WebsiteURL)
	}
	if !cfg.HideSiteBranding {
		t.Errorf("HideSiteBranding = false, want true")
	}
	if cfg.AppStoreURL == nil || cfg.GooglePlayURL != nil {
		t.Errorf("store links = appStore %v, googlePlay %v", cfg.AppStoreURL, cfg.GooglePlayURL)
	}
	if len(cfg.AllowedPlatforms) != 2 || cfg.AllowedPlatforms[0] != "ios" {
		t.Errorf("AllowedPlatforms = %v", cfg.AllowedPlatforms)
	}
	if cfg.MaxAttachmentBytes != 10485760 {
		t.Errorf("MaxAttachmentBytes = %d", cfg.MaxAttachmentBytes)
	}
	if !cfg.AllowAnonymousVote || cfg.AllowAnonymousChangelog {
		t.Errorf("anonymous flags = %+v", cfg)
	}
}

// TestPublicAppConfigMinimalPayload verifies the nullable and default
// behavior when the server omits optional fields: websiteUrl is null (not "")
// and hideSiteBranding defaults to false.
func TestPublicAppConfigMinimalPayload(t *testing.T) {
	var cfg PublicAppConfig
	const body = `{"appId":"app_2","appKey":"key_live_2","workspaceSlug":"ws","slug":"web","name":"Web"}`
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.WebsiteURL != nil {
		t.Errorf("WebsiteURL = %v, want nil", cfg.WebsiteURL)
	}
	if cfg.HideSiteBranding {
		t.Errorf("HideSiteBranding = true, want default false")
	}
}

// TestPublicAppConfigWebsiteURLEncodesNull checks that an explicit JSON null
// for websiteUrl decodes to a nil pointer, matching the server's
// `string | null` contract.
func TestPublicAppConfigWebsiteURLEncodesNull(t *testing.T) {
	var cfg PublicAppConfig
	const body = `{"appId":"app_3","appKey":"key_live_3","websiteUrl":null,"hideSiteBranding":false}`
	if err := json.Unmarshal([]byte(body), &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.WebsiteURL != nil {
		t.Errorf("WebsiteURL = %v, want nil for explicit null", cfg.WebsiteURL)
	}
}
