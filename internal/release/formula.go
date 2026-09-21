// Package release renders the artifacts published by a tagged CLI release:
// the Homebrew formula pinned to the tag's source tarball.
package release

import (
	"fmt"
	"regexp"
	"strings"
)

// defaultRepo is the GitHub owner/name the CLI publishes from.
const defaultRepo = "CupThread/CupThreadAgenticCoding"

// ldflagsTarget is the linker variable the built binary reads its version
// from. It must stay in sync with the module path in go.mod and the
// package-level Version var in internal/cmd; formula_test.go guards both.
const ldflagsTarget = "github.com/CupThread/CupThreadAgenticCoding/internal/cmd.Version"

var (
	repoRe    = regexp.MustCompile(`^[A-Za-z0-9.-]+/[A-Za-z0-9._-]+$`)
	versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$`)
	shaRe     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Formula describes one tagged release rendered into a Homebrew formula.
type Formula struct {
	Repo    string // GitHub owner/name, e.g. CupThread/CupThreadAgenticCoding ("" = default)
	Version string // release version without the leading "v", e.g. 0.3.0
	SHA256  string // sha256 of the tag's source tarball on GitHub (64 lowercase hex)
}

// Render returns the formula source, exactly the bytes written to the tap
// and to this repo's Formula/ mirror by the release workflow.
func (f Formula) Render() (string, error) {
	repo := f.Repo
	if repo == "" {
		repo = defaultRepo
	}
	if !repoRe.MatchString(repo) {
		return "", fmt.Errorf("invalid repo %q (want owner/name)", f.Repo)
	}
	if !versionRe.MatchString(f.Version) {
		return "", fmt.Errorf("invalid version %q (want semver without the leading v, e.g. 0.3.0)", f.Version)
	}
	sum := strings.ToLower(f.SHA256)
	if !shaRe.MatchString(sum) {
		return "", fmt.Errorf("invalid sha256 %q (want 64 hex chars)", f.SHA256)
	}

	return fmt.Sprintf(`class Cupthread < Formula
  desc "CupThread CLI — manage your CupThread projects from the command line"
  homepage "https://cupthread.com"
  url "https://github.com/%[1]s/archive/refs/tags/v%[2]s.tar.gz"
  sha256 "%[3]s"
  license "MIT"
  head "https://github.com/%[1]s.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(output: bin/"cupthread", ldflags: "-s -w -X %[4]s=#{version}"), "./cmd/cupthread"
  end

  test do
    assert_match "cupthread version #{version}", shell_output("#{bin}/cupthread --version")
  end
end
`, repo, f.Version, sum, ldflagsTarget), nil
}
