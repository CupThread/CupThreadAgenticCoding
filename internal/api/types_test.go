package api

import (
	"encoding/json"
	"strings"
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

// meEnvelopeFixture mirrors GET /api/v1/console/me as served by the SaaS
// handler: emailVerified is unconditional (SEC-08), maxWorkspaces is
// additive (BILL-02).
const meEnvelopeFixture = `{
	"clerkUserId": "user_1",
	"email": "dev@example.com",
	"emailVerified": true,
	"workspaces": [{
		"workspace": {"id": "ws_1", "name": "Acme", "slug": "acme",
			"createdAt": "2026-09-01T00:00:00Z", "updatedAt": "2026-09-01T00:00:00Z"},
		"membership": {"id": "mem_1", "workspaceId": "ws_1", "clerkUserId": "user_1",
			"role": "owner", "displayName": null, "email": null,
			"createdAt": "2026-09-01T00:00:00Z", "updatedAt": "2026-09-01T00:00:00Z"},
		"subscription": null
	}],
	"maxWorkspaces": 3
}`

// frListEnvelopeFixture mirrors GET /api/v1/console/workspaces/{id}/feature-requests:
// the handler returns the repository result verbatim, so unassignedTotal is
// always on the wire (QUAL-03).
const frListEnvelopeFixture = `{
	"requests": [{
		"id": "fr_1", "appId": "app_1", "title": "Dark mode",
		"description": "Please", "status": "open", "voteCount": 3,
		"createdAt": "2026-09-01T12:00:00.000Z", "updatedAt": "2026-09-01T12:00:00.000Z"
	}],
	"total": 3,
	"unassignedTotal": 2
}`

// commentsEnvelopeFixture mirrors the comments page shape shared by the
// public thread listing and the Console moderation listing (PROD-31): all
// four keys are always present, nextCursor null on the last page.
const commentsEnvelopeFixture = `{
	"comments": [{
		"id": "cmt_1", "featureRequestId": "fr_1", "appId": "app_1",
		"authorClerkId": null, "authorName": "Lex", "authorAvatarUrl": null,
		"body": "Same issue here", "parentId": null,
		"replyToClerkId": null, "replyToAuthorName": null,
		"isHidden": false, "createdAt": "2026-09-02T08:00:00.000Z"
	}],
	"total": 2,
	"hasMore": true,
	"nextCursor": "2026-09-02T08:00:00.000Z|cmt_1"
}`

// TestAdditiveEnvelopesRoundTripWithoutKeyLoss is the class guard for issue
// #79: typed commands re-marshal decoded structs, so any top-level key the
// server sends that the struct does not model is silently dropped from
// --json/-o yaml and human output. Each fixture is the exact server envelope
// (verified against SaaS origin/main b70bccc, where every handler returns
// the repository result verbatim); the test fails when a key stops
// round-tripping, turning the next additive server field into a test
// failure instead of a silent drop.
func TestAdditiveEnvelopesRoundTripWithoutKeyLoss(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		target  any
		check   func(t *testing.T, decoded any)
	}{
		{
			name:    "me",
			payload: meEnvelopeFixture,
			target:  &MeResponse{},
			check: func(t *testing.T, v any) {
				m := v.(*MeResponse)
				if !m.EmailVerified {
					t.Errorf("EmailVerified = false, want true")
				}
			},
		},
		{
			name:    "feature-requests list",
			payload: frListEnvelopeFixture,
			target:  &AdminListFeatureRequestsResponse{},
			check: func(t *testing.T, v any) {
				r := v.(*AdminListFeatureRequestsResponse)
				if r.Total != 3 || r.UnassignedTotal != 2 {
					t.Errorf("totals = (%d, %d), want (3, 2)", r.Total, r.UnassignedTotal)
				}
			},
		},
		{
			name:    "comments list",
			payload: commentsEnvelopeFixture,
			target:  &ListCommentsResponse{},
			check: func(t *testing.T, v any) {
				r := v.(*ListCommentsResponse)
				if r.Total != 2 || !r.HasMore || r.NextCursor == nil || *r.NextCursor != "2026-09-02T08:00:00.000Z|cmt_1" {
					t.Errorf("page = (%d, %v, %v), want (2, true, cursor)", r.Total, r.HasMore, r.NextCursor)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tt.payload), &sent); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			if err := json.Unmarshal([]byte(tt.payload), tt.target); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			out, err := json.Marshal(tt.target)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			for key := range sent {
				if _, ok := got[key]; !ok {
					t.Errorf("top-level key %q is sent by the server but dropped by %T: add the field to the struct and extend the fixture", key, tt.target)
				}
			}
			if tt.check != nil {
				tt.check(t, tt.target)
			}
		})
	}
}

// TestListCommentsResponseNullCursor pins the last-page encoding: the server
// always sends nextCursor, as JSON null when there is nothing after this
// page, and the pointer must stay nil so --json re-emits null.
func TestListCommentsResponseNullCursor(t *testing.T) {
	var resp ListCommentsResponse
	const body = `{"comments":[],"total":0,"hasMore":false,"nextCursor":null}`
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.NextCursor != nil {
		t.Errorf("NextCursor = %v, want nil for explicit null", resp.NextCursor)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), `"nextCursor":null`) {
		t.Errorf("re-marshaled output %s drops nextCursor null", out)
	}
}
