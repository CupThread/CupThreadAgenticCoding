package release

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestRenderFormulaGolden pins the exact formula bytes so a tap diff always
// corresponds to a real change in the renderer, not formatting drift.
func TestRenderFormulaGolden(t *testing.T) {
	f := Formula{Version: "0.3.0", SHA256: strings.Repeat("ab", 32)}
	got, err := f.Render()
	if err != nil {
		t.Fatalf("Render() errored: %v", err)
	}
	want := `class Cupthread < Formula
  desc "CupThread CLI — manage your CupThread projects from the command line"
  homepage "https://cupthread.com"
  url "https://github.com/CupThread/CupThreadAgenticCoding/archive/refs/tags/v0.3.0.tar.gz"
  sha256 "` + strings.Repeat("ab", 32) + `"
  license "MIT"
  head "https://github.com/CupThread/CupThreadAgenticCoding.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(output: bin/"cupthread", ldflags: "-s -w -X github.com/CupThread/CupThreadAgenticCoding/internal/cmd.Version=#{version}"), "./cmd/cupthread"
  end

  test do
    assert_match "cupthread version #{version}", shell_output("#{bin}/cupthread --version")
  end
end
`
	if got != want {
		t.Fatalf("Render() mismatch:\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderFormulaInjectsPinnedVersionIntoInstall checks the property the
// brew upgrade flow depends on: the binary the formula builds must report
// the formula's own version, not a hardcoded default.
func TestRenderFormulaInjectsPinnedVersionIntoInstall(t *testing.T) {
	got, err := Formula{Version: "1.2.3", SHA256: strings.Repeat("cd", 32)}.Render()
	if err != nil {
		t.Fatalf("Render() errored: %v", err)
	}
	if !strings.Contains(got, `ldflags: "-s -w -X github.com/CupThread/CupThreadAgenticCoding/internal/cmd.Version=#{version}"`) {
		t.Fatalf("install does not inject the formula version via ldflags:\n%s", got)
	}
	if !strings.Contains(got, `assert_match "cupthread version #{version}"`) {
		t.Fatalf("test block does not assert the reported version:\n%s", got)
	}
}

func TestRenderFormulaRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		f    Formula
	}{
		{"missing version", Formula{SHA256: strings.Repeat("ab", 32)}},
		{"v-prefixed version", Formula{Version: "v0.3.0", SHA256: strings.Repeat("ab", 32)}},
		{"non-semver version", Formula{Version: "main", SHA256: strings.Repeat("ab", 32)}},
		{"missing sha256", Formula{Version: "0.3.0"}},
		{"short sha256", Formula{Version: "0.3.0", SHA256: "abcd"}},
		{"non-hex sha256", Formula{Version: "0.3.0", SHA256: strings.Repeat("zz", 32)}},
		{"uppercase sha256 is normalized", Formula{Version: "0.3.0", SHA256: strings.ToUpper(strings.Repeat("ab", 32))}},
		{"repo without owner", Formula{Version: "0.3.0", SHA256: strings.Repeat("ab", 32), Repo: "justname"}},
		{"repo with scheme", Formula{Version: "0.3.0", SHA256: strings.Repeat("ab", 32), Repo: "https://github.com/a/b"}},
	}
	for _, tc := range cases {
		got, err := tc.f.Render()
		wantErr := tc.name != "uppercase sha256 is normalized"
		if wantErr && err == nil {
			t.Errorf("%s: expected error, got formula:\n%s", tc.name, got)
		}
		if !wantErr {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", tc.name, err)
				continue
			}
			if !regexp.MustCompile(`sha256 "[0-9a-f]{64}"`).MatchString(got) {
				t.Errorf("%s: sha256 was not lowercased:\n%s", tc.name, got)
			}
		}
	}
}

// TestRenderFormulaMatchesModulePath guards the ldflags target against
// module renames: go.mod is the source of truth, and a stale hardcoded path
// would make the formula silently build binaries that report "dev".
func TestRenderFormulaMatchesModulePath(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	moduleRe := regexp.MustCompile(`(?m)^module\s+(\S+)`)
	m := moduleRe.FindSubmatch(data)
	if m == nil {
		t.Fatal("no module directive in go.mod")
	}
	got, err := Formula{Version: "0.3.0", SHA256: strings.Repeat("ab", 32)}.Render()
	if err != nil {
		t.Fatalf("Render() errored: %v", err)
	}
	target := string(m[1]) + "/internal/cmd.Version="
	if !strings.Contains(got, target) {
		t.Fatalf("formula ldflags target does not match module path %q:\n%s", target, got)
	}
}

// TestLdflagsTargetStaysLinkable pins the fix for #60: Version must remain a
// package var the linker's -X flag can assign. Reverting it to a const would
// make the release workflow's ldflags a no-op and every binary report the
// hardcoded default forever.
func TestLdflagsTargetStaysLinkable(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "cmd", "root.go"))
	if err != nil {
		t.Fatalf("read root.go: %v", err)
	}
	if !regexp.MustCompile(`(?m)^var Version = `).Match(src) {
		t.Fatal("internal/cmd Version is no longer a link-injectable `var` — the release workflow's -X flag would stop working")
	}
}
