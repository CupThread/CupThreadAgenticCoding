package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestSDKSkillSigningGuidance guards the per-platform payment-attribute
// signing guidance in the four SDK SKILL.md files: the Swift and Flutter SDKs
// sign automatically once a signing secret is configured, while the Android
// and React Native SDKs cannot sign at all and must warn that every call
// carrying payment attributes returns 422 payment_attributes_require_signature
// until their mirror issues land. The markers pin the SDK-native status blocks
// so a future rewrite cannot reintroduce hand-rolled-HMAC-for-everyone
// guidance, and that the shared canonical-string reference survives.
func TestSDKSkillSigningGuidance(t *testing.T) {
	common := []string{
		"## Payment-Attribute Signing (HMAC-SHA256, DATA-03)",
		"cpt-user-attrs-v1",
	}
	cases := []struct {
		skill string
		want  []string
	}{
		{
			skill: "cupthread-swift-sdk",
			want: []string{
				"the Swift SDK signs automatically",
				"signingSecret",
				"UserAttributesSigner",
			},
		},
		{
			skill: "cupthread-flutter-sdk",
			want: []string{
				"the Flutter SDK signs automatically",
				"sdkSigningSecret",
				"precomputed pair",
			},
		},
		{
			skill: "cupthread-android-sdk",
			want: []string{
				"cannot sign payment attributes",
				"payment_attributes_require_signature",
				"CupThreadAndroidSDK#28",
				"workaround",
			},
		},
		{
			skill: "cupthread-react-native-sdk",
			want: []string{
				"cannot sign payment attributes",
				"payment_attributes_require_signature",
				"CupThreadReactNativeSDK#86",
				"workaround",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.skill, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "skills", tc.skill, "SKILL.md"))
			if err != nil {
				t.Fatalf("read skill doc: %v", err)
			}
			doc := string(data)
			for _, marker := range append(common, tc.want...) {
				if !strings.Contains(doc, marker) {
					t.Errorf("%s/SKILL.md is missing required signing-guidance marker %q", tc.skill, marker)
				}
			}
		})
	}
}

// TestAPISkillInteractiveOnlyCapabilitySet guards against Fact-2-class drift
// (issue #61): the AUTH-01 interactive-only enumeration in
// cupthread-api/SKILL.md must name every capability in the SaaS upstream
// INTERACTIVE_SESSION_CAPABILITIES set (apps/api/src/lib/capabilities.ts).
// When SaaS adds or removes one, update this snapshot and the SKILL.md in the
// same sync — the enumeration below is that snapshot.
func TestAPISkillInteractiveOnlyCapabilitySet(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "skills", "cupthread-api", "SKILL.md"))
	if err != nil {
		t.Fatalf("read skill doc: %v", err)
	}
	doc := string(data)

	interactiveOnlySet := "(`changelog.publish`, `members.manage`, `billing.manage`, `integration.manage`, `privacy.manage`, `workspace.delete`)"
	if !strings.Contains(doc, interactiveOnlySet) {
		t.Errorf("cupthread-api/SKILL.md interactive-only set drifted from SaaS INTERACTIVE_SESSION_CAPABILITIES; want %s", interactiveOnlySet)
	}

	// privacy.manage was the capability missing at the time of issue #61; pin
	// its matrix row (admin/owner only, interactive sessions required) too.
	if !strings.Contains(doc, "| `privacy.manage` (end-user erase/anonymize, attachment delete, export create / download-token / download — PRIV-01) | ❌ | ✅ | ✅ |") {
		t.Error("cupthread-api/SKILL.md capability matrix has no privacy.manage row (admin/owner)")
	}
}

// TestAPISkillScheduleAtClearSemantics guards against Fact-1-class drift
// (issue #61): the SEC-40 gate table must not claim that ""/null both clear
// scheduledAt under the gate. "" 400s on the datetime schema and a JSON null
// clears without the gate (plain content.manage suffices).
func TestAPISkillScheduleAtClearSemantics(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "skills", "cupthread-api", "SKILL.md"))
	if err != nil {
		t.Fatalf("read skill doc: %v", err)
	}
	doc := string(data)

	for _, marker := range []string{
		"| `PUT .../changelog/:entryId` | body has a string `scheduledAt` (setting a schedule only) | `changelog.publish` |",
		"`scheduledAt: null` clears the schedule and is **not** gated",
		`an empty string fails validation with ` + "`400 {\"error\": \"Validation failed\"}`",
		"The CLI's `changelog update <id> --schedule-at \"\"` sends exactly `{\"scheduledAt\": null}`",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required scheduleAt-clear marker %s", marker)
		}
	}
	if strings.Contains(doc, "(`\"\"`/null clears it)") {
		t.Error("cupthread-api/SKILL.md still claims \"\" clears scheduledAt under the changelog.publish gate (issue #61)")
	}
}

// TestSkillDocsCommentThreadPagination pins the PROD-31 keyset-pagination
// contract for both comment-thread endpoints in the API skill (limit/cursor
// parameters, the {comments, total, hasMore, nextCursor} page shape, the
// 400 Invalid cursor error) and the CLI skill's promise that both list
// commands walk pages to the end and count with the server's total.
func TestSkillDocsCommentThreadPagination(t *testing.T) {
	cases := []struct {
		skill string
		want  []string
	}{
		{
			skill: "cupthread-api",
			want: []string{
				"Keyset-paginated (PROD-31)",
				"`{comments, total, hasMore, nextCursor}`",
				"not `comments.length`",
				"at most 200 rows",
				`400 {"error": "Invalid cursor"}`,
			},
		},
		{
			skill: "cupthread-cli",
			want: []string{
				"walk the thread's keyset pagination (PROD-31",
				"server's authoritative `total`",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.skill, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "skills", tc.skill, "SKILL.md"))
			if err != nil {
				t.Fatalf("read skill doc: %v", err)
			}
			doc := string(data)
			for _, marker := range tc.want {
				if !strings.Contains(doc, marker) {
					t.Errorf("%s/SKILL.md is missing required comment-pagination marker %q", tc.skill, marker)
				}
			}
		})
	}
}

// TestAPISkillAnonymousAccessEnforcement guards the PRIV-12 sync (issue #77):
// the API skill must document that the per-app anonymous-access flags are
// enforced server-side across every surface of the family — roadmap columns
// and versions, feature-request comment threads, and changelog subscribe —
// with the 401 authentication_required / 404 existence-hiding /
// 403 email_not_verified conditions, and must no longer present the
// changelog subscribe endpoint as unconditionally available.
func TestAPISkillAnonymousAccessEnforcement(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "skills", "cupthread-api", "SKILL.md"))
	if err != nil {
		t.Fatalf("read skill doc: %v", err)
	}
	doc := string(data)
	for _, marker := range []string{
		"## Per-App Anonymous-Access Enforcement (PRIV-12)",
		// Columns and versions inherit the roadmap flag with a coded 401.
		"anonymous roadmap view disabled answer `401` `authentication_required`",
		"Same `401` `authentication_required` (PRIV-12) on sign-in-only roadmaps",
		// Comment threads: 401 for anonymous reads, 404 existence hiding for
		// unapproved requests.
		"Sign-in-only boards (anonymous roadmap view disabled) answer `401` `authentication_required` to anonymous reads, and threads of **unapproved** requests return `404`",
		"existence hiding, indistinguishable from a missing id",
		// Changelog subscribe: the flag-off 401 and the verified-email binding.
		"Changelogs with anonymous access disabled reject anonymous calls with `401` `authentication_required`",
		"code\": \"email_not_verified",
		"Subscriptions on this changelog are bound to your signed-in email address",
		// Client guidance pinned by the issue.
		"Never tell the user the request never existed",
		"prompt for the **signed-in account's own email**",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required PRIV-12 marker %q", marker)
		}
	}
}

// TestAPISkillChangelogUnsubscribeDurability pins the #635 contract: a
// changelog unsubscribe token whose signature matches the app succeeds when
// public surfaces are disabled and after exp, while confirm routes stay
// strict and the one-click budget is still chosen from the query string
// before the body is read.
func TestAPISkillChangelogUnsubscribeDurability(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "skills", "cupthread-api", "SKILL.md"))
	if err != nil {
		t.Fatalf("read skill doc: %v", err)
	}
	doc := string(data)
	for _, marker := range []string{
		"## Changelog Unsubscribe on Private Apps and After Expiry (#635)",
		"even after `exp`",
		"public surfaces are disabled",
		`{ "unsubscribed": true }`,
		"Missing unsubscribe token",
		"An unsubscribe token is required",
		"Invalid or expired unsubscribe token",
		"Public surfaces are disabled for this app",
		"token missing or not valid for this app",
		"90-day `exp`",
		"Invalid or expired confirmation token",
		"ship-notifications/confirm",
		"before the POST body is read",
		"query string",
		"Do not drop a signed link",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required #635 marker %q", marker)
		}
	}
	if strings.Contains(doc, "Missing/invalid tokens fail uniformly with `400`") {
		t.Error("cupthread-api/SKILL.md still treats every changelog unsubscribe token failure as 400 (issue #154)")
	}
}

// TestSkillDocsPublicBoardReadLimits pins the issue #143 contract: the four
// public board reads share a 60/min per-IP bucket, answer the generic 429
// body, and may serve anonymous 200s from a 30-second cache. The changelog
// feed is the only one that splits by Clerk session. The CLI and SDK skills
// have to carry the same retry and staleness guidance so a later edit cannot
// drop it the way earlier API-sync issues dropped a status code.
func TestSkillDocsPublicBoardReadLimits(t *testing.T) {
	apiMarkers := []string{
		"## Public Board Read Limits and Shared Cache",
		"PUBLIC_READ_RATE_LIMITER",
		"60 requests / 60 s",
		"Cache-Control: public, max-age=30",
		"Vary: Authorization",
		"roadmapVisible",
		"`GET /api/v1/feature-requests/{id}/comments`",
		"`GET /api/v1/public/apps/{appKey}/changelog`",
		"`GET /api/v1/public/columns/{appKey}`",
		"`GET /api/v1/public/versions/{appKey}`",
		`429 {"error": "Too many requests. Please try again shortly."}`,
	}
	cliMarkers := []string{
		"60 requests/minute",
		"30-second shared cache",
		"comments moderation list` is the console route",
		"apps public-changelog",
	}
	sdkMarkers := []string{
		"## Public Board Read Limits and Shared Cache",
		"60 requests per minute",
		"Cache-Control: public, max-age=30",
		"`GET /api/v1/feature-requests/{id}/comments`",
		"`GET /api/v1/public/apps/{appKey}/changelog`",
		"`GET /api/v1/public/columns/{appKey}`",
		"`GET /api/v1/public/versions/{appKey}`",
		"needs no new status mapping",
	}

	check := func(t *testing.T, skill string, markers []string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", "skills", skill, "SKILL.md"))
		if err != nil {
			t.Fatalf("read skill doc: %v", err)
		}
		doc := string(data)
		for _, marker := range markers {
			if !strings.Contains(doc, marker) {
				t.Errorf("%s/SKILL.md is missing required public-read marker %q", skill, marker)
			}
		}
	}

	t.Run("cupthread-api", func(t *testing.T) { check(t, "cupthread-api", apiMarkers) })
	t.Run("cupthread-cli", func(t *testing.T) { check(t, "cupthread-cli", cliMarkers) })
	for _, skill := range []string{
		"cupthread-swift-sdk",
		"cupthread-android-sdk",
		"cupthread-react-native-sdk",
		"cupthread-flutter-sdk",
	} {
		t.Run(skill, func(t *testing.T) { check(t, skill, sdkMarkers) })
	}
}

// TestAPISkillOAuthServerDocs pins the OAuth authorization-server and public
// route facts the cupthread-api skill synced from the OpenAPI 3.1 spec
// (QUAL-04): RFC 8414 discovery, the token envelope's fixed lifetimes and
// rotating refresh tokens, S256-only PKCE, RFC 7009 always-empty revocation,
// the RFC 8628 polling errors, plus the released-only public versions list,
// the /files/:key image-serving hardening, and the uploads/images quota 429.
// Without these markers a future edit can silently desync the skill from the
// spec the way earlier API-sync issues (e.g. #67) did.
func TestAPISkillOAuthServerDocs(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "skills", "cupthread-api", "SKILL.md"))
	if err != nil {
		t.Fatalf("read skill doc: %v", err)
	}
	doc := string(data)
	for _, marker := range []string{
		"## OAuth Authorization Server (QUAL-04)",
		"GET /.well-known/oauth-authorization-server",
		"expires_in` = 1209600",
		"180 days",
		"rotate on every use",
		"invalid_grant",
		"code_challenge_method=S256",
		"RFC 7009",
		"empty body",
		"authorization_pending",
		"slow_down",
		"expired_token",
		"access_denied",
		"redirect_uri` must never become an open redirect",
		"released` flag is true are listed (PROD-32)",
		"`/api/v1/files/:key`",
		"private_attachment_forbidden",
		"daily_storage_quota_exceeded",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required OAuth-server/public-route marker %q", marker)
		}
	}
}

// TestAPISkillIssue152 pins the public-API contract synced for issue #152:
// private image attachments, shipNotifyEmail, fail-closed OAuth consent,
// 409 already_finalized, public-read 429s and offset clamps, and the
// free|pro tier (business removed).
func TestAPISkillIssue152(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"only the exact lowercase `allow` or `deny`",
		"decision must be 'allow' or 'deny'",
		"shipNotifyEmail",
		"email_not_verified",
		"Ship notifications on this board are bound to your signed-in email address",
		"no `url`, `key`, or `variants`",
		"already_finalized",
		"clamped to 5000",
		"clamped to 10000",
		"BOARD_LIST_RATE_LIMITER",
		"PUBLIC_CONFIG_RATE_LIMITER",
		"## Subscription Tier (`free` | `pro`)",
		"tier_limit_import_pro",
		"`business` value was removed",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#152 marker %q", marker)
		}
	}
	if strings.Contains(doc, "upload_finalized_concurrently") {
		t.Error("cupthread-api/SKILL.md uses the approximate code upload_finalized_concurrently; the wire code is already_finalized")
	}
}

// TestSDKSkillIssue152 pins the same SDK-facing facts in all four SDK skills:
// image uploads are private, a concurrent finalize is 409 already_finalized,
// votes may carry shipNotifyEmail, and board offset above 10000 clamps.
func TestSDKSkillIssue152(t *testing.T) {
	for _, skill := range []string{
		"cupthread-swift-sdk",
		"cupthread-android-sdk",
		"cupthread-flutter-sdk",
		"cupthread-react-native-sdk",
	} {
		t.Run(skill, func(t *testing.T) {
			doc := readSkill(t, skill)
			for _, marker := range []string{
				"no `url`, `key`, or `variants`",
				"private_attachment_forbidden",
				"already_finalized",
				"shipNotifyEmail",
				"email_not_verified",
				"Ship notifications on this board are bound to your signed-in email address",
				"`offset` above 10000 is clamped to 10000",
			} {
				if !strings.Contains(doc, marker) {
					t.Errorf("%s/SKILL.md is missing required issue-#152 marker %q", skill, marker)
				}
			}
		})
	}
}

// TestCLISkillIssue152 pins the CLI skill's tier and public-read wording so
// Linear/Notion/Slack cannot drift back to a Business plan requirement.
func TestCLISkillIssue152(t *testing.T) {
	doc := readSkill(t, "cupthread-cli")
	for _, marker := range []string{
		"Every import source requires Pro",
		"there is no `business` tier",
		"above 10000 is clamped to 10000",
		"rate limited per client IP (60/min, 429) before lookup",
		"Show subscription tier (free | pro)",
		"Public showcase, board listing, and public config GETs",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-cli/SKILL.md is missing required issue-#152 marker %q", marker)
		}
	}
	for _, stale := range []string{
		"Linear/Notion/Slack: Business",
		"need Business",
	} {
		if strings.Contains(doc, stale) {
			t.Errorf("cupthread-cli/SKILL.md still says %q", stale)
		}
	}
}

// TestIssue152CommandHelp pins the cobra help that agents read for imports
// and the public app reads. The subscription tier is free or pro, board
// offset above 10000 clamps, and public config is rate limited before lookup.
func TestIssue152CommandHelp(t *testing.T) {
	create := newImportsCreateCmd()
	if !strings.Contains(create.Long, "Every import source\nrequires Pro (402 tier_limit_import_pro)") {
		t.Errorf("imports create Long drifted:\n%s", create.Long)
	}
	if strings.Contains(strings.ToLower(create.Long), "business") {
		t.Errorf("imports create Long still names a business tier:\n%s", create.Long)
	}
	apps := newAppsCmd()
	fr := findSub(t, apps, "public-feature-requests")
	if !strings.Contains(fr.Long, "clamped to 10000") {
		t.Errorf("public-feature-requests Long missing clamp:\n%s", fr.Long)
	}
	offset := fr.Flags().Lookup("offset")
	if offset == nil || !strings.Contains(offset.Usage, "clamped to 10000") {
		t.Errorf("offset flag usage = %#v", offset)
	}
	cfg := findSub(t, apps, "public-config")
	if !strings.Contains(cfg.Long, "60/minute") || !strings.Contains(cfg.Long, "429") {
		t.Errorf("public-config Long missing rate limit:\n%s", cfg.Long)
	}
}

func readSkill(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "skills", name, "SKILL.md"))
	if err != nil {
		t.Fatalf("read skill doc: %v", err)
	}
	return string(data)
}

// TestAPISkillIssue135 pins the SEC-50 reply contract on the public comment
// create endpoint: parentId must reference a visible comment on the request
// in the URL, anything else is 400 invalid_parent with nothing stored, and
// replyToAuthorName stays ignored (the author is resolved server-side).
func TestAPISkillIssue135(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"`parentId` must reference a **visible** comment on the feature request in the URL",
		`"code": "invalid_parent"`,
		"The comment you are replying to was not found on this feature request",
		"nothing is stored (SEC-50)",
		"`replyToAuthorName` in the request body is ignored",
		"resolved server-side from the parent comment",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#135 marker %q", marker)
		}
	}
}

// TestCLISkillIssue135 pins the CLI skill's comments-create guidance: the
// --parent-id rule, the wire code, and the ignored reply-to-author-name flag.
func TestCLISkillIssue135(t *testing.T) {
	doc := readSkill(t, "cupthread-cli")
	for _, marker := range []string{
		"must reference a visible comment on the same",
		"`400 invalid_parent`",
		"The comment you are replying to was not found on this",
		"crafting `--reply-to-author-name`",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-cli/SKILL.md is missing required issue-#135 marker %q", marker)
		}
	}
}

// TestIssue135CommandHelp pins the cobra flag help agents read for comment
// replies: --parent-id states the same-request visible-parent rule and
// --reply-to-author-name admits the server ignores it.
func TestIssue135CommandHelp(t *testing.T) {
	create := newCommentsCreateCmd()
	parentID := create.Flags().Lookup("parent-id")
	if parentID == nil || !strings.Contains(parentID.Usage, "visible comment on the same feature request") || !strings.Contains(parentID.Usage, "400 invalid_parent") {
		t.Errorf("parent-id flag usage = %#v, want the SEC-50 rule and the wire code", parentID)
	}
	replyName := create.Flags().Lookup("reply-to-author-name")
	if replyName == nil || !strings.Contains(replyName.Usage, "ignored by the API") {
		t.Errorf("reply-to-author-name flag usage = %#v, want the ignored-by-the-API note", replyName)
	}
}

func findSub(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("command %q not found under %s", name, parent.Name())
	return nil
}
