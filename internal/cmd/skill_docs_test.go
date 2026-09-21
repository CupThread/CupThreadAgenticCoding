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
