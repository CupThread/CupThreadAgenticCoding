package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
