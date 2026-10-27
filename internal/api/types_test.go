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
	"allowAnonymousChangelog": false,
	"requireAnonymousVoteTurnstile": true,
	"allowedEmbedOrigins": ["https://docs.example.com", "https://app.partner.com:8443"]
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
	if !cfg.RequireAnonymousVoteTurnstile {
		t.Errorf("RequireAnonymousVoteTurnstile = false, want the SEC-846 gate from the full-schema payload")
	}
	if len(cfg.AllowedEmbedOrigins) != 2 ||
		cfg.AllowedEmbedOrigins[0] != "https://docs.example.com" ||
		cfg.AllowedEmbedOrigins[1] != "https://app.partner.com:8443" {
		t.Errorf("AllowedEmbedOrigins = %v, want the two configured origins", cfg.AllowedEmbedOrigins)
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
	if cfg.RequireAnonymousVoteTurnstile {
		t.Errorf("RequireAnonymousVoteTurnstile = true, want default false when the server omits the field")
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

// TestPublicAppConfigEmbedOriginsPresence pins the allowedEmbedOrigins
// presence semantics (issue #146, SEC-518): the field is present only when
// the tenant configured at least one embed origin — never an empty array —
// and omitempty keeps a decoded config re-marshaling without the key when it
// was absent, so --json/-o yaml output stays byte-faithful to the server.
func TestPublicAppConfigEmbedOriginsPresence(t *testing.T) {
	var withOrigins PublicAppConfig
	const withBody = `{"appId":"app_1","appKey":"key_live_1","allowedEmbedOrigins":["https://docs.example.com"]}`
	if err := json.Unmarshal([]byte(withBody), &withOrigins); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := json.Marshal(&withOrigins)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), `"allowedEmbedOrigins":["https://docs.example.com"]`) {
		t.Errorf("re-marshaled config %s drops allowedEmbedOrigins", out)
	}

	var withoutOrigins PublicAppConfig
	const withoutBody = `{"appId":"app_2","appKey":"key_live_2"}`
	if err := json.Unmarshal([]byte(withoutBody), &withoutOrigins); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if withoutOrigins.AllowedEmbedOrigins != nil {
		t.Errorf("AllowedEmbedOrigins = %v, want nil when the server omits the field", withoutOrigins.AllowedEmbedOrigins)
	}
	out, err = json.Marshal(&withoutOrigins)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(out), "allowedEmbedOrigins") {
		t.Errorf("re-marshaled config %s invents allowedEmbedOrigins for an app without embed origins", out)
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
// always on the wire (QUAL-03) and importedVotes rides along on the record
// (issue #131; verified against SaaS origin/main de89c95, where the console
// mapper sends `importedVotes: row.imported_votes ?? 0`). The record's
// voteCount already includes the imported portion — 3 total with 2 imported
// — so decoding must never treat the two as additive.
const frListEnvelopeFixture = `{
	"requests": [{
		"id": "fr_1", "appId": "app_1", "title": "Dark mode",
		"description": "Please", "status": "open", "voteCount": 3,
		"importedVotes": 2,
		"createdAt": "2026-09-01T12:00:00.000Z", "updatedAt": "2026-09-01T12:00:00.000Z"
	}],
	"total": 3,
	"unassignedTotal": 2
}`

// commentsEnvelopeFixture mirrors the comments page shape shared by the
// public thread listing and the Console moderation listing (PROD-31): all
// four envelope keys are always present, nextCursor null on the last page,
// and each comment row carries the complete 13-key served record — verified
// against SaaS origin/main mapCommentRow, where both listing queries select
// every column and the mapper always emits all keys (updatedAt falling back
// to createdAt). The two rows exercise both authorClerkId branches: an
// anonymous author (null) and a signed-in author (the app-scoped u_* public
// pseudonym).
const commentsEnvelopeFixture = `{
	"comments": [{
		"id": "cmt_1", "featureRequestId": "fr_1", "appId": "app_1",
		"authorClerkId": null, "authorName": "Lex", "authorAvatarUrl": null,
		"body": "Same issue here", "parentId": null,
		"replyToClerkId": null, "replyToAuthorName": null,
		"isHidden": false, "createdAt": "2026-09-02T08:00:00.000Z",
		"updatedAt": "2026-09-02T08:00:00.000Z"
	}, {
		"id": "cmt_2", "featureRequestId": "fr_1", "appId": "app_1",
		"authorClerkId": "u_4f9a2b", "authorName": "Kim", "authorAvatarUrl": null,
		"body": "Fixed it locally", "parentId": "cmt_1",
		"replyToClerkId": null, "replyToAuthorName": "Lex",
		"isHidden": false, "createdAt": "2026-09-02T09:30:00.000Z",
		"updatedAt": "2026-09-02T09:31:00.000Z"
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
// failure instead of a silent drop. Since issue #276 the guard also walks
// one level deeper: every top-level array of objects (comment rows, request
// rows, workspace entries) gets the same sent ⊆ re-marshaled comparison per
// item, because the envelope-level check cannot see inside list items.
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
				req := r.Requests[0]
				if req.VoteCount != 3 || req.ImportedVotes == nil || *req.ImportedVotes != 2 {
					t.Errorf("votes = (%d, %v), want (3, 2) with importedVotes decoded", req.VoteCount, req.ImportedVotes)
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
				if len(r.Comments) != 2 {
					t.Fatalf("comments = %d rows, want the fixture's 2", len(r.Comments))
				}
				anonymous, signedIn := r.Comments[0], r.Comments[1]
				if anonymous.AuthorClerkID != nil {
					t.Errorf("anonymous AuthorClerkID = %v, want nil for the explicit null", anonymous.AuthorClerkID)
				}
				if signedIn.AuthorClerkID == nil || *signedIn.AuthorClerkID != "u_4f9a2b" {
					t.Errorf("signed-in AuthorClerkID = %v, want u_4f9a2b", signedIn.AuthorClerkID)
				}
				for i, c := range r.Comments {
					if c.AppID == nil || *c.AppID != "app_1" {
						t.Errorf("comment %d AppID = %v, want app_1", i, c.AppID)
					}
					if c.UpdatedAt == "" {
						t.Errorf("comment %d UpdatedAt = %q, want the served value", i, c.UpdatedAt)
					}
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
			// One level deeper (issue #276): the top-level check cannot see
			// inside list items, so walk every top-level array of objects
			// and require each item's served keys to survive re-marshaling.
			for key, raw := range sent {
				var sentItems []map[string]json.RawMessage
				if err := json.Unmarshal(raw, &sentItems); err != nil || len(sentItems) == 0 {
					continue // not a non-empty array of objects: nothing to walk
				}
				gotRaw, ok := got[key]
				if !ok {
					continue // already reported by the top-level check
				}
				var gotItems []map[string]json.RawMessage
				if err := json.Unmarshal(gotRaw, &gotItems); err != nil {
					t.Errorf("re-marshaled %q = %s is not an array of objects", key, gotRaw)
					continue
				}
				if len(gotItems) != len(sentItems) {
					t.Errorf("re-marshaled %q has %d items, want the served %d", key, len(gotItems), len(sentItems))
					continue
				}
				for i, sentItem := range sentItems {
					for itemKey := range sentItem {
						if _, ok := gotItems[i][itemKey]; !ok {
							t.Errorf("nested item %s[%d] key %q is sent by the server but dropped by %T: add the field to the item struct and extend the fixture", key, i, itemKey, tt.target)
						}
					}
				}
			}
			if tt.check != nil {
				tt.check(t, tt.target)
			}
		})
	}
}

// TestCommentsEnvelopeItemKeySetsMatchServer extends the #79 guard one level
// deeper for the one fixture that carries the complete served row shape
// (issue #276; verified against SaaS origin/main mapCommentRow, shared by the
// public thread listing and the Console moderation listing). Each comment
// item's re-marshaled key set must EQUAL the served set: the sent ⊆ got
// direction fails when a server key stops being modeled, and got ⊆ sent
// fails when the struct invents a key the server never serves — the phantom
// authorEmail this issue removed must stay gone.
func TestCommentsEnvelopeItemKeySetsMatchServer(t *testing.T) {
	var sent struct {
		Comments []json.RawMessage `json:"comments"`
	}
	if err := json.Unmarshal([]byte(commentsEnvelopeFixture), &sent); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var resp ListCommentsResponse
	if err := json.Unmarshal([]byte(commentsEnvelopeFixture), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(sent.Comments) != len(resp.Comments) || len(sent.Comments) == 0 {
		t.Fatalf("decoded %d of %d comment rows", len(resp.Comments), len(sent.Comments))
	}
	for i, raw := range sent.Comments {
		var sentKeys map[string]json.RawMessage
		if err := json.Unmarshal(raw, &sentKeys); err != nil {
			t.Fatalf("comment %d: %v", i, err)
		}
		out, err := json.Marshal(resp.Comments[i])
		if err != nil {
			t.Fatalf("marshal comment %d: %v", i, err)
		}
		var gotKeys map[string]json.RawMessage
		if err := json.Unmarshal(out, &gotKeys); err != nil {
			t.Fatalf("re-marshal comment %d: %v", i, err)
		}
		for key := range sentKeys {
			if _, ok := gotKeys[key]; !ok {
				t.Errorf("comment %d key %q is sent by the server but dropped by FeatureRequestComment: add the field and extend the fixture", i, key)
			}
		}
		for key := range gotKeys {
			if _, ok := sentKeys[key]; !ok {
				t.Errorf("re-marshaled comment %d invents key %q the server never serves: remove the field from FeatureRequestComment", i, key)
			}
		}
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

// TestFeatureRequestImportedVotes pins the summed voteCount contract from
// issue #131 (SaaS PR #395): voteCount on feature-request records is the
// displayed total — in-app votes plus the historical votes imported from the
// source provider — and importedVotes carries only that historical portion,
// already included in voteCount (never additive; 124 total with 120 imported
// means 4 votes were cast in CupThread). The field is optional: requests
// created in CupThread omit it on the wire, and both records must survive
// that omission with a nil pointer while re-emitting the field verbatim when
// present, so typed --json output keeps the server envelope.
func TestFeatureRequestImportedVotes(t *testing.T) {
	const publicImported = `{
		"id": "fr_pub_1", "title": "Dark mode", "description": "Please",
		"status": "open", "voteCount": 124, "importedVotes": 120,
		"hasVoted": false, "commentCount": 0,
		"createdAt": "2026-09-01T12:00:00.000Z", "updatedAt": "2026-09-01T12:00:00.000Z"
	}`
	const publicNative = `{
		"id": "fr_pub_2", "title": "Linux build", "description": "Ship it",
		"status": "open", "voteCount": 4,
		"hasVoted": false, "commentCount": 1,
		"createdAt": "2026-09-02T12:00:00.000Z", "updatedAt": "2026-09-02T12:00:00.000Z"
	}`
	const adminImported = `{
		"id": "fr_adm_1", "appId": "app_1", "title": "Dark mode",
		"description": "Please", "status": "open", "voteCount": 124,
		"importedVotes": 120, "createdByAdmin": false,
		"createdAt": "2026-09-01T12:00:00.000Z", "updatedAt": "2026-09-01T12:00:00.000Z"
	}`
	const adminNative = `{
		"id": "fr_adm_2", "appId": "app_1", "title": "Linux build",
		"description": "Ship it", "status": "open", "voteCount": 4,
		"createdByAdmin": true,
		"createdAt": "2026-09-02T12:00:00.000Z", "updatedAt": "2026-09-02T12:00:00.000Z"
	}`

	t.Run("public record with imported votes", func(t *testing.T) {
		var r PublicFeatureRequest
		if err := json.Unmarshal([]byte(publicImported), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if r.VoteCount != 124 {
			t.Errorf("VoteCount = %d, want the displayed total 124", r.VoteCount)
		}
		if r.ImportedVotes == nil || *r.ImportedVotes != 120 {
			t.Fatalf("ImportedVotes = %v, want 120", r.ImportedVotes)
		}
		out, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if !strings.Contains(string(out), `"importedVotes":120`) {
			t.Errorf("re-marshaled output %s drops importedVotes", out)
		}
	})

	t.Run("public record created in CupThread", func(t *testing.T) {
		var r PublicFeatureRequest
		if err := json.Unmarshal([]byte(publicNative), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if r.ImportedVotes != nil {
			t.Errorf("ImportedVotes = %v, want nil when the server omits it", r.ImportedVotes)
		}
		out, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if strings.Contains(string(out), "importedVotes") {
			t.Errorf("re-marshaled output %s invents importedVotes for a record without one", out)
		}
	})

	t.Run("console record with imported votes", func(t *testing.T) {
		var r AdminFeatureRequest
		if err := json.Unmarshal([]byte(adminImported), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if r.VoteCount != 124 {
			t.Errorf("VoteCount = %d, want the displayed total 124", r.VoteCount)
		}
		if r.ImportedVotes == nil || *r.ImportedVotes != 120 {
			t.Fatalf("ImportedVotes = %v, want 120", r.ImportedVotes)
		}
		out, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if !strings.Contains(string(out), `"importedVotes":120`) {
			t.Errorf("re-marshaled output %s drops importedVotes", out)
		}
	})

	t.Run("console record created in CupThread", func(t *testing.T) {
		var r AdminFeatureRequest
		if err := json.Unmarshal([]byte(adminNative), &r); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if r.ImportedVotes != nil {
			t.Errorf("ImportedVotes = %v, want nil when the server omits it", r.ImportedVotes)
		}
		out, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if strings.Contains(string(out), "importedVotes") {
			t.Errorf("re-marshaled output %s invents importedVotes for a record without one", out)
		}
	})
}
