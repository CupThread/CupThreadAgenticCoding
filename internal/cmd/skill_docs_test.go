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

// TestAPISkillDeviceFlowSlowDownEnforcement pins the issue #145 sync: the
// device-token row must state that the server enforces RFC 8628 §3.5 —
// polls inside the advertised interval get slow_down, every slow_down grows
// the required spacing by 5 seconds, capped at interval + 60 seconds — so
// clients cannot keep polling at a fixed interval.
func TestAPISkillDeviceFlowSlowDownEnforcement(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"the server enforces §3.5",
		"every `slow_down` grows the required spacing by 5 seconds, capped at `interval` + 60 seconds",
		"honor it by growing their polling interval",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required device-flow slow_down marker %q", marker)
		}
	}
}

// TestAPISkillIssue152 pins the public-API contract synced for issue #152:
// private image attachments, shipNotifyEmail (including the SaaS #613
// sign-in-required binding), fail-closed OAuth consent, 409 already_finalized,
// public-read 429s and offset clamps, and the free|pro tier (business removed).
func TestAPISkillIssue152(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"only the exact lowercase `allow` or `deny`",
		"decision must be 'allow' or 'deny'",
		"shipNotifyEmail",
		"email_not_verified",
		"Ship notifications on this board are bound to your signed-in email address",
		"the address must be one of the session's verified emails (compared case-insensitively)",
		"No consent row is written and no confirmation email is sent",
		"The warning body is the same whether or not that address already has a consent row",
		"Boards that allow anonymous voting accept any address and do not return this warning",
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

// TestAPISkillIssue144 pins the authenticated-revocation contract synced for
// issue #144: the RFC 7009 revoke endpoint requires client identity — a
// required client_id (400 invalid_request when missing, 400 invalid_client
// for unknown clients, 401 invalid_client for confidential-client secret
// failures), revokes only tokens issued to the authenticated client, and
// answers 400 unsupported_token_type for values that are neither cpt_ nor
// cpr_ tokens.
func TestAPISkillIssue144(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"required `client_id`",
		"`client_id is required`",
		"`Unknown client`",
		"invalid_client",
		"`client_secret`",
		"unsupported_token_type",
		"revokes nothing",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#144 marker %q", marker)
		}
	}
	if strings.Contains(doc, "optional `client_id`") {
		t.Error("cupthread-api/SKILL.md still describes the revoke client_id as optional; issue #144 makes it required")
	}
}

// TestAPISkillIssue146 pins the public-config contract synced for issue
// #146 (SEC-518): the optional allowedEmbedOrigins embed allowlist on both
// public config routes, its presence semantics, and its origin rules.
func TestAPISkillIssue146(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"allowedEmbedOrigins",
		"SEC-518",
		"an empty array is never returned",
		"at most 10 exact `https://` origins",
		"frame-ancestors",
		"Same `PublicAppConfig` (including `allowedEmbedOrigins`)",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#146 marker %q", marker)
		}
	}
}

// TestSDKSkillIssue152 pins the same SDK-facing facts in all four SDK skills:
// image uploads are private, a concurrent finalize is 409 already_finalized,
// votes may carry shipNotifyEmail (including the SaaS #613 sign-in-required
// binding), and board offset above 10000 clamps.
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
				"an address that is not one of the session's verified emails",
				"no confirmation email is sent",
				"Boards that allow anonymous voting accept any address",
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

// TestSkillDocsIssue134CurrencySignature pins the DATA-07 sync (issue #134):
// `currency` on PUT /api/v1/public/apps/{appKey}/user is a signed payment
// attribute (422 payment_attributes_require_signature without signature +
// timestamp), a signed update that omits `currency` preserves the stored
// value instead of coercing it to USD, and the canonical string is unchanged.
// The stale "currency-only writes stay unsigned" guidance must not resurface.
func TestSkillDocsIssue134CurrencySignature(t *testing.T) {
	t.Run("cupthread-api", func(t *testing.T) {
		doc := readSkill(t, "cupthread-api")
		for _, marker := range []string{
			"(`isPaying`, `mrr`, `plan`, `currency` — an explicit `null` counts)",
			"any of `isPaying`, `mrr`, `plan`, or `currency`",
			"### Omitted `currency` keeps the stored value (DATA-07)",
			"preserves the stored currency",
			"`USD` is applied only when a profile is **created** with no stored currency",
			"The signature canonical string is unchanged by DATA-07",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-api/SKILL.md is missing required issue-#134 marker %q", marker)
			}
		}
		for _, stale := range []string{
			"Identity-only and `currency`-only writes keep working unchanged",
			"`currency`-only writes keep working unchanged",
		} {
			if strings.Contains(doc, stale) {
				t.Errorf("cupthread-api/SKILL.md still says %q (currency is signed since DATA-07)", stale)
			}
		}
	})
	t.Run("cupthread-cli", func(t *testing.T) {
		doc := readSkill(t, "cupthread-cli")
		for _, marker := range []string{
			"(`isPaying`, `mrr`, `plan`, `currency` — an explicit `null` counts)",
			"a signed update that omits `currency` preserves the stored value instead of defaulting to `USD`",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-cli/SKILL.md is missing required issue-#134 marker %q", marker)
			}
		}
	})
	for _, skill := range []string{
		"cupthread-swift-sdk",
		"cupthread-android-sdk",
		"cupthread-react-native-sdk",
		"cupthread-flutter-sdk",
	} {
		t.Run(skill, func(t *testing.T) {
			doc := readSkill(t, skill)
			for _, marker := range []string{
				"any of `isPaying`, `mrr`, `plan`, or `currency`",
				"identity-only writes are the only unsigned path left",
				"preserves the stored currency instead of coercing it to `USD`",
			} {
				if !strings.Contains(doc, marker) {
					t.Errorf("%s/SKILL.md is missing required issue-#134 marker %q", skill, marker)
				}
			}
			for _, stale := range []string{
				"currency-only writes stay unsigned",
				"identity/currency-only updates",
			} {
				if strings.Contains(doc, stale) {
					t.Errorf("%s/SKILL.md still says %q (currency is signed since DATA-07)", skill, stale)
				}
			}
		})
	}
}

// TestIssue134SignUserAttrsCommandHelp pins the sign-user-attrs help and the
// unsigned-note gate on the currency attribute, so the CLI cannot drift back
// to treating currency as an unsigned field (issue #134, DATA-07).
func TestIssue134SignUserAttrsCommandHelp(t *testing.T) {
	sign := newAPISignUserAttrsCmd()
	if !strings.Contains(sign.Long, "payment attributes (isPaying, mrr, plan, or currency)") {
		t.Errorf("sign-user-attrs Long missing currency in the payment-attribute list:\n%s", sign.Long)
	}
}

// TestSkillDocsIssue196SchemaMirror pins the issue-#196 sync: sign-user-attrs
// mirrors the server's EndUserAttributesInputSchema before signing (currency
// 3-letter and non-null, mrr <= 1000000, plan 1-64 code points, non-null
// isPaying, RFC 4122 UUID userToken), and both skills teach that a body the
// schema rejects can never be signed into a sendable request.
func TestSkillDocsIssue196SchemaMirror(t *testing.T) {
	t.Run("command help", func(t *testing.T) {
		sign := newAPISignUserAttrsCmd()
		for _, marker := range []string{
			"mirrored against the server's schema before anything is signed",
			"currency must be a 3-letter alphabetic code",
			"mrr at most 1000000",
			"plan 1-64 characters",
			"userToken an RFC 4122 UUID",
		} {
			if !strings.Contains(sign.Long, marker) {
				t.Errorf("sign-user-attrs Long missing issue-#196 marker %q:\n%s", marker, sign.Long)
			}
		}
	})
	t.Run("cupthread-cli", func(t *testing.T) {
		doc := readSkill(t, "cupthread-cli")
		for _, marker := range []string{
			"mirrored against the server's `EndUserAttributesInputSchema` before anything is signed (issue #196)",
			"`mrr` at most `1000000`",
			"`plan` 1–64 characters (counted in Unicode code points)",
			"`userToken` an RFC 4122 UUID (in the body, or in `--user-token` when the body omits it)",
			"no round trip is ever burned on a guaranteed 400",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-cli/SKILL.md is missing required issue-#196 marker %q", marker)
			}
		}
	})
	t.Run("cupthread-api", func(t *testing.T) {
		doc := readSkill(t, "cupthread-api")
		for _, marker := range []string{
			"### Request-body field constraints run before the signature gate",
			"a signature computed over any other shape is dead on arrival",
			"`plan` a 1–64-character string (counted in Unicode code points) or `null`",
			"`mrr` a number between 0 and `1000000` or `null`",
			"`currency` a 3-letter alphabetic code (`[A-Za-z]{3}`)",
			"refuses to sign bodies that cannot pass (issue #196)",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-api/SKILL.md is missing required issue-#196 marker %q", marker)
			}
		}
	})
}

// TestAPISkillIssue132 pins the PRIV-19 avatar policy sync: profile writes
// only accept managed image URLs (400 avatar_url_not_allowed otherwise) and
// every public payload serves avatar fields as null or a managed https URL.
func TestAPISkillIssue132(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"## Profile Avatar URLs Are Managed-Host-Only (PRIV-19)",
		`<PUBLIC_BASE_URL>/api/v1/files/images`,
		"hostname is exactly `imagedelivery.net`",
		`{"error": "Validation failed", "code": "avatar_url_not_allowed"`,
		"`code: \"website_url_not_allowed\"`",
		"either `null` or a managed `https:` URL",
		"served as `null`",
		"[Profile Avatar URLs Are Managed-Host-Only (PRIV-19)](#profile-avatar-urls-are-managed-host-only-priv-19)",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#132 marker %q", marker)
		}
	}
	for _, stale := range []string{
		`"code": "avatar_url_invalid"`,
		"any `https:` URL is accepted",
	} {
		if strings.Contains(doc, stale) {
			t.Errorf("cupthread-api/SKILL.md still says %q", stale)
		}
	}

	cli := readSkill(t, "cupthread-cli")
	for _, marker := range []string{
		"Avatar fields in profile and board payloads are always `null` or a managed `https:` URL (PRIV-19)",
	} {
		if !strings.Contains(cli, marker) {
			t.Errorf("cupthread-cli/SKILL.md is missing required issue-#132 marker %q", marker)
		}
	}
}

// TestCLISkillMutationResultDocs pins the issue #194 mutation-output contract
// in the CLI skill: mutating commands always print exactly one structured
// document on stdout in --json/-o yaml mode, the minimal shape is
// {"action", "id", "success"} with id carrying the RESOLVED resource ID
// (the prefix→full-ID disclosure for features delete), and empty stdout after
// a successful mutation is a bug. Without these markers a future edit could
// re-teach agents to parse nothing after a delete.
func TestCLISkillMutationResultDocs(t *testing.T) {
	doc := readSkill(t, "cupthread-cli")
	for _, marker := range []string{
		"Mutation commands always print exactly one structured document on stdout",
		"never an empty stream",
		`{"action": "<verb>", "id": "<resource>", "success": true}`,
		"resolves the prefix and emits",
		"prefix matches was destroyed",
		"Whole-context actions with no resource id",
		"omit the `id` key",
		"Treat empty stdout after a",
		"mutation as a bug",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-cli/SKILL.md is missing required mutation-result marker %q", marker)
		}
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

// TestAPISkillVoteInsertOnlySemantics pins the PROD-36 insert-only vote
// contract in the API skill (issue #133): POST .../vote casts a vote and is
// idempotent — a repeat (or a retry after a network error) can no longer
// cancel it — and DELETE is the only way to un-vote, itself idempotent, with
// removing an absent vote still answering 200 and the same body rather than
// an error. It also rejects the stale toggle wording the row carried before
// the sync, so the toggle description cannot silently return.
func TestAPISkillVoteInsertOnlySemantics(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"Cast the caller's vote — insert-only (PROD-36), no longer a toggle",
		"Repeating the request is idempotent: it leaves an existing vote in place and re-serves the same `{voted: true, voteCount}` body",
		"a retry after a network error can no longer cancel the vote",
		"`DELETE` is the only way to remove it",
		"the only way to un-vote now that POST no longer toggles",
		"removing a vote that is not present still answers `200` with the same `{voted: false, voteCount}` body, not an error",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required insert-only vote marker %q", marker)
		}
	}
	if strings.Contains(doc, "Toggle (upvote / un-upvote)") {
		t.Error("cupthread-api/SKILL.md still describes POST vote as a toggle (issue #133)")
	}
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

// TestAPISkillIssue136 pins the OAuth consent-journey 429 contract (issue
// #136, SaaS SEC-61/SEC-27/SEC-65): GET/POST authorize and consent-info
// share one per-IP budget, the authorize 429 is the RFC 6749 §5.2 shape, the
// helper 429 is a plain {error} body, and the guidance marks the 429 as a
// transient per-minute condition rather than a protocol failure.
func TestAPISkillIssue136(t *testing.T) {
	api := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		// GET authorize row: the 429 replaces the consent redirect.
		"Rate-limited on the shared per-IP authorize budget (SEC-61, 60 requests/60 s",
		"`invalid_request` \"Too many requests\" body **instead of a redirect**",
		// POST authorize row: per-IP key, no client_id in it, shared journey.
		"SEC-61: keyed on the client IP alone",
		"a legitimate interactive consent journey spends ~3 requests",
		// Console helpers row: consent-info 429 body shape.
		"same shared per-IP authorize bucket",
		"an over-budget `consent-info` call answers `429` with a plain `{\"error\": …}` body",
		// Rate-limiting table row: budget, limiter, two-layer token/revoke spend.
		"`OAUTH_TOKEN_RATE_LIMITER`, SEC-61",
		"`429 {\"error\": \"invalid_request\", \"error_description\": \"Too many requests\"}`",
		// Retry guidance: consent-journey 429 is transient.
		"a `429` is transient within the per-minute window",
		"The CLI itself never calls consent-info or POST authorize",
	} {
		if !strings.Contains(api, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#136 marker %q", marker)
		}
	}
	cli := readSkill(t, "cupthread-cli")
	for _, marker := range []string{
		"The OAuth consent family (`GET`/`POST /oauth/authorize`, `GET /oauth/consent-info`, device lookup/decide) shares a separate 60 req/min per-IP budget",
		"transient within the per-minute window",
	} {
		if !strings.Contains(cli, marker) {
			t.Errorf("cupthread-cli/SKILL.md is missing required issue-#136 marker %q", marker)
		}
	}
}

// TestBoundedInputDocs guards the issue #142 sync: the skills must document
// the client-side bounded reads (route caps from SEC-36, the input_too_large
// code, and the 64 KB piped-credential bound), and every file/stdin input
// flag must name its cap so the limit is discoverable from --help alone.
func TestBoundedInputDocs(t *testing.T) {
	apiDoc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"read through a bounded reader against the route's cap first",
		"fails locally with `input_too_large` and nothing is sent",
	} {
		if !strings.Contains(apiDoc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required bounded-input marker %q", marker)
		}
	}
	for _, stale := range []string{
		// Over-limit bodies no longer reach the server, so no 413 passthrough.
		"get the `413` body passed through",
	} {
		if strings.Contains(apiDoc, stale) {
			t.Errorf("cupthread-api/SKILL.md still says %q", stale)
		}
	}

	cliDoc := readSkill(t, "cupthread-cli")
	for _, marker := range []string{
		"enforces those caps locally before sending",
		`code: "input_too_large"`,
		"64 KB",
	} {
		if !strings.Contains(cliDoc, marker) {
			t.Errorf("cupthread-cli/SKILL.md is missing required bounded-input marker %q", marker)
		}
	}

	usage := func(t *testing.T, cmd *cobra.Command, flag string) string {
		t.Helper()
		f := cmd.Flags().Lookup(flag)
		if f == nil {
			t.Fatalf("flag --%s not found on %s", flag, cmd.Name())
		}
		return f.Usage
	}
	request := findSub(t, newAPICmd(), "request")
	if got := usage(t, request, "input"); !strings.Contains(got, "max 1 MB") || !strings.Contains(got, "256 KB") {
		t.Errorf("api request --input usage = %q, want both route caps", got)
	}
	sign := findSub(t, newAPICmd(), "sign-user-attrs")
	if got := usage(t, sign, "input"); !strings.Contains(got, "max 256 KB") {
		t.Errorf("sign-user-attrs --input usage = %q, want the public cap", got)
	}
	settingsSet := findSub(t, findSub(t, newAppsCmd(), "settings"), "set")
	if got := usage(t, settingsSet, "input"); !strings.Contains(got, "max 1 MB") {
		t.Errorf("apps settings set --input usage = %q, want the console cap", got)
	}
	importsCreate := findSub(t, newImportsCmd(), "create")
	if got := usage(t, importsCreate, "options"); !strings.Contains(got, "max 1 MB") {
		t.Errorf("imports create --options usage = %q, want the console cap", got)
	}
	changelog := newChangelogCmd()
	for _, name := range []string{"create", "update"} {
		entry := findSub(t, changelog, name)
		if got := usage(t, entry, "body-file"); !strings.Contains(got, "max 1 MB") {
			t.Errorf("changelog %s --body-file usage = %q, want the console cap", name, got)
		}
	}
}

// TestAPISkillIssue204WidthHints pins the issue #204 sync of the public
// image-delivery contract on GET /api/v1/files/:key: the optional integer
// width hint accepts only the fixed 16–160 size set, width-hint responses
// (WebP thumbnail or original fallback) cache for five minutes while hintless
// requests keep the immutable one-day policy, clients must decode by the
// response content type rather than the URL extension, and public image URLs
// are handled as opaque URLs. It also rejects the stale max-age=60 cache
// policy the row carried before SaaS #649 moved the canonical originals to a
// content-addressed one-day entry with 15-second negative caching.
func TestAPISkillIssue204WidthHints(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"`Cache-Control: public, max-age=86400, immutable`",
		"a missing key 404s with `Cache-Control: public, max-age=15`",
		"`16, 32, 36, 56, 64, 72, 80, 112, 128, 160`",
		`400 {"error": "Unsupported image width"}`,
		"the original bytes are served as the fallback",
		"`Cache-Control: public, max-age=300` (five minutes)",
		"(`image/webp` when transformed)",
		"rather than infer the format from the URL extension",
		"as opaque — preserve the path and query string",
		// PRIV-19 avatar reads: documented sizes only, appended to the stored
		// URL, with an original-image fallback; other hosts untouched.
		"appended `?width=` thumbnail hint",
		"always with a fallback to the original URL",
		"must be passed through untouched",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#204 marker %q", marker)
		}
	}
	for _, stale := range []string{
		"max-age=60",
		// A thumbnail helper must never widen the size set beyond the
		// documented fixed values.
		"width=192",
		"width=256",
	} {
		if strings.Contains(doc, stale) {
			t.Errorf("cupthread-api/SKILL.md still says %q", stale)
		}
	}
}
