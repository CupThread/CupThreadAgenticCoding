package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateASCIIPinsHistoricalBytes pins the issue #66 requirement that
// pure-ASCII over-cap output stays byte-identical to the historical
// s[:n-1] + "…" behavior.
func TestTruncateASCIIPinsHistoricalBytes(t *testing.T) {
	long := strings.Repeat("a", 100)
	if got := truncate(long, 44); got != strings.Repeat("a", 43)+"…" {
		t.Errorf("truncate(100a, 44) = %q, want 43 a's + ellipsis", got)
	}
	for _, n := range []int{2, 3, 10, 44} {
		want := long[:n-1] + "…"
		if got := truncate(long, n); got != want {
			t.Errorf("truncate(long, %d) = %q, want %q", n, got, want)
		}
	}
}

// TestTruncateCJKNeverSplitsARune covers the core defect: a byte cut inside a
// 3-byte CJK rune emitted invalid UTF-8 into every table. The result must be
// valid UTF-8, end with the ellipsis, and keep the longest rune-aligned
// prefix that fits the n-1 byte content budget.
func TestTruncateCJKNeverSplitsARune(t *testing.T) {
	in := strings.Repeat("反", 30) // 90 bytes
	got := truncate(in, 44)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated string missing ellipsis suffix: %q", got)
	}
	want := strings.Repeat("反", 14) + "…" // 14*3=42 <= 43 budget, 15*3=45 > 43
	if got != want {
		t.Errorf("truncate = %q (%d bytes), want %q", got, len(got), want)
	}
}

// TestTruncateFourByteEmoji covers 4-byte runes: the cut must back up to the
// rune boundary, never emit a partial sequence.
func TestTruncateFourByteEmoji(t *testing.T) {
	in := strings.Repeat("🙂", 20) // 80 bytes
	got := truncate(in, 44)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
	want := strings.Repeat("🙂", 10) + "…" // 10*4=40 <= 43 budget, 11*4=44 > 43
	if got != want {
		t.Errorf("truncate = %q, want %q", got, want)
	}
}

// TestTruncateMixedScriptCutBoundary checks a cut landing mid-CJK-rune after
// an ASCII prefix walks back to the boundary instead of splitting the rune.
func TestTruncateMixedScriptCutBoundary(t *testing.T) {
	in := "abc" + strings.Repeat("反", 20)
	got := truncate(in, 12) // budget 11: "abc"+2 runes = 9 bytes fits, 3rd ends at 12
	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
	if want := "abc" + strings.Repeat("反", 2) + "…"; got != want {
		t.Errorf("truncate = %q, want %q", got, want)
	}
}

// TestTruncateWithinCapUnchanged pins that strings fitting the cap (including
// a multi-byte string whose byte length equals n exactly) pass through
// untouched.
func TestTruncateWithinCapUnchanged(t *testing.T) {
	for _, tc := range []struct {
		in string
		n  int
	}{
		{"", 5},
		{"abc", 3},
		{"abc", 4},
		{"反馈", 6}, // exact byte fit
		{"反馈", 7},
		{"反馈反馈", 100},
	} {
		if got := truncate(tc.in, tc.n); got != tc.in {
			t.Errorf("truncate(%q, %d) = %q, want unchanged", tc.in, tc.n, got)
		}
	}
}

// TestTruncateTinyCaps pins the n <= 1 semantics chosen by the fix: an
// over-cap string always renders as the ellipsis (never a lone byte sliced
// out of a multi-byte rune, the historical n <= 1 defect), and n == 0
// renders as the empty string.
func TestTruncateTinyCaps(t *testing.T) {
	if got := truncate("反馈", 0); got != "" {
		t.Errorf("truncate(反馈, 0) = %q, want empty", got)
	}
	for _, in := range []string{"反馈", "🙂🙂", "abcdef"} {
		if got := truncate(in, 1); got != "…" || !utf8.ValidString(got) {
			t.Errorf("truncate(%q, 1) = %q, want ellipsis", in, got)
		}
	}
}

// TestTruncateAlwaysValidUTF8 sweeps cap sizes over mixed content asserting
// the output is valid UTF-8 whatever the cut position.
func TestTruncateAlwaysValidUTF8(t *testing.T) {
	inputs := []string{
		strings.Repeat("反", 10),
		"a反b🙂c反",
		strings.Repeat("🙂", 5) + strings.Repeat("反", 5),
		"plain ascii but long enough to cut",
	}
	for _, in := range inputs {
		for n := 0; n <= len(in)+2; n++ {
			got := truncate(in, n)
			if !utf8.ValidString(got) {
				t.Fatalf("truncate(%q, %d) produced invalid UTF-8: %q", in, n, got)
			}
			if n > 0 && len(in) > n && !strings.HasSuffix(got, "…") {
				t.Errorf("truncate(%q, %d) dropped content without ellipsis: %q", in, n, got)
			}
		}
	}
}

// TestFeaturesListCJKTitleValidUTF8 is the end-to-end issue #66 check: a
// feature request titled with more CJK characters than the 44-byte table cap
// renders through `features list` without corrupting the stream, and the
// Title cell ends with the ellipsis.
func TestFeaturesListCJKTitleValidUTF8(t *testing.T) {
	title := strings.Repeat("反", 30) // 90 bytes, well over the 44-byte cap
	fixture, err := json.Marshal(map[string]any{
		"requests": []map[string]any{{
			"id":        "fr_cjk_1",
			"appId":     "app_a",
			"title":     title,
			"status":    "open",
			"voteCount": 1,
			"createdAt": "2026-09-01T12:00:00.000Z",
			"updatedAt": "2026-09-01T12:00:00.000Z",
		}},
		"total": 1,
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/feature-requests") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer server.Close()
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	out, err := runRoot(t, server.URL, "features", "list", "--workspace", "ws_1")
	if err != nil {
		t.Fatalf("features list: %v", err)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("features list emitted invalid UTF-8 for a CJK title over the cap")
	}
	wantCell := strings.Repeat("反", 14) + "…"
	if !strings.Contains(out, wantCell) {
		t.Errorf("output missing truncated title cell %q:\n%s", wantCell, out)
	}
}
