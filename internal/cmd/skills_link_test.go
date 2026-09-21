package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillsLinkFixture builds a fake source checkout (go.mod + skills/) and a
// separate target project, chdirs into the source so repoRoot() resolves
// there, and returns both paths.
func skillsLinkFixture(t *testing.T) (source, target string) {
	t.Helper()

	source = t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module github.com/CupThread/CupThreadAgenticCoding\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	for _, skill := range []string{"cupthread-api", "cupthread-cli"} {
		dir := filepath.Join(source, "skills", skill)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir skill: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("bundled "+skill+"\n"), 0o644); err != nil {
			t.Fatalf("write SKILL.md: %v", err)
		}
	}

	target = t.TempDir()
	t.Chdir(source)
	return source, target
}

// assertSkillLink verifies link is a symlink resolving to wantTarget,
// normalizing both sides through EvalSymlinks because the command derives
// paths from Getwd (physical /private/var/...) while tests see t.TempDir()
// paths (/var/...).
func assertSkillLink(t *testing.T, link, wantTarget string) {
	t.Helper()

	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink %s: %v", link, err)
	}
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("resolve %s: %v", got, err)
	}
	wantResolved, err := filepath.EvalSymlinks(wantTarget)
	if err != nil {
		t.Fatalf("resolve %s: %v", wantTarget, err)
	}
	if gotResolved != wantResolved {
		t.Fatalf("link %s points at %s, want %s", link, gotResolved, wantResolved)
	}
}

func TestSkillsLinkFreshDestination(t *testing.T) {
	source, target := skillsLinkFixture(t)

	out, err := runRoot(t, "http://127.0.0.1:1", "skills", "link", target)
	if err != nil {
		t.Fatalf("skills link: %v", err)
	}
	if strings.Count(out, "✓ Linked 2 skills into") != 3 {
		t.Fatalf("expected three per-dir summary lines, got:\n%s", out)
	}
	for _, agentDir := range agentSkillDirs {
		assertSkillLink(t, filepath.Join(target, agentDir, "cupthread-api"), filepath.Join(source, "skills", "cupthread-api"))
		assertSkillLink(t, filepath.Join(target, agentDir, "cupthread-cli"), filepath.Join(source, "skills", "cupthread-cli"))
	}
}

func TestSkillsLinkReplacesExistingSymlink(t *testing.T) {
	source, target := skillsLinkFixture(t)

	decoy := filepath.Join(target, "elsewhere")
	if err := os.MkdirAll(decoy, 0o755); err != nil {
		t.Fatalf("mkdir decoy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(decoy, "keep.txt"), []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("write keep.txt: %v", err)
	}
	for _, agentDir := range agentSkillDirs {
		if err := os.MkdirAll(filepath.Join(target, agentDir), 0o755); err != nil {
			t.Fatalf("mkdir agent dir: %v", err)
		}
		if err := os.Symlink(decoy, filepath.Join(target, agentDir, "cupthread-api")); err != nil {
			t.Fatalf("seed symlink: %v", err)
		}
	}

	if _, err := runRoot(t, "http://127.0.0.1:1", "skills", "link", target); err != nil {
		t.Fatalf("skills link: %v", err)
	}
	for _, agentDir := range agentSkillDirs {
		assertSkillLink(t, filepath.Join(target, agentDir, "cupthread-api"), filepath.Join(source, "skills", "cupthread-api"))
	}
	// The replaced links' old referent must be untouched.
	if _, err := os.Stat(filepath.Join(decoy, "keep.txt")); err != nil {
		t.Fatalf("decoy referent was modified: %v", err)
	}
}

func TestSkillsLinkReplacesBrokenSymlink(t *testing.T) {
	source, target := skillsLinkFixture(t)

	linkDir := filepath.Join(target, ".claude", "skills")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(filepath.Join(target, "does-not-exist"), filepath.Join(linkDir, "cupthread-api")); err != nil {
		t.Fatalf("seed broken symlink: %v", err)
	}

	if _, err := runRoot(t, "http://127.0.0.1:1", "skills", "link", target); err != nil {
		t.Fatalf("skills link: %v", err)
	}
	assertSkillLink(t, filepath.Join(linkDir, "cupthread-api"), filepath.Join(source, "skills", "cupthread-api"))
}

func TestSkillsLinkSkipsRealDirectory(t *testing.T) {
	source, target := skillsLinkFixture(t)

	local := filepath.Join(target, ".claude", "skills", "cupthread-api")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatalf("mkdir local skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(local, "SKILL.md"), []byte("my local edits\n"), 0o644); err != nil {
		t.Fatalf("write local SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(local, "notes.md"), []byte("extra notes\n"), 0o644); err != nil {
		t.Fatalf("write notes.md: %v", err)
	}

	out, err := runRoot(t, "http://127.0.0.1:1", "skills", "link", target)
	if err == nil {
		t.Fatalf("expected non-zero exit on non-symlink conflict, output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error should hint at --force, got: %v", err)
	}
	if !strings.Contains(out, "Skipped cupthread-api") || !strings.Contains(out, "use --force to replace") {
		t.Fatalf("expected skip warning, got:\n%s", out)
	}
	if !strings.Contains(out, "Linked 1 of 2 skills into") {
		t.Fatalf("expected partial summary line, got:\n%s", out)
	}

	// The customized directory is untouched: contents intact, no backup made.
	if b, err := os.ReadFile(filepath.Join(local, "SKILL.md")); err != nil || string(b) != "my local edits\n" {
		t.Fatalf("local SKILL.md was modified: %v (%q)", err, string(b))
	}
	if _, err := os.Stat(filepath.Join(local, "notes.md")); err != nil {
		t.Fatalf("notes.md missing: %v", err)
	}
	if entries, err := filepath.Glob(local + ".bak-*"); err != nil || len(entries) != 0 {
		t.Fatalf("unexpected backup without --force: %v (%v)", entries, err)
	}
	// The conflict only blocks its own skill; everything else is linked.
	assertSkillLink(t, filepath.Join(target, ".claude", "skills", "cupthread-cli"), filepath.Join(source, "skills", "cupthread-cli"))
	assertSkillLink(t, filepath.Join(target, ".agents", "skills", "cupthread-api"), filepath.Join(source, "skills", "cupthread-api"))
	assertSkillLink(t, filepath.Join(target, ".zcode", "skills", "cupthread-api"), filepath.Join(source, "skills", "cupthread-api"))
}

func TestSkillsLinkForceReplacesRealDirectoryWithBackup(t *testing.T) {
	source, target := skillsLinkFixture(t)

	local := filepath.Join(target, ".claude", "skills", "cupthread-api")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatalf("mkdir local skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(local, "SKILL.md"), []byte("my local edits\n"), 0o644); err != nil {
		t.Fatalf("write local SKILL.md: %v", err)
	}

	out, err := runRoot(t, "http://127.0.0.1:1", "skills", "link", target, "--force")
	if err != nil {
		t.Fatalf("skills link --force: %v\noutput:\n%s", err, out)
	}
	assertSkillLink(t, local, filepath.Join(source, "skills", "cupthread-api"))

	backups, err := filepath.Glob(local + ".bak-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected exactly one backup, got %v (%v)", backups, err)
	}
	if b, err := os.ReadFile(filepath.Join(backups[0], "SKILL.md")); err != nil || string(b) != "my local edits\n" {
		t.Fatalf("backup contents wrong: %v (%q)", err, string(b))
	}
}

func TestSkillsLinkSkipsRealFile(t *testing.T) {
	source, target := skillsLinkFixture(t)

	dest := filepath.Join(target, ".zcode", "skills", "cupthread-cli")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dest, []byte("a plain file\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	out, err := runRoot(t, "http://127.0.0.1:1", "skills", "link", target)
	if err == nil {
		t.Fatalf("expected non-zero exit on non-symlink conflict, output:\n%s", out)
	}
	if !strings.Contains(out, "Skipped cupthread-cli") {
		t.Fatalf("expected skip warning, got:\n%s", out)
	}
	if b, err := os.ReadFile(dest); err != nil || string(b) != "a plain file\n" {
		t.Fatalf("file was modified: %v (%q)", err, string(b))
	}
	assertSkillLink(t, filepath.Join(target, ".zcode", "skills", "cupthread-api"), filepath.Join(source, "skills", "cupthread-api"))
}
