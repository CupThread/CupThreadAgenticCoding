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
