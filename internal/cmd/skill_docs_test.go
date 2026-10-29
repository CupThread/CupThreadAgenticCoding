package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
			doc := readSkill(t, tc.skill)
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
	doc := readSkill(t, "cupthread-api")

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
	doc := readSkill(t, "cupthread-api")

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
			doc := readSkill(t, tc.skill)
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
	doc := readSkill(t, "cupthread-api")
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
	doc := readSkill(t, "cupthread-api")
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
		doc := readSkill(t, skill)
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
	doc := readSkill(t, "cupthread-api")
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

// TestSkillDocsIssue235PlanReservedSentinels pins the issue-#235 sync: the
// plan strings "unset" and "null" are reserved signature sentinels (an
// absent plan signs as "unset", an explicit null as "null", so a literal
// equal to either is byte-indistinguishable from leave-unchanged/clear) and
// the server rejects them with 400 plan_reserved_sentinel before the
// signature check. The command help and both skills must teach the
// reservation, its byte-exactness, and the null/omit remediation.
func TestSkillDocsIssue235PlanReservedSentinels(t *testing.T) {
	t.Run("command help", func(t *testing.T) {
		sign := newAPISignUserAttrsCmd()
		for _, marker := range []string{
			`The plan strings "unset" and "null" are reserved signature sentinels`,
			"plan_reserved_sentinel",
			"clear the stored plan or omit the key to keep it",
			`"UNSET", "Unset", "unsettled", "nullify" remain valid`,
		} {
			if !strings.Contains(sign.Long, marker) {
				t.Errorf("sign-user-attrs Long missing issue-#235 marker %q:\n%s", marker, sign.Long)
			}
		}
	})
	t.Run("cupthread-cli", func(t *testing.T) {
		doc := readSkill(t, "cupthread-cli")
		for _, marker := range []string{
			"The plan strings `\"unset\"` and `\"null\"` are reserved signature sentinels (issue #235)",
			"`400 plan_reserved_sentinel` server-side",
			"Send JSON `null` to clear the stored plan or omit the key to keep it",
			"`\"UNSET\"`, `\"Unset\"`, `\"unsettled\"`, `\"nullify\"` remain valid",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-cli/SKILL.md is missing required issue-#235 marker %q", marker)
			}
		}
	})
	t.Run("cupthread-api", func(t *testing.T) {
		doc := readSkill(t, "cupthread-api")
		for _, marker := range []string{
			"**reserved signature sentinels**",
			`{"plan": ["plan_reserved_sentinel"]}`,
			"treat that as a normal validation error, do not retry",
			"never send those two literals as plan values",
			"One `plan` value rule sits in the same parse (issue #235)",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-api/SKILL.md is missing required issue-#235 marker %q", marker)
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
		"[Profile Avatar URLs Are Managed-Host-Only (PRIV-19)](user-identity-and-privacy.md#profile-avatar-urls-are-managed-host-only-priv-19)",
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

// TestCLISkillWebhookSecretGuidance pins the issue #190 sync: the CLI skill's
// secret-handling guidance must cover the per-app GitHub webhook secret — the
// stdin form, the $CUPTHREAD_GITHUB_WEBHOOK_SECRET fallback, and the
// deliberate-clear precedence — so the last inline-only secret flag cannot
// lose its documented off-the-command-line path again.
func TestCLISkillWebhookSecretGuidance(t *testing.T) {
	doc := readSkill(t, "cupthread-cli")
	for _, marker := range []string{
		"--webhook-secret -",
		"$CUPTHREAD_GITHUB_WEBHOOK_SECRET",
		`--webhook-secret ""`,
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-cli/SKILL.md is missing required webhook-secret marker %q", marker)
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

// skillDocFiles lists the markdown files that make up one skill's logical
// documentation: SKILL.md plus, when present, the reference/*.md detail files
// next to it. The cupthread-api skill split its per-endpoint contract detail
// into reference files so SKILL.md stays under the agent-skill size budget
// (issue #318); skill hosts cap SKILL.md only, so the pins below must check
// the index and its references as one document.
func skillDocFiles(t *testing.T, skill string) []string {
	t.Helper()
	root := filepath.Join("..", "..", "skills", skill)
	files := []string{filepath.Join(root, "SKILL.md")}
	refs, err := filepath.Glob(filepath.Join(root, "reference", "*.md"))
	if err != nil {
		t.Fatalf("glob reference docs: %v", err)
	}
	slices.Sort(refs)
	return append(files, refs...)
}

func readSkill(t *testing.T, name string) string {
	t.Helper()
	var sb strings.Builder
	for _, f := range skillDocFiles(t, name) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read skill doc %s: %v", f, err)
		}
		sb.WriteString("\n\n")
		sb.Write(data)
	}
	return sb.String()
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
// --parent-id rule, the wire code, and the ignored reply-to-author-name flag
// (the SEC-50 reply contract now phrased as a future-credential rule since
// issue #181 made the command fail fast).
func TestCLISkillIssue135(t *testing.T) {
	doc := readSkill(t, "cupthread-cli")
	for _, marker := range []string{
		"reference a visible comment on the same",
		"`400 invalid_parent`",
		"comment you are replying to was not found on this feature request",
		"`--reply-to-author-name` stays ignored",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-cli/SKILL.md is missing required issue-#135 marker %q", marker)
		}
	}
}

// TestCLISkillIssue181CommentsCreateUnavailable pins the issue #181 docs
// contract: the CLI skill must no longer present `comments create` as a
// credential-usable command (it never was — the POST needs a Clerk end-user
// session), must teach the local fail-fast with the web-portal guidance, and
// must no longer recommend `--reply-to`.
func TestCLISkillIssue181CommentsCreateUnavailable(t *testing.T) {
	doc := readSkill(t, "cupthread-cli")
	for _, marker := range []string{
		"`comments create` cannot succeed with any CLI credential and fails locally",
		"before sending anything",
		"requires a signed-in end-user",
		"Post the comment from the CupThread web portal while",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-cli/SKILL.md is missing required issue-#181 marker %q", marker)
		}
	}
	for _, stale := range []string{
		// The old runnable block presented the command as working and
		// recommended --reply-to, whose wire field the server schema strips.
		"comments create <featureRequestId> --body",
		"[--reply-to <clerkId>]",
	} {
		if strings.Contains(doc, stale) {
			t.Errorf("cupthread-cli/SKILL.md still teaches the impossible invocation %q (issue #181)", stale)
		}
	}
}

// TestIssue181CommentsCreateFailFastHelp pins the truthful help text and the
// flag cleanup: the Long help states the command can never succeed with a CLI
// credential and names the web portal, and the dead identity/reply/header
// flags are no longer declared so cobra rejects them as unknown.
func TestIssue181CommentsCreateFailFastHelp(t *testing.T) {
	create := newCommentsCreateCmd()
	for _, marker := range []string{
		"cannot succeed with any CLI credential",
		"signed-in end-user session",
		"fails locally, before sending anything",
		"Post the comment from the CupThread web",
		"guaranteed 401 round-trip",
	} {
		if !strings.Contains(create.Long, marker) {
			t.Errorf("comments create Long missing issue-#181 marker %q:\n%s", marker, create.Long)
		}
	}
	for _, dead := range []string{"author-name", "author-email", "author-avatar-url", "reply-to", "app-key", "user-token"} {
		if create.Flags().Lookup(dead) != nil {
			t.Errorf("comments create still declares the dead flag --%s (issue #181)", dead)
		}
	}
	for _, kept := range []string{"body", "parent-id", "reply-to-author-name"} {
		if create.Flags().Lookup(kept) == nil {
			t.Errorf("comments create lost the kept flag --%s", kept)
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
// requests keep the one-day policy, clients must decode by the
// response content type rather than the URL extension, and public image URLs
// are handled as opaque URLs. It also rejects the stale max-age=60 cache
// policy the row carried before SaaS #649 moved the canonical originals to a
// content-addressed one-day entry with 15-second negative caching.
func TestAPISkillIssue204WidthHints(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"`Cache-Control: public, max-age=86400`",
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

// TestAPISkillIssue231SingleFeatureRequestRead pins the issue #231 sync of
// the new public single-request read, GET /api/v1/feature-requests/{id}
// (SaaS #713): the 200 body is {request} in the board-listing record shape,
// an X-User-Token (or Clerk session) personalizes hasVoted/isOwnRequest, and
// the gate chain mirrors the board listing and comment thread — public-read
// rate limit first, then existence hiding (missing / unapproved / private /
// deactivated all answer the same uniform 404, and the unapproved owner's own
// token does not un-hide it), then the PRIV-12 gate ahead of the approved
// check. It also pins the shared-cache split (only an anonymous 200 caches;
// token- and session-bearing callers skip the cache in both directions, 404s
// never cache), the 60/min PUBLIC_READ_RATE_LIMITER membership, and the
// PRIV-12 matrix rows, and rejects the stale "four public board reads"
// enumerations the sync replaced.
func TestAPISkillIssue231SingleFeatureRequestRead(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		// Endpoint row: purpose, record shape, and personalization.
		"| `/api/v1/feature-requests/:id` | `GET` |",
		"the deep-link read behind the web portal's shareable `/{workspaceSlug}/{appSlug}/requests/{requestId}` URLs",
		"The `200` body is `{request}` holding the **same record shape the board listing serves per item**",
		"`voteCount` (live votes plus imported historical total)",
		"`hasVoted` reflects that token's ballot and `isOwnRequest` marks the caller's own request",
		"a malformed token personalizes nothing (silently treated as absent, no error)",
		// Gate chain: limiter, existence hiding, PRIV-12.
		"the spend is the first statement, before any lookup",
		"the same uniform `404 {\"error\": \"Feature request not found\"}`",
		"the unapproved requester's own token does **not** un-hide it",
		"checked before the approved check",
		// Cache split.
		"a token- or session-bearing caller skips the cache in both directions",
		// Rate-limit and cache sections.
		"Five public GETs document `429` and a short shared cache.",
		"| `GET /api/v1/feature-requests/{id}` | Only an **anonymous** `200` is cached",
		"skips the cache in **both** directions (SEC-15: the Cache API keys by URL and ignores `Vary`",
		"404s are never cached",
		"a deep link adds one detail read",
		// PRIV-12 matrix rows and the ordering note.
		"| `GET /api/v1/feature-requests/:id` | anonymous read, `allowAnonymousRoadmap` off | `401` `authentication_required` |",
		"| `GET /api/v1/feature-requests/:id` | request exists but is **unapproved** |",
		"the `401` gate runs **after** the missing/private existence `404` but **before** the approved `404`",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#231 marker %q", marker)
		}
	}
	for _, stale := range []string{
		// The pre-sync enumerations of the public-read family: the detail
		// read is a member now, so every "four" count is wrong.
		"the four public board reads",
		"For the four public board reads",
		"Four public GETs document",
		"The four routes share",
		"those four may come from a 30-second shared cache",
		// The pre-sync ordering note, which named only the comments thread.
		"the comments-thread `401` check runs **after** existence hiding",
		// The pre-sync ordering conclusion, which missed that a missing id
		// still answers 404 (gate sits between the two 404 checks).
		"cannot distinguish existing ids from missing ones",
	} {
		if strings.Contains(doc, stale) {
			t.Errorf("cupthread-api/SKILL.md still says %q", stale)
		}
	}
}

// TestAPISkillIssue230RevalidatableImageCache pins the issue #230 sync of the
// public image-delivery cache policy on GET /api/v1/files/:key: the one-day
// hit response drops the `immutable` directive — `Cache-Control: public,
// max-age=86400` — so an explicit reload revalidates and can discover a
// deleted object before the edge TTL lapses (SaaS PR #775, issue #715), while
// the 15-second negative cache and the five-minute width-hint policy are
// unchanged.
func TestAPISkillIssue230RevalidatableImageCache(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"`Cache-Control: public, max-age=86400`",
		"the response is not `immutable`, so an explicit reload revalidates",
		"an explicit reload revalidates and can discover a deleted object",
		"before the edge TTL lapses",
		"a missing key 404s with `Cache-Control: public, max-age=15`",
		"`Cache-Control: public, max-age=300` (five minutes)",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#230 marker %q", marker)
		}
	}
	// ", immutable`" is the dropped directive as it appeared in the old
	// `Cache-Control` header value; the only remaining "immutable" mentions
	// are the "not `immutable`" reload-revalidation explanations.
	if strings.Contains(doc, ", immutable`") {
		t.Errorf("cupthread-api/SKILL.md still promises an immutable public image cache")
	}
}

// TestSkillDocsConsoleOffsetCap guards the console-listing offset-cap notes
// synced with the server's shared CONSOLE_LIST_MAX_OFFSET clamp (issue #178):
// every offset-paginated console listing degrades an offset above 10,000 to
// the cap and still answers 200, so the cupthread-cli skill must tell agents
// paging the four list commands that --offset stops there (the CLI rewrites
// an out-of-range value with a warning notice, and a page starting at the
// cap repeats forever), and the cupthread-api skill must document the
// server-side behavior on the console routes.
func TestSkillDocsConsoleOffsetCap(t *testing.T) {
	cases := []struct {
		skill string
		want  []string
	}{
		{
			skill: "cupthread-cli",
			want: []string{
				// inbox list
				"stops at the server's shared console",
				// notifications list
				"it caps `--offset` at the",
				// features list
				"capped at the server's shared console cap",
				// changelog list
				"itself caps at the server's shared console cap",
				"once a page starts at the cap, the printed next offset re-serves",
				// the changelog one-liner example
				"--offset caps at 10000",
			},
		},
		{
			skill: "cupthread-api",
			want: []string{
				"Console listing offsets are capped at 10,000",
				"CONSOLE_LIST_MAX_OFFSET",
				"degrades to 10,000 and still returns `200`",
				"degrades to `0`",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.skill, func(t *testing.T) {
			doc := readSkill(t, tc.skill)
			for _, marker := range tc.want {
				if !strings.Contains(doc, marker) {
					t.Errorf("%s/SKILL.md is missing required offset-cap marker %q", tc.skill, marker)
				}
			}
		})
	}
}

// TestAPISkillIssue234ImageUploadStable500 pins the issue #234 sync of the
// stable storage-failure body on POST /api/v1/uploads/images (SaaS PR #793,
// #690): an unexpected R2 / Cloudflare Images / D1 failure inside the route
// answers the provider-agnostic `500 {"error": "Image upload failed. Please
// try again.", "code": "image_upload_failed"}` — the same no-context rule the
// console icon route applies since SEC-69 — with the provider detail logged
// server-side only, while the platform-authored 400/413/415 validation set
// keeps its plain {error} shape without a `code` field, and the client
// guidance branches on the `code` for retry UX instead of parsing the message.
func TestAPISkillIssue234ImageUploadStable500(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		// The wire body, verbatim, in the endpoint row and the policy section.
		`500 {"error": "Image upload failed. Please try again.", "code": "image_upload_failed"}`,
		// The stability framing and the SEC-69 precedent.
		"stable, provider-agnostic",
		"the same no-context rule the console icon route applies (SEC-69)",
		// Detail never reaches the caller.
		"logged server-side only",
		// The validation set keeps its uncoded {error} shape.
		"The `415` bodies carry no `code` field — match on the status, not on an error code.",
		// Retry guidance keyed on the code, not the message.
		"branch on `code == \"image_upload_failed\"`",
		"never parse the `error` text",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#234 marker %q", marker)
		}
	}
	for _, stale := range []string{
		// The pre-#793 catch-all body: a bare generic error with no `code`.
		`{"error": "Image upload failed"}`,
	} {
		if strings.Contains(doc, stale) {
			t.Errorf("cupthread-api/SKILL.md still documents the pre-#793 upload-failure body %q", stale)
		}
	}
}

// TestAPISkillIssue240VoteProposeUserTokenHeader pins the issue #240 sync of
// the SaaS #834/#837 write-side token transport: the feature-request vote
// (POST and DELETE) and propose writes accept the end-user token in either
// the JSON body field or the X-User-Token header, with the body field winning
// when both are present, a present-but-invalid body field still failing zod
// validation, and a payload with neither source answering the same 400 shape
// (details.fieldErrors.userToken/requesterToken "Required — send the token in
// the JSON body or the X-User-Token header"). It also pins the spec
// corrections the SaaS PR shipped: the vote DELETE documents its required
// appKey-bearing JSON body (a spec-following client that sent none got that
// 400), the propose row documents the implemented CreateFeatureRequestInput
// fields (required appKey/title/description; optional requesterName/
// requesterToken/turnstileToken; no requesterEmail) and the 201
// {featureRequestId, pending: true} body, and the comment POST keeps
// Clerk-session-only authentication with no X-User-Token parameter — while
// the read paths' silent malformed-header degradation never crosses onto the
// writes (a non-UUID header merged into a write is a hard 400).
func TestAPISkillIssue240VoteProposeUserTokenHeader(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		// The auth overview bullet names the write-side transport.
		"the feature-request **vote and propose writes** accept the end-user token in either the JSON body field (`userToken`/`requesterToken`) or the `X-User-Token` header",
		// SEC-12 subsection: precedence, error contract, asymmetry.
		"### Vote and propose writes accept the header too (SaaS #834)",
		"The **body field wins** when both are present: only an *absent* body field is replaced with the header value",
		"A body field that is **present but invalid** keeps failing schema validation exactly as before",
		"`details.fieldErrors.<field>: [\"Required — send the token in the JSON body or the X-User-Token header\"]`",
		"a non-UUID header value is a hard `400`, not a silent fallthrough",
		"The vote `DELETE` still requires a JSON body carrying `appKey`",
		// Propose row: implemented schema, token transport, 201 body.
		"there is no `requesterEmail` field",
		"The requester token may be sent in the body field `requesterToken` **or** the `X-User-Token` header (SaaS #834)",
		"`details.fieldErrors.requesterToken: [\"Required — send the token in the JSON body or the X-User-Token header\"]`",
		// 201 body renamed by the SaaS #1105 spec correction (issue #256):
		// the two-field intake receipt, never the full board record.
		"Success answers `201` with the two-field intake receipt `FeatureRequestSubmissionCreated` (SaaS #1105): `{featureRequestId, pending}`, both required",
		// Vote POST row: header-only body shape.
		"a header-only vote body carries just `appKey`",
		"`details.fieldErrors.userToken: [\"Required — send the token in the JSON body or the X-User-Token header\"]`",
		// Vote DELETE row: the previously undocumented body.
		"The `DELETE` reads the **same JSON body as the POST**",
		"`appKey` is always required",
		"a spec-following client that sent none got the `400 Validation failed` above",
		// Comments POST: Clerk-session-only, no header parameter.
		"Authentication is the Clerk end-user session only — the route reads no `X-User-Token`",
		// Corrected OpenAPI components.
		"`CreateFeatureRequestInput` requires `appKey`/`title`/`description` with optional `requesterName`/`requesterToken`/`turnstileToken` and no `requesterEmail`",
		"`VoteResponse` is `voted` + `voteCount` with an optional `warning` (no `featureRequestId`/`hasVoted`)",
		"the vote `DELETE` documents its JSON body",
		// Backward compatibility.
		"every body-token payload accepted before is accepted byte-for-byte on the old validation path",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#240 marker %q", marker)
		}
	}
	for _, stale := range []string{
		// The pre-sync propose row was a single sentence with no contract.
		"| `/api/v1/feature-requests` | `POST` | Submit a new feature request. |",
	} {
		if strings.Contains(doc, stale) {
			t.Errorf("cupthread-api/SKILL.md still documents the pre-#837 text %q", stale)
		}
	}
}

// TestAPISkillIssue242VoteSubscribeTurnstileToken pins the issue #242 sync of
// the optional turnstileToken field on the two email-triggering public
// writes:
//   - POST /api/v1/public/apps/{appKey}/changelog/subscribe
//     (SubscribeChangelogInput) accepts an optional turnstileToken (string,
//     10-2048 chars); when the server enforces Turnstile human verification an
//     anonymous subscription requires a valid token and one that is missing or
//     fails verification answers 403 {"error": "Human verification failed.
//     Please try again.", "code": "turnstile_verification_failed"}, while
//     signed-in callers subscribing with their session's verified email
//     address are exempt.
//   - POST /api/v1/feature-requests/{id}/vote (VoteFeatureRequestInput)
//     accepts an optional turnstileToken; when verification is enforced an
//     anonymous voter that opts in via shipNotifyEmail should supply a
//     verified token, and a missing or failed token leaves the vote itself
//     unchanged ({voted: true, voteCount}, no warning) while silently
//     suppressing the ship-notification opt-in (no consent row, no
//     confirmation email) to protect recipients from abuse.
//
// Every SDK skill's vote section carries the same suppression warning for
// custom-voting-UI authors.
func TestAPISkillIssue242VoteSubscribeTurnstileToken(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		// Subscribe row: the field, the gated 403 body, the signed-in exemption.
		"The request body (`SubscribeChangelogInput`) accepts an optional `turnstileToken` (string, 10–2048 chars)",
		`403 {"error": "Human verification failed. Please try again.", "code": "turnstile_verification_failed"}`,
		"signed-in callers subscribing with their session's verified email address are exempt",
		// Changelog flow blockquote: the same gate in the flow summary.
		"anonymous subscribe bodies must also carry a valid `turnstileToken` or they answer `403` `turnstile_verification_failed`",
		// Vote POST row: the field and the observable refusal warning.
		"optional `turnstileToken` (string, 10–2048 chars, `VoteFeatureRequestInput`)",
		"on a board whose public config sets `requireAnonymousVoteTurnstile: true`, a missing or failed token rejects the vote outright with the SEC-846 `403`",
		"on boards without that flag, a missing or failed token leaves the vote itself at `{voted: true, voteCount}` unchanged but the refusal is observable",
		// Ship-notification section: the observable-warning contract (SaaS
		// #1472 replaced the old silent suppression).
		"### Turnstile-gated ship-notification opt-in (#242)",
		"the response carries a `warning` object with `code: \"ship_notify_verification_failed\"`",
		"no consent row is written and no confirmation email is sent",
		"a client must never treat a bare `200` as proof the address was enrolled",
		// Issue #243 correction: the observable refusal is qualified by
		// the vote gate — it only applies while the vote itself passes
		// (flag false); flag-on boards reject the vote instead.
		"When the flag is `true`, an anonymous opt-in with a missing or failed token never reaches that warning state",
		"the opt-in guard applies to every anonymous opt-in, and a token that satisfies the vote gate also satisfies it",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#242 marker %q", marker)
		}
	}
	for _, stale := range []string{
		// Superseded by issue #243: PublicAppConfig now legitimately carries
		// the requireAnonymousVoteTurnstile vote gate, but the pre-#243 docs
		// must not come back claiming either the old no-field position or the
		// even older config-signal position.
		"`PublicAppConfig` exposes no Turnstile-enforcement field",
		"the host app shows its Turnstile challenge based on its own app settings",
	} {
		if strings.Contains(doc, stale) {
			t.Errorf("cupthread-api/SKILL.md still documents the incorrect public-config Turnstile signal %q", stale)
		}
	}

	// Every SDK skill's vote section must warn custom-voting-UI authors that a
	// 200 does not prove the ship-notify address was enrolled when Turnstile
	// verification fails: the refusal now rides the 200 as the observable
	// ship_notify_verification_failed warning (SaaS #1472).
	for _, skill := range []string{
		"cupthread-swift-sdk",
		"cupthread-android-sdk",
		"cupthread-react-native-sdk",
		"cupthread-flutter-sdk",
	} {
		t.Run(skill, func(t *testing.T) {
			doc := readSkill(t, skill)
			vote := skillSection(t, doc, "## Feature Request Vote Rate Limits (429 Too Many Requests)")
			for _, marker := range []string{
				"an anonymous vote that opts in via `shipNotifyEmail` should carry a verified `turnstileToken` (string, 10–2048 chars)",
				// Both refusal consequences in one pinned phrase: a loose
				// "no confirmation email" marker would also match the
				// preceding email_not_verified bullet, hiding an omission
				// from the Turnstile bullet.
				"no consent row is written and no confirmation email is sent",
				"ship_notify_verification_failed",
				"a `200` **without** a `warning` means the opt-in was recorded",
			} {
				if !strings.Contains(vote, marker) {
					t.Errorf("%s/SKILL.md vote section is missing required issue-#242 marker %q", skill, marker)
				}
			}
		})
	}
}

// TestSkillDocsIssue243RequireAnonymousVoteTurnstile pins the issue #243 sync
// of the SEC-846 Turnstile vote gate:
//   - PublicAppConfig gains requireAnonymousVoteTurnstile (boolean, always
//     served, defaults to false) alongside the other anonymous-access flags.
//   - When a board sets it to true, an anonymous voter must supply a verified
//     turnstileToken (string, 10-2048 chars) minted for action "vote" with
//     cdata bound to the board's appKey, or POST /feature-requests/{id}/vote
//     rejects the vote outright: a missing token answers 403
//     {"error": "Human verification (Turnstile) is required",
//     "code": "turnstile_required"} and a token that fails verification
//     answers 403 {"error": "Human verification failed. Please try again.",
//     "code": "turnstile_verification_failed"} — the vote is not recorded.
//     Signed-in voters are exempt (the guard is flag && !identity).
//   - The flag gates only the vote itself: the #242 ship-notification opt-in
//     guard applies to every anonymous opt-in regardless of the flag, so a
//     200 still never proves the address was enrolled.
//
// Every SDK skill's vote section carries the same hard-gate warning so
// custom-voting-UI authors read the flag off the public config before
// rendering the ballot.
func TestSkillDocsIssue243RequireAnonymousVoteTurnstile(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		// Public-config endpoint row: the flag joins the anonymous-access list.
		"and the `requireAnonymousVoteTurnstile` SEC-846 vote-verification gate",
		// The dedicated gate section.
		"### Turnstile-gated anonymous vote (SEC-846, #243)",
		"`PublicAppConfig` carries a `requireAnonymousVoteTurnstile` flag (boolean, always served, defaults to `false`)",
		"minted for action `vote` with cdata bound to the board's `appKey`",
		`403 {"error": "Human verification (Turnstile) is required", "code": "turnstile_required"}`,
		`403 {"error": "Human verification failed. Please try again.", "code": "turnstile_verification_failed"}`,
		"the guard is `requireAnonymousVoteTurnstile && !identity`",
		"the ship-notification opt-in guard below applies to every anonymous opt-in regardless of the flag",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api/SKILL.md is missing required issue-#243 marker %q", marker)
		}
	}

	for _, skill := range []string{
		"cupthread-swift-sdk",
		"cupthread-android-sdk",
		"cupthread-react-native-sdk",
		"cupthread-flutter-sdk",
	} {
		t.Run(skill, func(t *testing.T) {
			doc := readSkill(t, skill)
			vote := skillSection(t, doc, "## Feature Request Vote Rate Limits (429 Too Many Requests)")
			for _, marker := range []string{
				"sets `requireAnonymousVoteTurnstile` to `true`, an anonymous vote must carry a verified `turnstileToken` (string, 10–2048 chars) minted for action `vote` with cdata bound to the board's `appKey`, or it is rejected outright",
				`403 {"error": "Human verification (Turnstile) is required", "code": "turnstile_required"}`,
				`403 {"error": "Human verification failed. Please try again.", "code": "turnstile_verification_failed"}`,
				"the vote is not recorded. Signed-in voters are exempt; read the flag off `PublicAppConfig` before rendering the ballot",
				"and that suppression only applies while the vote itself passes: when the board sets `requireAnonymousVoteTurnstile` to `true`, a missing or failed token rejects the whole vote with the `403`s above instead of suppressing anything",
			} {
				if !strings.Contains(vote, marker) {
					t.Errorf("%s/SKILL.md vote section is missing required issue-#243 marker %q", skill, marker)
				}
			}
		})
	}
}

// TestSkillDocsIssue254PublicUserProfileResponseShape pins the issue #254 sync
// of the OpenAPI 3.1 PublicUserProfileResponse contract on GET
// /api/v1/users/{userId}/profile (SaaS #1190 / PR #1192):
//   - Top-level keys are {profile, publicApps, recentComments}, all three
//     required (recentComments is always emitted, [] when hidden or empty —
//     never apps or top-level hideComments).
//   - profile (UserProfile component) has {clerkUserId, displayName, avatarUrl,
//     bio, websiteUrl, createdAt}, all six required, with no hideComments or
//     updatedAt.
//   - publicApps items (PublicUserProfileApp component — the workspace-owner
//     member-of shape, not the showcase PublicAppSummary shape) have {id,
//     workspaceSlug, workspaceName, appSlug, name, description, iconUrl}, all
//     seven required.
//   - recentComments items have {id, body, createdAt, featureRequestId,
//     featureRequestTitle, workspaceSlug, appSlug, appName}, all eight required
//     (no appId).
//   - Both the API skill and the four SDK skills warn strict decoders
//     (Codable, kotlinx.serialization) to read publicApps and not require the
//     removed phantom keys.
func TestSkillDocsIssue254PublicUserProfileResponseShape(t *testing.T) {
	t.Run("cupthread-api", func(t *testing.T) {
		doc := readSkill(t, "cupthread-api")
		for _, marker := range []string{
			// Endpoint row summary.
			"(`PublicUserProfileResponse`: `{profile, publicApps, recentComments}`, all three keys required; `recentComments` is always emitted, `[]` when hidden or empty — never `apps` or top-level `hideComments`, SaaS #1190",
			// PRIV-06 schema breakdown.
			"**Exact `PublicUserProfileResponse` shape (OpenAPI 3.1, SaaS #1190 / PR #1192):**",
			"the top-level response is always `{profile, publicApps, recentComments}` — all three keys required",
			"`profile` (`UserProfile` component): `{clerkUserId, displayName, avatarUrl, bio, websiteUrl, createdAt}` — all six keys required",
			"Neither `hideComments` (the privacy flag gates `recentComments` server-side and is never disclosed) nor `updatedAt` is ever emitted on `profile`.",
			"`publicApps` items (`PublicUserProfileApp` component — the workspace-owner member-of shape, **not** the showcase `PublicAppSummary` `{appId, slug, storeUrl, storeKind}` shape): `{id, workspaceSlug, workspaceName, appSlug, name, description, iconUrl}` — all seven keys required",
			"`recentComments` items: `{id, body, createdAt, featureRequestId, featureRequestTitle, workspaceSlug, appSlug, appName}` — all eight keys required",
			"`appId` is never emitted on comment items",
			"must decode `publicApps` (not `apps`) and must not require the removed phantom keys (`apps`, top-level or inner `hideComments`, inner `updatedAt`, or comment `appId`)",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-api/SKILL.md is missing required issue-#254 marker %q", marker)
			}
		}
	})

	sdkMarkers := []string{
		"**Decode the served `PublicUserProfileResponse` envelope (SaaS #1190):**",
		"`{profile, publicApps, recentComments}` — all three keys required (`recentComments` is always emitted, `[]` when hidden or empty; never `apps` or top-level `hideComments`)",
		"`profile` (`UserProfile`) has `{clerkUserId, displayName, avatarUrl, bio, websiteUrl, createdAt}` (no `hideComments` or `updatedAt`)",
		"`publicApps` item (`PublicUserProfileApp`, not `PublicAppSummary`) has `{id, workspaceSlug, workspaceName, appSlug, name, description, iconUrl}`",
		"`recentComments` item has `{id, body, createdAt, featureRequestId, featureRequestTitle, workspaceSlug, appSlug, appName}` (no `appId`)",
		"Strict decoders (`Codable`, `kotlinx.serialization`) must read `publicApps` and must not require the removed phantom keys.",
	}
	for _, skill := range []string{
		"cupthread-swift-sdk",
		"cupthread-android-sdk",
		"cupthread-react-native-sdk",
		"cupthread-flutter-sdk",
	} {
		t.Run(skill, func(t *testing.T) {
			doc := readSkill(t, skill)
			for _, marker := range sdkMarkers {
				if !strings.Contains(doc, marker) {
					t.Errorf("%s/SKILL.md is missing required issue-#254 marker %q", skill, marker)
				}
			}
		})
	}
}

// TestSkillDocsIssue262PublicFeatureRequestRemovedInternalFields pins the
// issue #262 sync of CupThread/SaaS#1320 (PR CupThread/SaaS#1323):
//   - GET /api/v1/feature-requests and GET /api/v1/feature-requests/{id} no
//     longer serve the ten undocumented tenant-internal keys: importSource,
//     importRef, githubIssueId, githubIssueNumber, githubIssueUrl,
//     githubDiscussionId, githubDiscussionNumber, githubDiscussionUrl,
//     githubLastSyncedAt, and githubSyncStatus.
//   - Documented public FeatureRequest fields (including importedVotes,
//     voteCount, hasVoted, isOwnRequest, and the column/version fields) remain
//     unchanged.
//   - Both cupthread-api and the four SDK skills document the removed keys and
//     instruct strict/custom SDK models to drop them.
func TestSkillDocsIssue262PublicFeatureRequestRemovedInternalFields(t *testing.T) {
	removedKeys := []string{
		"`importSource`",
		"`importRef`",
		"`githubIssueId`",
		"`githubIssueNumber`",
		"`githubIssueUrl`",
		"`githubDiscussionId`",
		"`githubDiscussionNumber`",
		"`githubDiscussionUrl`",
		"`githubLastSyncedAt`",
		"`githubSyncStatus`",
	}

	t.Run("cupthread-api", func(t *testing.T) {
		doc := readSkill(t, "cupthread-api")
		for _, marker := range []string{
			// Single-request detail row.
			"SaaS #1320 / PR #1323: neither this endpoint nor `GET /api/v1/feature-requests` serves the ten undocumented tenant-internal keys",
			// Public feature-request schema paragraph.
			"Both `GET /api/v1/feature-requests` (board listing/search) and `GET /api/v1/feature-requests/{id}` (request detail) serve only the documented public `FeatureRequest` schema (SaaS #1320 / PR #1323)",
			"ten undocumented tenant-internal keys previously leaked as `null`",
			"are never served on public surfaces (drop them from any SDK or client model that declared them; `importedVotes`, `voteCount`, `hasVoted`, `isOwnRequest`, and the column/version fields remain unchanged)",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-api/SKILL.md is missing required issue-#262 marker %q", marker)
			}
		}
		for _, key := range removedKeys {
			if !strings.Contains(doc, key) {
				t.Errorf("cupthread-api/SKILL.md is missing removed internal key %s", key)
			}
		}
	})

	sdkMarkers := []string{
		"**Public `FeatureRequest` payloads omit tenant-internal keys (SaaS #1320 / PR #1323):**",
		"`GET /api/v1/feature-requests` and `GET /api/v1/feature-requests/{id}` serve only the documented public `FeatureRequest` schema (including `importedVotes`, `voteCount`, `hasVoted`, `isOwnRequest`, and the column/version fields)",
		"are never served on public surfaces — drop them from any custom or strict SDK model that required them.",
	}
	for _, skill := range []string{
		"cupthread-swift-sdk",
		"cupthread-android-sdk",
		"cupthread-react-native-sdk",
		"cupthread-flutter-sdk",
	} {
		t.Run(skill, func(t *testing.T) {
			doc := readSkill(t, skill)
			for _, marker := range sdkMarkers {
				if !strings.Contains(doc, marker) {
					t.Errorf("%s/SKILL.md is missing required issue-#262 marker %q", skill, marker)
				}
			}
			for _, key := range removedKeys {
				if !strings.Contains(doc, key) {
					t.Errorf("%s/SKILL.md is missing removed internal key %s", skill, key)
				}
			}
		})
	}
}

// TestSkillDocsCommandReferencesResolve mechanically re-validates every
// command and flag reference in the six skills/*/SKILL.md files against the
// real cobra command tree (issue #176). Every other skill-doc test in this
// file pins one specific claim somebody thought to check, which is how the
// nonexistent `imports run --options` survived the doc-sync commits; this is
// the general invariant. Two reference shapes are collected — inline
// backtick spans (the absolute `cupthread …` form and the bare relative form
// whose first token is a top-level command, the two shapes the offender
// used) and fenced code-block lines that invoke `cupthread …` — and each is
// resolved by walking the tree: a plain word under a command group must be a
// registered subcommand or alias, and every `--flag` in a reference must
// exist on the resolved command, persistent flags included. Alternatives
// written `a/b` or `a|b` resolve to the union of matching branches, and a
// documented flag must exist on every resolved branch. Placeholders
// (`<id>`, `[flags]`, `…`), plain arguments of leaf commands, tokens
// directly after a flag, and pipeline stages after `|` are not part of the
// command path. Per-file canaries keep the extractor honest: a refactor
// that stops matching references must fail here, not pass vacuously.
func TestSkillDocsCommandReferencesResolve(t *testing.T) {
	root := newRootCmd()
	root.InitDefaultVersionFlag()
	var initHelp func(c *cobra.Command)
	initHelp = func(c *cobra.Command) {
		c.InitDefaultHelpFlag()
		for _, sub := range c.Commands() {
			initHelp(sub)
		}
	}
	initHelp(root)

	// The cli skill documents the piped signing form
	// `printf %s "$CUP_SDK_SECRET" | cupthread api sign-user-attrs …`;
	// pin that the extractor picks the cupthread stage out of the pipeline
	// instead of skipping the line.
	if got, ok := fencedCupthreadInvocation(`printf %s "$CUP_SDK_SECRET" | cupthread api sign-user-attrs --app-key app_demo12345 \`); !ok || got != "cupthread api sign-user-attrs --app-key app_demo12345" {
		t.Errorf("piped cupthread invocation not extracted: got %q, ok=%v", got, ok)
	}

	// A pipe inside a quoted flag value is not a pipeline separator, so the
	// whole invocation must survive extraction and the flag after the quoted
	// value must still be resolved — pin that a bogus flag there is caught.
	quoted := `cupthread changelog create --title 'Alpha|Beta' --bad-flag`
	if got, ok := fencedCupthreadInvocation(quoted); !ok || got != quoted {
		t.Errorf("quoted-pipe invocation truncated: got %q, ok=%v", got, ok)
	}
	if err := resolveSkillCommandRef(root, quoted); err == nil || !strings.Contains(err.Error(), "--bad-flag") {
		t.Errorf("flag after quoted pipe not resolved: err=%v", err)
	}
	if err := resolveSkillCommandRef(root, `cupthread changelog create --title 'Alpha|Beta'`); err != nil {
		t.Errorf("valid quoted-pipe reference rejected: %v", err)
	}
	if got := unquotedCommentIndex(`cupthread changelog create --title 'Issue #176' --body-file ./b.md`); got >= 0 {
		t.Errorf("`#` inside single-quoted value treated as comment start: index %d", got)
	}

	// An escaped double quote does not close a double-quoted value, so the
	// embedded pipe must not split and the trailing flag stays resolvable.
	escaped := `cupthread changelog create --title "a\"b|c" --bad-flag`
	if got, ok := fencedCupthreadInvocation(escaped); !ok || got != escaped {
		t.Errorf("escaped-quote invocation truncated: got %q, ok=%v", got, ok)
	}
	if err := resolveSkillCommandRef(root, escaped); err == nil || !strings.Contains(err.Error(), "--bad-flag") {
		t.Errorf("flag after escaped-quote value not resolved: err=%v", err)
	}
	// A `#` after an escaped blank is a plain character mid-word, not a
	// comment: the line must not be truncated at the `#`.
	escComment := `cupthread changelog create --title hello\ #176 --bad-flag`
	if got := unquotedCommentIndex(escComment); got >= 0 {
		t.Errorf("`#` after escaped blank treated as comment start: index %d", got)
	}
	if err := resolveSkillCommandRef(root, escComment); err == nil || !strings.Contains(err.Error(), "--bad-flag") {
		t.Errorf("flag after escaped-blank value not resolved: err=%v", err)
	}

	minRefs := map[string]int{
		"cupthread-android-sdk":      2,
		"cupthread-api":              20,
		"cupthread-cli":              120,
		"cupthread-flutter-sdk":      2,
		"cupthread-react-native-sdk": 2,
		"cupthread-swift-sdk":        2,
	}
	skills := []string{
		"cupthread-android-sdk",
		"cupthread-api",
		"cupthread-cli",
		"cupthread-flutter-sdk",
		"cupthread-react-native-sdk",
		"cupthread-swift-sdk",
	}
	for _, skill := range skills {
		refs := extractSkillCommandRefs(t, root, skill)
		if len(refs) < minRefs[skill] {
			t.Errorf("%s/SKILL.md: command-reference extractor found only %d references, want at least %d — the extraction rules drifted", skill, len(refs), minRefs[skill])
		}
		for _, r := range refs {
			if err := resolveSkillCommandRef(root, r.text); err != nil {
				t.Errorf("%s: %v", r.loc, err)
			}
		}
	}
}

// skillCommandRef is one extracted command reference with its source location
// (`<repo-relative file>:<1-based line>`), for actionable failure messages.
type skillCommandRef struct {
	loc  string
	text string
}

// extractSkillCommandRefs collects the command references of one SKILL.md:
// inline backtick spans whose first token is `cupthread` or a top-level
// command, and fenced code-block lines that invoke `cupthread …`.
func extractSkillCommandRefs(t *testing.T, root *cobra.Command, skill string) []skillCommandRef {
	t.Helper()
	var refs []skillCommandRef
	for _, path := range skillDocFiles(t, skill) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read skill doc %s: %v", path, err)
		}
		// Label refs with the path relative to the repo root so failures
		// point at the right file, whether the reference lives in SKILL.md
		// or a reference/*.md detail file.
		label, err := filepath.Rel(filepath.Join("..", ".."), path)
		if err != nil {
			t.Fatalf("relativize %s: %v", path, err)
		}
		inFence := false
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			if inFence {
				if invocation, ok := fencedCupthreadInvocation(line); ok {
					refs = append(refs, skillCommandRef{label + ":" + strconv.Itoa(i+1), invocation})
				}
				continue
			}
			for _, span := range inlineCodeSpans(line) {
				toks := strings.Fields(span)
				if len(toks) == 0 {
					continue
				}
				if toks[0] == "cupthread" || findChildCommand(root, toks[0]) != nil {
					refs = append(refs, skillCommandRef{label + ":" + strconv.Itoa(i+1), span})
				}
			}
		}
	}
	return refs
}

// inlineCodeSpans returns the `…` inline code spans of one markdown line.
func inlineCodeSpans(line string) []string {
	var spans []string
	for {
		start := strings.Index(line, "`")
		if start < 0 {
			return spans
		}
		rest := line[start+1:]
		end := strings.Index(rest, "`")
		if end < 0 {
			return spans
		}
		spans = append(spans, rest[:end])
		line = rest[end+1:]
	}
}

// fencedCupthreadInvocation strips shell decoration from a fenced code-block
// line (a `$` prompt and an unquoted trailing `#` comment), splits the rest
// into pipeline stages at unquoted `|`, and returns the first stage that
// invokes the cupthread binary, trailing `\` line-continuations removed — so
// piped forms like `printf %s "$S" | cupthread api …` are validated too.
func fencedCupthreadInvocation(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "$ "))
	if i := unquotedCommentIndex(trimmed); i >= 0 {
		trimmed = trimmed[:i]
	}
	for _, stage := range splitUnquotedPipes(trimmed) {
		stage = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(stage), "\\"))
		if stage == "cupthread" || strings.HasPrefix(stage, "cupthread ") {
			return stage, true
		}
	}
	return "", false
}

// shellLexer walks a shell command line byte by byte so both doc-lexer
// consumers share one shell-semantics implementation. next classifies line[i]:
// quoted is true when the byte sits inside a single- or double-quoted value
// (single-quoted spans have no escape sequences; inside double quotes a
// backslash escapes only `"` and `\`), escaped is true when the byte is made
// literal by a backslash outside quotes.
type shellLexer struct {
	quote  rune // 0, '\'' or '"'
	escape bool // the next byte is escaped
}

func (lx *shellLexer) next(line string, i int) (c byte, quoted, escaped bool) {
	c = line[i]
	escaped = lx.escape
	lx.escape = false
	switch {
	case escaped:
		// literal byte: quote state unchanged
	case lx.quote == '\'':
		quoted = true
		if c == '\'' {
			lx.quote = 0
		}
	case lx.quote == '"':
		quoted = true
		switch c {
		case '\\':
			if i+1 < len(line) && (line[i+1] == '"' || line[i+1] == '\\') {
				lx.escape = true
			}
		case '"':
			lx.quote = 0
		}
	case c == '\'' || c == '"':
		lx.quote = rune(c)
		quoted = true
	case c == '\\':
		lx.escape = true
	}
	return c, quoted, escaped
}

// splitUnquotedPipes splits a shell line at `|` separators that the shell
// would treat as pipeline separators: outside quoted values and not
// backslash-escaped.
func splitUnquotedPipes(line string) []string {
	var stages []string
	var lx shellLexer
	start := 0
	for i := 0; i < len(line); i++ {
		c, quoted, escaped := lx.next(line, i)
		if c == '|' && !quoted && !escaped {
			stages = append(stages, line[start:i])
			start = i + 1
		}
	}
	return append(stages, line[start:])
}

// unquotedCommentIndex finds the start of a `#` comment, or -1. The shell
// starts a comment only at the beginning of a new word: the `#` must be
// unquoted, unescaped, and preceded by line start or an unescaped blank —
// `--title hello\ #176` is a single word whose `#` is a plain character, so
// the flags after it stay available for validation.
func unquotedCommentIndex(line string) int {
	var lx shellLexer
	prevEscaped := false
	for i := 0; i < len(line); i++ {
		c, quoted, escaped := lx.next(line, i)
		if c == '#' && !quoted && !escaped {
			if i == 0 || ((line[i-1] == ' ' || line[i-1] == '\t') && !prevEscaped) {
				return i
			}
		}
		prevEscaped = escaped
	}
	return -1
}

// resolveSkillCommandRef resolves one extracted reference against the cobra
// tree. See TestSkillDocsCommandReferencesResolve for the grammar.
func resolveSkillCommandRef(root *cobra.Command, reference string) error {
	toks := strings.Fields(reference)
	if len(toks) > 0 && toks[0] == "cupthread" {
		toks = toks[1:]
	}
	cur := []*cobra.Command{root}
	pathOpen := true
	prevWasFlag := false
	for _, tok := range toks {
		if tok == "|" {
			return nil // the rest of the line is another pipeline stage
		}
		if tok == "-" || tok == "--" { // stdin value / flag separator
			prevWasFlag = false
			pathOpen = false
			continue
		}
		if strings.HasPrefix(tok, "-") {
			if err := requireSkillRefFlag(cur, tok, reference); err != nil {
				return err
			}
			prevWasFlag = true
			continue
		}
		afterFlag := prevWasFlag
		prevWasFlag = false
		if afterFlag || !pathOpen {
			continue // a token directly after a flag is that flag's value, not a path segment
		}
		if strings.ContainsAny(tok, "<>[{…") {
			pathOpen = false // placeholder argument
			continue
		}
		var next []*cobra.Command
		hasGroup := false
		for _, c := range cur {
			if len(c.Commands()) > 0 {
				hasGroup = true
			}
			for _, alt := range strings.FieldsFunc(tok, func(r rune) bool { return r == '/' || r == '|' }) {
				if sub := findChildCommand(c, alt); sub != nil {
					next = append(next, sub)
				}
			}
		}
		if len(next) == 0 {
			if hasGroup {
				return fmt.Errorf("command reference %q: %q does not resolve to a registered subcommand", reference, tok)
			}
			pathOpen = false // plain argument of a leaf command
			continue
		}
		cur = next
	}
	return nil
}

// requireSkillRefFlag asserts that the flag token (`--name`, `--name=value`,
// `-s`, or a combined shorthand) resolves on every current command branch,
// walking up through persistent flags.
func requireSkillRefFlag(cur []*cobra.Command, tok, reference string) error {
	if strings.HasPrefix(tok, "--") {
		name := strings.SplitN(tok[2:], "=", 2)[0]
		for _, c := range cur {
			if !commandHasLongFlag(c, name) {
				return fmt.Errorf("command reference %q: flag --%s does not exist on %q", reference, name, skillRefCommandPath(c))
			}
		}
		return nil
	}
	shorthand := strings.SplitN(tok[1:], "=", 2)[0]
	if shorthand == "" {
		return nil
	}
	for _, c := range cur {
		if !commandHasShortFlag(c, shorthand[:1]) {
			return fmt.Errorf("command reference %q: flag %s does not exist on %q", reference, tok, skillRefCommandPath(c))
		}
	}
	return nil
}

func commandHasLongFlag(c *cobra.Command, name string) bool {
	if c.Flags().Lookup(name) != nil {
		return true
	}
	for x := c; x != nil; x = x.Parent() {
		if x.PersistentFlags().Lookup(name) != nil {
			return true
		}
	}
	return false
}

func commandHasShortFlag(c *cobra.Command, shorthand string) bool {
	if c.Flags().ShorthandLookup(shorthand) != nil {
		return true
	}
	for x := c; x != nil; x = x.Parent() {
		if x.PersistentFlags().ShorthandLookup(shorthand) != nil {
			return true
		}
	}
	return false
}

// findChildCommand returns the direct subcommand of c matching name,
// aliases included.
func findChildCommand(c *cobra.Command, name string) *cobra.Command {
	for _, sub := range c.Commands() {
		if sub.Name() == name {
			return sub
		}
		for _, alias := range sub.Aliases {
			if alias == name {
				return sub
			}
		}
	}
	return nil
}

func skillRefCommandPath(c *cobra.Command) string {
	if c.Parent() == nil {
		return c.Name()
	}
	return skillRefCommandPath(c.Parent()) + " " + c.Name()
}

// skillLineByPrefix returns the first line of doc that starts with prefix,
// failing the test when no line does.
func skillLineByPrefix(t *testing.T, doc, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("no line starts with %q", prefix)
	return ""
}

// skillSection returns the body of the "## <heading>" section, up to the next
// "## " heading, failing the test when the heading is absent.
func skillSection(t *testing.T, doc, heading string) string {
	t.Helper()
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("no section heading %q", heading)
	}
	rest := doc[start+len(heading):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// TestSkillDocsIssue177VoteRoadmapGate pins the PRIV-12 roadmap-surface gate
// on the vote writes (issue #177, SaaS PR #618/#646): both vote routes answer
// the uniform `401 authentication_required` on boards with anonymous roadmap
// view disabled — even when allowAnonymousVote is on — and the gate runs
// ahead of the feature-request existence lookup, so missing and approved ids
// are indistinguishable and no voteCount is served. The cupthread-api skill
// must document the gate on both vote endpoint rows and in the PRIV-12
// enforcement table, and all four SDK skills must tell custom-voting-UI
// authors to surface the sign-in flow instead of retrying.
// Red-before/green-after: reverting any one of the phrases below must fail
// this test.
func TestSkillDocsIssue177VoteRoadmapGate(t *testing.T) {
	t.Run("cupthread-api", func(t *testing.T) {
		doc := readSkill(t, "cupthread-api")

		// Each vote endpoint row must carry the gate note with its ordering.
		rowRequirements := []struct{ rowPrefix, want string }{
			{
				rowPrefix: "| `/api/v1/feature-requests/:id/vote` | `POST` |",
				want:      "even when anonymous voting is enabled",
			},
			{
				rowPrefix: "| `/api/v1/feature-requests/:id/vote` | `DELETE` |",
				want:      "even when anonymous voting is enabled",
			},
		}
		for _, req := range rowRequirements {
			row := skillLineByPrefix(t, doc, req.rowPrefix)
			if !strings.Contains(row, req.want) {
				t.Errorf("vote endpoint row %q must pin the PRIV-12 gate (%q); got: %s", req.rowPrefix, req.want, row)
			}
			if !strings.Contains(row, `"code": "authentication_required"`) {
				t.Errorf("vote endpoint row %q must name the uniform 401 body code; got: %s", req.rowPrefix, row)
			}
			if !strings.Contains(row, "the gate runs before the feature-request existence lookup") {
				t.Errorf("vote endpoint row %q must pin the gate's ordering ahead of the existence lookup; got: %s", req.rowPrefix, row)
			}
		}

		// The PRIV-12 enforcement table must list both vote writes.
		priv12Rows := []struct{ rowPrefix, want string }{
			{
				rowPrefix: "| `POST /api/v1/feature-requests/:id/vote` | anonymous write, `allowAnonymousRoadmap` off |",
				want:      "it fires even when `allowAnonymousVote` is on",
			},
			{
				rowPrefix: "| `DELETE /api/v1/feature-requests/:id/vote` | anonymous write, `allowAnonymousRoadmap` off |",
				want:      "same ordering ahead of the existence lookup",
			},
		}
		for _, req := range priv12Rows {
			row := skillLineByPrefix(t, doc, req.rowPrefix)
			if !strings.Contains(row, "`401` `authentication_required`") {
				t.Errorf("PRIV-12 table row %q must document 401 authentication_required; got: %s", req.rowPrefix, row)
			}
			if !strings.Contains(row, req.want) {
				t.Errorf("PRIV-12 table row %q is missing required claim %q; got: %s", req.rowPrefix, req.want, row)
			}
		}

		for _, marker := range []string{
			// The gate ordering contrast in the 401-body paragraph.
			"The two vote-write gates are the strictest of the family: they run **ahead of** the feature-request existence lookup, so a gated board also keeps its missing-vs-approved oracle shut on the write paths — missing and approved ids both answer the `401`, never a `404`, and no `voteCount` is served.",
			// The client-guidance bullet for the vote writes.
			"Treat `401` + `code: \"authentication_required\"` on a vote `POST`/`DELETE` the same way: it is **sign-in required**, not a credentials bug — surface the host app's sign-in flow instead of retrying",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-api/SKILL.md is missing required issue-#177 marker %q", marker)
			}
		}
	})

	// Every SDK skill's vote section must warn custom-voting-UI authors about
	// the gated-board 401 and the sign-in-flow handling.
	for _, skill := range []string{
		"cupthread-swift-sdk",
		"cupthread-android-sdk",
		"cupthread-react-native-sdk",
		"cupthread-flutter-sdk",
	} {
		t.Run(skill, func(t *testing.T) {
			doc := readSkill(t, skill)
			vote := skillSection(t, doc, "## Feature Request Vote Rate Limits (429 Too Many Requests)")
			for _, marker := range []string{
				"On boards with anonymous roadmap view disabled, anonymous vote `POST`/`DELETE` answer `401 authentication_required` (even when anonymous voting is enabled)",
				"surface the host app's sign-in flow instead of retrying",
			} {
				if !strings.Contains(vote, marker) {
					t.Errorf("%s/SKILL.md vote section is missing required issue-#177 marker %q", skill, marker)
				}
			}
		})
	}
}

// TestSkillDocsIssue261PseudonymOnlyProfiles pins the issue-#261 sync of
// pseudonym-only public profile resolution (SaaS #1322): GET
// /api/v1/users/{userId}/profile resolves app-scoped u_* ids only, and every
// non-u_* identifier — including raw Clerk user_* ids from legacy /u/ links
// — answers the anonymous placeholder (200, displayName null, empty
// publicApps/recentComments) even for opted-in users. The CLI rejects
// user_* ids up front, and the api/cli skills plus all four SDK skills must
// not claim the old legacy-id resolution anymore.
func TestSkillDocsIssue261PseudonymOnlyProfiles(t *testing.T) {
	t.Run("command help", func(t *testing.T) {
		profile := newUsersProfileCmd()
		for _, marker := range []string{
			"must be the app-scoped pseudonym",
			"no longer resolved (issue #261)",
			"rejects them up front",
		} {
			if !strings.Contains(profile.Long, marker) {
				t.Errorf("users profile Long missing issue-#261 marker %q:\n%s", marker, profile.Long)
			}
		}
	})
	t.Run("cupthread-api", func(t *testing.T) {
		doc := readSkill(t, "cupthread-api")
		for _, marker := range []string{
			"resolves app-scoped `u_*` pseudonyms only",
			"any raw Clerk `user_*` id answers the anonymous placeholder",
			"including raw Clerk `user_*` ids from legacy `/u/` links",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-api docs are missing required issue-#261 marker %q", marker)
			}
		}
	})
	t.Run("cupthread-cli", func(t *testing.T) {
		doc := readSkill(t, "cupthread-cli")
		for _, marker := range []string{
			"Raw Clerk `user_*` ids no longer work at all (issue #261)",
			"rejects `user_*` arguments up front",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-cli/SKILL.md is missing required issue-#261 marker %q", marker)
			}
		}
	})
	// The superseded wording must not come back anywhere: the api reference
	// claiming legacy ids remain accepted, the cli skill claiming they still
	// work, or an SDK skill keeping the old opt-in placeholder phrasing.
	staleMarkers := []string{
		"Legacy `user_*` ids remain accepted without `appKey`",
		"Legacy `user_*` ids still work without it",
		"keep the opt-in placeholder behavior",
	}
	for _, skill := range []string{
		"cupthread-api",
		"cupthread-cli",
		"cupthread-swift-sdk",
		"cupthread-android-sdk",
		"cupthread-react-native-sdk",
		"cupthread-flutter-sdk",
	} {
		t.Run(skill, func(t *testing.T) {
			doc := readSkill(t, skill)
			for _, stale := range staleMarkers {
				if strings.Contains(doc, stale) {
					t.Errorf("%s docs still document the incorrect legacy-id resolution %q", skill, stale)
				}
			}
		})
	}
	// Every SDK skill's profile section must state the always-placeholder
	// consequence for raw user_* ids and the u_* remediation.
	for _, skill := range []string{
		"cupthread-swift-sdk",
		"cupthread-android-sdk",
		"cupthread-react-native-sdk",
		"cupthread-flutter-sdk",
	} {
		t.Run(skill, func(t *testing.T) {
			doc := readSkill(t, skill)
			profile := skillSection(t, doc, "## Public Profile Rate Limits & Unknown-User 404 (SEC-34)")
			for _, marker := range []string{
				"now always answer that placeholder — even for opted-in users",
				"switch legacy `/u/` flows to the `u_*` id carried by board and comment payloads",
			} {
				if !strings.Contains(profile, marker) {
					t.Errorf("%s/SKILL.md profile section is missing required issue-#261 marker %q", skill, marker)
				}
			}
		})
	}
}

// TestSkillDocsIssue260PresentedSpellingHMAC pins the issue-#260 sync of
// CupThread/SaaS#1271 (PR SaaS #1274): the DATA-03 HMAC is verified over the
// userToken exactly as presented — byte-for-byte, mixed-case UUIDs included —
// and the route folds the token to lowercase only after the signature
// verifies, solely for identity resolution, so the stored profile is keyed by
// the lowercased spelling. The signing-contract reference, the compact
// API-skill section, the CLI skill, the sign-user-attrs command help, and all
// four SDK skills must teach the sign-and-send-the-same-spelling rule and the
// 401 invalid_signature outcome of a spelling mismatch.
func TestSkillDocsIssue260PresentedSpellingHMAC(t *testing.T) {
	t.Run("cupthread-api", func(t *testing.T) {
		doc := readSkill(t, "cupthread-api")
		for _, marker := range []string{
			// Reference contract bullet (reference/payment-attribute-signing.md).
			"The server verifies the HMAC over that token **exactly as presented** (byte-for-byte, mixed-case UUIDs included)",
			"folds it to lowercase only **after** the signature verifies, solely for identity resolution",
			"So sign and send the **same** spelling",
			"Signing one spelling and presenting another fails `401 invalid_signature`",
			"No client change is required for tokens already minted lowercase.",
			// invalid_signature table-row trigger.
			"or a `userToken` spelling different from the one signed (the HMAC is verified over the token exactly as presented)",
			// Compact DATA-03 section in SKILL.md.
			"The HMAC is verified over the `userToken` **exactly as presented** — sign and send the same spelling (mixed-case UUIDs included); the server folds it to lowercase only for identity storage",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-api docs are missing required issue-#260 marker %q", marker)
			}
		}
	})

	t.Run("cli-command-help", func(t *testing.T) {
		sign := newAPISignUserAttrsCmd()
		for _, marker := range []string{
			"userToken exactly as presented, then folds it to lowercase only for",
			"identity storage — sign and send the same spelling (mixed-case UUIDs",
			"included); signing one spelling and sending another fails 401",
			"invalid_signature (issue #260).",
		} {
			if !strings.Contains(sign.Long, marker) {
				t.Errorf("sign-user-attrs help is missing required issue-#260 marker %q", marker)
			}
		}
	})

	t.Run("cupthread-cli", func(t *testing.T) {
		doc := readSkill(t, "cupthread-cli")
		for _, marker := range []string{
			"The server verifies the HMAC over the `userToken` **exactly as presented** — sign and send the same spelling (mixed-case UUIDs included); it folds the token to lowercase only for identity storage",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-cli/SKILL.md is missing required issue-#260 marker %q", marker)
			}
		}
	})

	sdkMarker := "The server verifies the HMAC over the token **exactly as presented** — send the same spelling you signed (mixed-case UUIDs included); it folds the token to lowercase only for identity storage, so signing one spelling and sending another fails `401 invalid_signature` (issue #260)."
	for _, skill := range []string{
		"cupthread-swift-sdk",
		"cupthread-android-sdk",
		"cupthread-react-native-sdk",
		"cupthread-flutter-sdk",
	} {
		t.Run(skill, func(t *testing.T) {
			doc := readSkill(t, skill)
			if !strings.Contains(doc, sdkMarker) {
				t.Errorf("%s/SKILL.md is missing required issue-#260 marker %q", skill, sdkMarker)
			}
		})
	}
}

// TestSkillDocsIssue259PublicChangelogFeedSchema pins the corrected public
// changelog feed contract (issue #259, SaaS #1226 / PR #1273): entries carry
// exactly `{id, title, body, versionLabel, publishedAt, linkedRequests}` — no
// workspace-internal `appId`, nullable free-form `versionLabel` (never
// `versionNumber`), and an always-present `linkedRequests` array — and the
// API reference documents the deterministic 400/401/403/404 errors alongside
// 429 plus the always-emitted envelope.
func TestSkillDocsIssue259PublicChangelogFeedSchema(t *testing.T) {
	t.Run("cupthread-api", func(t *testing.T) {
		doc := readSkill(t, "cupthread-api")
		for _, marker := range []string{
			// Compact index row (SKILL.md) — pointer only; the field-level
			// detail stays in reference/endpoints.md per the #318 budget.
			"corrected entry schema in [reference/endpoints.md](reference/endpoints.md) (SaaS #1226)",
			// Corrected entry schema (reference/endpoints.md).
			"the corrected `ChangelogEntry` schema (SaaS #1226 / PR #1273): `{id, title, body, versionLabel, publishedAt, linkedRequests}`, all six required",
			"no `appId` (a workspace-internal identifier the public feed never serves)",
			"the version field is the operator's free-form `versionLabel` (nullable string, **not** `versionNumber`)",
			"`linkedRequests` is an always-present array of `{id, title}` approved feature-request summaries (empty for callers who fail the roadmap visibility gate — it is required, not optional)",
			// Always-emitted envelope and deterministic errors.
			"the envelope always emits all three keys, with `hasMore: false` and `nextCursor: null` on the last page",
			"`400` `{\"error\": \"Invalid cursor\"}` for a malformed `cursor`",
			"`401` `authentication_required` when the app's `allowAnonymousChangelog` is false and no end-user session is present",
			"`403` `{\"error\": \"Public surfaces are disabled for this app\"}` for a private app",
			"`404` for an unknown `appKey`",
			"`429` when the rate budget is spent",
		} {
			if !strings.Contains(doc, marker) {
				t.Errorf("cupthread-api docs are missing required issue-#259 marker %q", marker)
			}
		}
	})

	sdkMarkers := []string{
		"**Public changelog entries follow the corrected `ChangelogEntry` schema (SaaS #1226 / PR #1273):**",
		"no `appId` (a workspace-internal identifier the public feed never serves), a nullable free-form `versionLabel` (never `versionNumber`)",
		"Drop `appId`/`versionNumber` from any custom or strict changelog model and require all six keys.",
	}
	for _, skill := range []string{
		"cupthread-swift-sdk",
		"cupthread-android-sdk",
		"cupthread-react-native-sdk",
		"cupthread-flutter-sdk",
	} {
		t.Run(skill, func(t *testing.T) {
			doc := readSkill(t, skill)
			for _, marker := range sdkMarkers {
				if !strings.Contains(doc, marker) {
					t.Errorf("%s/SKILL.md is missing required issue-#259 marker %q", skill, marker)
				}
			}
		})
	}
}

// TestSkillDocsIssue248FeedbackIntakeContract pins the corrected public
// intake contracts (issue #248, SaaS #1117 / PR #1119): POST /api/v1/feedback
// requires `platform` (exactly ios/macos/android/universal — never the
// documented-but-rejected web/other) and `appKey`, carries the corrected
// field bounds with no `systemVersion`, and documents its full deterministic
// status-code set (400/401/402/403/404/413/429) — while POST
// /api/v1/feature-requests/{id}/comments takes a `body` of at most 2000
// chars and no author identity fields (the server derives and strips them).
func TestSkillDocsIssue248FeedbackIntakeContract(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		// Corrected FeedbackSubmissionInput schema (reference/endpoints.md).
		"Corrected request schema `FeedbackSubmissionInput` (SaaS #1117 / PR #1119)",
		"`platform` is **required** and accepts exactly `\"ios\"`, `\"macos\"`, `\"android\"`, or `\"universal\"`",
		"the documented-but-rejected `\"web\"`/`\"other\"` values are removed and `\"universal\"` is the only cross-platform value",
		"`appKey` (8–128 chars) is **required**",
		"`systemVersion` was never accepted and is removed from the schema",
		"`title` 3–160, `description` 5–10000, `reporterName` 1–120 (optional, non-nullable), `reporterEmail` optional email (non-nullable)",
		"`appVersion`/`buildNumber` 1–64 (optional), `uploadIds` items 1–128 chars with at most 8 entries, `attachments` at most 8 entries",
		"omitting `metadata`, `uploadIds`, or `attachments` is equivalent to sending `{}`/`[]`/`[]`",
		// Full deterministic status-code set for POST /api/v1/feedback.
		"`402` the workspace's monthly submission quota is exceeded (`tier_limit_submissions`) or its subscription is inactive or canceled (`subscription_inactive`)",
		"`413` request body over the public JSON size limit (`payload_too_large`)",
		"`403` Turnstile verification failures **and**, additionally, \"public feedback is disabled for the app\"",
		"`400` schema validation (including a `platform` value the app does not allow) and attachment-reference failures (`direct_attachment_forbidden`, `uploader_mismatch`) — it no longer covers rate limiting",
		// Corrected CreateFeatureRequestCommentInput schema.
		"Corrected request schema `CreateFeatureRequestCommentInput` (SaaS #1117 / PR #1119)",
		"`body` max length is **2000** (the previously documented 4000 was wrong)",
		"the author fields `authorName`, `authorEmail`, `authorAvatarUrl`, and `replyToClerkId` are removed from the schema",
		"the server always derives author identity server-side and silently strips such request fields, so sending them is accepted but never stored",
		"`parentId` and `replyToAuthorName` remain optional string fields",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api docs are missing required issue-#248 marker %q", marker)
		}
	}
}

// TestSkillDocsIssue250Uniform404PublicWrites pins the uniform-404 existence
// hiding on the public roadmap surfaces (issue #250, SaaS #1165 / PR #1171):
// an unknown appKey and a known app with public access disabled answer the
// same uniform 404 (never a 403 "disabled" distinction), and a missing or
// unapproved feature-request id answers the same 404 on the public writes —
// so 403 on the vote route means Turnstile rejection only. The cupthread-api
// skill must document this on every affected endpoint row (board listing,
// vote POST/DELETE, comment create, public columns/versions) and in the
// shared-cache gates bullet, and must no longer claim "a private app still
// answers 403" for the cached roadmap reads (the changelog feed keeps its
// 403). The vote rows pin the documented body split — `App not found` for an
// unknown key or disabled app, `Feature request not found` for a missing or
// unapproved id — so neither route passes with the bodies swapped or lumped.
// Red-before/green-after: reverting any one of the phrases below must fail
// this test.
func TestSkillDocsIssue250Uniform404PublicWrites(t *testing.T) {
	doc := readSkill(t, "cupthread-api")

	const appNotFound404 = "`404 {\"error\": \"App not found\"}`"
	const requestNotFound404 = "`404 {\"error\": \"Feature request not found\"}`"

	rowRequirements := []struct {
		rowPrefix string
		want      []string
	}{
		{
			rowPrefix: "| `/api/v1/public/columns/:appKey` | `GET` |",
			want: []string{
				"An unknown `appKey` and a known app with public access disabled both answer the same uniform " + appNotFound404 + " (existence hiding, SaaS #1165/#1171)",
			},
		},
		{
			rowPrefix: "| `/api/v1/public/versions/:appKey` | `GET` |",
			want: []string{
				"Same uniform " + appNotFound404 + " for an unknown `appKey` and a known app with public access disabled",
			},
		},
		{
			// The longer prefix skips SKILL.md's compact index row (the #318
			// budget keeps field-level status codes in reference/endpoints.md).
			rowPrefix: "| `/api/v1/feature-requests` | `GET` | List/search feature requests",
			want: []string{
				"An unknown `appKey` and a known app with public access disabled both answer the same uniform " + appNotFound404 + " (existence hiding, SaaS #1165/#1171)",
			},
		},
		{
			// Both vote routes must pin the documented body split: the
			// unknown-key/disabled-app case answers `App not found` while the
			// missing-or-unapproved case answers `Feature request not found`,
			// so a regression that swaps or lumps the two bodies fails here.
			rowPrefix: "| `/api/v1/feature-requests/:id/vote` | `POST` |",
			want: []string{
				"a `403` from this route now means **only** Turnstile human-verification rejection",
				"both answer " + appNotFound404 + ", and a missing or **unapproved** feature-request id answers " + requestNotFound404,
			},
		},
		{
			rowPrefix: "| `/api/v1/feature-requests/:id/vote` | `DELETE` |",
			want: []string{
				"a missing or **unapproved** feature-request id answers " + requestNotFound404,
				"both answer " + appNotFound404 + ", and a missing or **unapproved** feature-request id answers " + requestNotFound404,
			},
		},
		{
			rowPrefix: "| `/api/v1/feature-requests/:id/comments` | `POST` | Post a comment",
			want: []string{
				"a missing id, an id on an app with public access disabled, and an **unapproved** request all answer the same uniform " + requestNotFound404,
			},
		},
	}
	for _, req := range rowRequirements {
		row := skillLineByPrefix(t, doc, req.rowPrefix)
		if !strings.Contains(row, appNotFound404) && !strings.Contains(row, requestNotFound404) {
			t.Errorf("endpoint row %q must document a uniform 404 body; got: %s", req.rowPrefix, row)
		}
		for _, want := range req.want {
			if !strings.Contains(row, want) {
				t.Errorf("endpoint row %q is missing required issue-#250 claim %q; got: %s", req.rowPrefix, want, row)
			}
		}
	}

	// The comment-create row must also pin the Clerk gate ordering and the
	// removed undocumented 403.
	commentsRow := skillLineByPrefix(t, doc, "| `/api/v1/feature-requests/:id/comments` | `POST` | Post a comment")
	for _, marker := range []string{
		"`401 {\"error\": \"Sign in is required to comment\", \"code\": \"authentication_required\"}` **before** the lookup",
		"the previously undocumented `403` \"Only approved requests can be commented on\" is gone",
	} {
		if !strings.Contains(commentsRow, marker) {
			t.Errorf("comment-create row is missing required issue-#250 marker %q; got: %s", marker, commentsRow)
		}
	}

	// The shared-cache gates bullet must split the private-app answer:
	// roadmap reads answer the uniform 404, the changelog feed keeps its 403.
	gatesBullet := skillLineByPrefix(t, doc, "- **Gates run before the cache read.**")
	for _, marker := range []string{
		"Since SaaS #1165/#1171 the columns and versions reads answer the same uniform " + appNotFound404,
		"The changelog feed is the exception: it still answers `403 {\"error\": \"Public surfaces are disabled for this app\"}` for a private app",
	} {
		if !strings.Contains(gatesBullet, marker) {
			t.Errorf("shared-cache gates bullet is missing required issue-#250 marker %q; got: %s", marker, gatesBullet)
		}
	}
	if strings.Contains(doc, "a private app still answers `403`") {
		t.Error("cupthread-api docs still claim \"a private app still answers 403\" for the cached roadmap reads (issue #250: they answer the uniform 404)")
	}
}

// TestSkillDocsIssue256IntakeResponseShapes pins the corrected public intake
// response shapes (issue #256, SaaS #1105): POST /api/v1/feature-requests
// answers a two-field `FeatureRequestSubmissionCreated` receipt (never the
// full public board record), and POST
// /api/v1/feature-requests/{id}/comments wraps the created comment in the
// `FeatureRequestCommentCreated` `{ comment }` envelope (never the bare
// record). The corrected comments-POST 401/429 documentation lives in the
// issue-#250 uniform-404 sync, so this test pins only the 201 shapes.
// Red-before/green-after: reverting either phrase below must fail this test.
func TestSkillDocsIssue256IntakeResponseShapes(t *testing.T) {
	doc := readSkill(t, "cupthread-api")

	submitRow := skillLineByPrefix(t, doc, "| `/api/v1/feature-requests` | `POST` | Submit (propose) a new feature request.")
	for _, marker := range []string{
		"Success answers `201` with the two-field intake receipt `FeatureRequestSubmissionCreated` (SaaS #1105): `{featureRequestId, pending}`, both required",
		"a strict client must not model the full public `FeatureRequest` board record here",
	} {
		if !strings.Contains(submitRow, marker) {
			t.Errorf("submit row is missing required issue-#256 marker %q; got: %s", marker, submitRow)
		}
	}

	commentsRow := skillLineByPrefix(t, doc, "| `/api/v1/feature-requests/:id/comments` | `POST` | Post a comment")
	for _, marker := range []string{
		"Success answers `201` with the `FeatureRequestCommentCreated` envelope `{ comment }` (SaaS #1105)",
		"`comment` is required and holds the full public `FeatureRequestComment` record inside the wrapper — **not** the bare record the spec previously referenced",
	} {
		if !strings.Contains(commentsRow, marker) {
			t.Errorf("comment-create row is missing required issue-#256 marker %q; got: %s", marker, commentsRow)
		}
	}
}

// TestSkillDocsIssue319ShipNotifyVerificationFailedWarning pins the
// SaaS#1472/#1387 warning on the vote response (issues #319/#320): when an
// anonymous voter opts in via shipNotifyEmail but the single Turnstile
// verdict (SEC-90) is missing or rejected on a board without the hard SEC-846
// vote gate, the vote still answers 200 {voted, voteCount} — but the refusal
// now rides the 200 as warning.code "ship_notify_verification_failed" (same
// shape as the signed-in email_not_verified mismatch) instead of the old
// silent suppression. The cupthread-api skill must document the observable
// warning and must no longer claim "no warning field" / "silently
// suppressed", and each SDK skill's repeated shipNotify bullet must carry the
// new code. Red-before/green-after: reverting any of the doc phrases below
// must fail this test.
func TestSkillDocsIssue319ShipNotifyVerificationFailedWarning(t *testing.T) {
	doc := readSkill(t, "cupthread-api")

	const warningCode = "`ship_notify_verification_failed`"
	const warningError = "Human verification failed, so the ship-notification opt-in was not recorded. Complete the verification and try again."

	for _, marker := range []string{
		"the refusal is observable (SaaS #1472): the response carries a `warning` object with `code: \"ship_notify_verification_failed\"`",
		"Treat `ship_notify_verification_failed` as \"opt-in refused (verification)\"",
		warningError,
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api docs are missing required issue-#319 marker %q", marker)
		}
	}

	voteRow := skillLineByPrefix(t, doc, "| `/api/v1/feature-requests/:id/vote` | `POST` |")
	if !strings.Contains(voteRow, "the response gains `warning {\"code\": \"ship_notify_verification_failed\", \"error\": …}` (SaaS #1472)") {
		t.Errorf("vote POST row is missing required issue-#319 warning marker; got: %s", voteRow)
	}

	for _, stale := range []string{
		"with no warning field",
		"with no `warning` field",
		"silently suppressed",
	} {
		if strings.Contains(doc, stale) {
			t.Errorf("cupthread-api docs still claim %q (issue #319: the refusal now rides the 200 as warning.code ship_notify_verification_failed)", stale)
		}
	}

	for _, sdk := range []string{"cupthread-swift-sdk", "cupthread-android-sdk", "cupthread-react-native-sdk", "cupthread-flutter-sdk"} {
		sdkDoc := readSkill(t, sdk)
		for _, marker := range []string{warningCode, warningError} {
			if !strings.Contains(sdkDoc, marker) {
				t.Errorf("%s docs are missing required issue-#319 marker %q", sdk, marker)
			}
		}
		for _, stale := range []string{"with no `warning` field", "silently suppressed"} {
			if strings.Contains(sdkDoc, stale) {
				t.Errorf("%s docs still claim %q (issue #319: the refusal is observable now)", sdk, stale)
			}
		}
	}
}

// TestSkillDocsIssue322ErrorResponseDetails pins the issue #322 sync (SaaS
// #1475): the OpenAPI spec now documents the optional `details` object on
// `ErrorResponse` — the zod flatten() output served with every validation
// `400` and only there — and the cupthread-api skill must mirror it so
// agents render per-field validation reasons instead of the generic
// message. The CLI side needs no change: APIError.Details already carries
// the raw flatten payload (client_test.go pins that), and SubmissionDetail
// already types app + activity.
func TestSkillDocsIssue322ErrorResponseDetails(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		"Validation `400`s (`\"error\": \"Validation failed\"`) additionally carry an optional `details` object",
		"the zod `flatten()` output `{formErrors: string[], fieldErrors: Record<fieldName, string[]>}` (SaaS #1475)",
		"it is absent on every other error",
		"render the per-field reasons from it when present instead of only the generic message",
		"The CLI's `APIError` already exposes it verbatim as `Details`",
		"validation `400`s additionally carry the optional zod-flatten `details` object, never a `402`",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api docs are missing required issue-#322 marker %q", marker)
		}
	}
}

// TestSkillDocsIssue321ResponseSchemaSync pins the issue #321 sync (SaaS
// #1474): the OpenAPI spec now documents wire shapes the live API always
// served but the spec omitted, and the cupthread-api skill must mirror them.
// Three corrections are pinned: (1) the POST /api/v1/uploads/sessions 201
// envelope always serves six top-level keys — the nested session descriptor
// and per-file client-handle files[] array plus the legacy flat mirror
// (sessionId/sessionToken/expiresAt and the intake-descriptor uploads[]
// array, a different item shape than files[]); (2) POST /api/v1/feedback
// serves three success statuses (200 inline GitHub forward, 201 inbox store,
// 202 queued-retry forward) over one shared FeedbackSubmissionResponse body,
// with the advisory warning always present on the 201/202 paths; (3) the
// public FeatureRequest record pins seven previously undocumented
// always-served fields (columnName/columnColor/releasedVersion/approved/
// isOwnRequest/commentCount/updatedAt). The four SDK skills gain only the
// nested-envelope note — decoding is unaffected, so their flat-mirror
// guidance stays valid.
func TestSkillDocsIssue321ResponseSchemaSync(t *testing.T) {
	doc := readSkill(t, "cupthread-api")
	for _, marker := range []string{
		// Upload-session envelope: six keys, nested + legacy flat mirror.
		"the envelope **always serves six top-level keys** (SaaS #1474)",
		"`session` = `{ \"sessionId\", \"sessionToken\", \"expiresAt\", \"maxFileSizeBytes\", \"maxFiles\" }`",
		"`files` = `[{ \"clientFileId\", \"uploadId\", \"uploadUrl\", \"maxSizeBytes\" }]`",
		"a slot created without a `clientFileId` echoes its `uploadId` in that field",
		"`uploads` = `[{ \"clientFileId\"?, \"uploadId\", \"uploadUrl\", \"filename\", \"mimeType\", \"maxBytes\" }]`",
		"`files[]` carries the client handle (`maxSizeBytes`), `uploads[]` carries the **intake descriptor** (`filename`/`mimeType`/`maxBytes`)",
		// Feedback: three success statuses, one body, warning always on 201/202.
		"**three success statuses, all with the same `FeedbackSubmissionResponse` body**",
		"`200` when the submission was forwarded to GitHub Discussions synchronously (`forwardedToGithub: true`)",
		"`201` when it is stored in the CupThread inbox and the app has no GitHub delivery configured (`forwardedToGithub: false`)",
		"`202` when it is stored with GitHub delivery queued and retrying (`forwardedToGithub: false`)",
		"On the `201`/`202` paths the advisory `warning` string is **always present**",
		"on `200` it appears only when attachment signing is unconfigured",
		// FeatureRequest: seven always-served fields.
		"**seven always-served wire fields**",
		"the board-column presentation pair `columnName`/`columnColor` (both nullable, resolved from the request's column)",
		"`releasedVersion` (nullable mirror of `versionLabel`, for clients keyed on the released-version name)",
		"the moderation flag `approved` (board-visible ⇔ approved)",
		"`isOwnRequest` (always served; `false` for callers whose end-user token is not the submitter's)",
		"`commentCount` (visible comments on the thread)",
		// Detail row: the previously vague field enumeration is now precise.
		"the status/column fields `status`/`columnId`/`columnSlug`/`columnName`/`columnColor`",
		"the release version label `versionId`/`versionLabel` plus its `releasedVersion` mirror",
	} {
		if !strings.Contains(doc, marker) {
			t.Errorf("cupthread-api docs are missing required issue-#321 marker %q", marker)
		}
	}
	for _, stale := range []string{
		// The pre-sync four-key session response.
		"{ \"sessionId\", \"sessionToken\", \"expiresAt\", \"uploads\": [...] }",
		// The pre-sync single-status feedback sentence.
		"answers `201` (or `202` when GitHub delivery is queued, `200` when forwarded inline)",
		// `warning` is no longer documented as uniformly optional.
		"the optional human-readable `warning`",
		// The pre-sync vague detail-row enumeration.
		"status/column fields, release version label",
	} {
		if strings.Contains(doc, stale) {
			t.Errorf("cupthread-api docs still carry stale pre-#321 text %q", stale)
		}
	}

	// The SDK skills keep the flat-mirror guidance and note the nested
	// envelope so clients that model the full response do not split the
	// shapes.
	for _, sdk := range []string{"cupthread-swift-sdk", "cupthread-android-sdk", "cupthread-react-native-sdk", "cupthread-flutter-sdk"} {
		sdkDoc := readSkill(t, sdk)
		for _, marker := range []string{
			"The envelope also nests the same session and slots as `session` and `files` (per-file `maxSizeBytes`) — both shapes are always served together (SaaS #1474).",
		} {
			if !strings.Contains(sdkDoc, marker) {
				t.Errorf("%s docs are missing required issue-#321 marker %q", sdk, marker)
			}
		}
	}
}
