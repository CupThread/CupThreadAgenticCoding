package skills

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// skillMD is one SKILL.md and its exact byte size, keyed by skill directory.
type skillMD struct {
	skill string
	size  int
}

// TestSkillMDSizeBudgets asserts every skills/<name>/SKILL.md stays under the
// soft budget and the hard limit — in both distribution paths. The embedded
// copies (skills.FS) are what `cupthread skills link` serves from a brew or
// `go install` binary; the on-disk copies under skills/ are what checkout-mode
// `skills link` symlinks. The go:embed directive guarantees the two agree for
// the directories it names, but a new skill directory missing from the
// directive would be invisible to the embedded walk while checkout mode still
// distributes it — so the two sets are also compared for parity.
func TestSkillMDSizeBudgets(t *testing.T) {
	embedded := embeddedSkillMDs(t)
	onDisk := onDiskSkillMDs(t)

	// Parity: every skill directory on disk must be embedded, and vice
	// versa, so neither walk can silently miss a skill.
	var missing []string
	for _, f := range onDisk {
		if _, ok := embedded[f.skill]; !ok {
			missing = append(missing, f.skill+" (on disk, absent from the go:embed directive in embed.go)")
		}
	}
	for skill := range embedded {
		if _, ok := onDisk[skill]; !ok {
			missing = append(missing, skill+" (embedded, absent from the skills/ directory)")
		}
	}
	if len(missing) > 0 {
		t.Errorf("embedded and on-disk skill sets drifted:\n\t%s", strings.Join(missing, "\n\t"))
	}

	var overHardLimit, overBudget []string
	check := func(f skillMD, label string) {
		switch {
		case f.size >= skillSizeHardLimit:
			overHardLimit = append(overHardLimit, formatSkillSize(label, f.size,
				"exceeds the 100 KB agent-skill hard limit: skill hosts refuse to load the file wholesale"))
		case f.size >= skillSizeBudget:
			overBudget = append(overBudget, formatSkillSize(label, f.size,
				"budget exceeded (soft budget 80 KB, hard limit at 100 KB): move endpoint detail to the live OpenAPI spec before the next docs sync"))
		}
	}
	for _, f := range embedded {
		check(f, "embedded "+f.skill+"/SKILL.md")
	}
	for _, f := range onDisk {
		check(f, "on-disk "+f.skill+"/SKILL.md")
	}

	// Hard-limit offenders first: they break skill loading today, not just
	// the budget.
	problems := append(slices.Clone(overHardLimit), overBudget...)
	if len(problems) > 0 {
		t.Errorf("SKILL.md size check failed:\n%s", strings.Join(problems, "\n"))
	}
}

// embeddedSkillMDs reads the SKILL.md sizes out of skills.FS. The go:embed
// directive copies the source files byte-for-byte at build time, so these are
// the sizes a distributed binary ships.
func embeddedSkillMDs(t *testing.T) map[string]skillMD {
	t.Helper()
	dirs := mustEmbeddedSkillDirs(t)
	out := make(map[string]skillMD, len(dirs))
	for _, dir := range dirs {
		data, err := fs.ReadFile(FS, dir+"/SKILL.md")
		if err != nil {
			t.Fatalf("read embedded %s/SKILL.md: %v", dir, err)
		}
		out[dir] = skillMD{skill: dir, size: len(data)}
	}
	return out
}

// onDiskSkillMDs reads the SKILL.md sizes from the skills/ working tree, which
// checkout-mode `skills link` distributes directly.
func onDiskSkillMDs(t *testing.T) map[string]skillMD {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read skills directory: %v", err)
	}
	out := make(map[string]skillMD)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(e.Name(), "SKILL.md"))
		if err != nil {
			if os.IsNotExist(err) {
				continue // a non-skill directory carries no SKILL.md
			}
			t.Fatalf("read %s/SKILL.md: %v", e.Name(), err)
		}
		out[e.Name()] = skillMD{skill: e.Name(), size: len(data)}
	}
	if len(out) == 0 {
		t.Fatal("no on-disk skills found next to the test")
	}
	return out
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
