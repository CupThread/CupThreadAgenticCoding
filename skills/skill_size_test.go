package skills

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

// Agent-skill hosts refuse to load a SKILL.md at or above the 100 KB maximum
// (issue #318): the whole skill is rejected, not just the overflowing part, so
// an oversized file silently removes all of its guidance from agents.
const (
	// skillSizeHardLimit mirrors the 100 KB maximum skill hosts enforce. A
	// file at or above it fails the check outright.
	skillSizeHardLimit = 100 << 10
	// skillSizeBudget is the soft budget: failing at a lower threshold with
	// its own "budget exceeded" message flags routine docs-sync growth a few
	// cycles before the file becomes a hard break.
	skillSizeBudget = 80 << 10
)

// TestSkillMDSizeBudgets asserts every embedded skills/<name>/SKILL.md stays
// under the soft budget and the hard limit. The check reads the embedded
// copies (skills.FS), which the go:embed directive guarantees are byte-for-byte
// identical to the source tree at build time, so a single assertion covers both
// the repo files and what `cupthread skills link` distributes.
// skillMD is one embedded SKILL.md and its exact byte size.
type skillMD struct {
	path string
	size int
}

func TestSkillMDSizeBudgets(t *testing.T) {
	// Walk the embedded FS rather than the working tree so the check also
	// holds in the distributed binary, where no source checkout exists.
	var files []skillMD
	err := fs.WalkDir(FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, "/SKILL.md") {
			return nil
		}
		data, err := fs.ReadFile(FS, path)
		if err != nil {
			return err
		}
		files = append(files, skillMD{path: path, size: len(data)})
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded skills: %v", err)
	}
	// Guard against an embed-directive refactor silently emptying the check.
	dirs := mustEmbeddedSkillDirs(t)
	if len(files) < len(dirs) {
		t.Fatalf("expected a SKILL.md per embedded skill %v, found %d: %v", dirs, len(files), skillMDPaths(files))
	}

	var overHardLimit, overBudget []string
	for _, f := range files {
		switch {
		case f.size >= skillSizeHardLimit:
			overHardLimit = append(overHardLimit, formatSkillSize(f.path, f.size,
				"exceeds the 100 KB agent-skill hard limit: skill hosts refuse to load the file wholesale"))
		case f.size >= skillSizeBudget:
			overBudget = append(overBudget, formatSkillSize(f.path, f.size,
				"budget exceeded (soft budget 80 KB, hard limit at 100 KB): move endpoint detail to the live OpenAPI spec before the next docs sync"))
		}
	}

	// Hard-limit offenders first: they break skill loading today, not just
	// the budget.
	problems := append(slices.Clone(overHardLimit), overBudget...)
	if len(problems) > 0 {
		t.Errorf("SKILL.md size check failed:\n%s", strings.Join(problems, "\n"))
	}
}

// mustEmbeddedSkillDirs lists the top-level directories embedded in skills.FS.
func mustEmbeddedSkillDirs(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatalf("read embedded skills root: %v", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 0 {
		t.Fatal("no skills embedded in the binary")
	}
	slices.Sort(dirs)
	return dirs
}

func formatSkillSize(path string, size int, problem string) string {
	return fmt.Sprintf("\t%s: %s (got %d bytes ≈ %d KB)", path, problem, size, size>>10)
}

func skillMDPaths(files []skillMD) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out
}
