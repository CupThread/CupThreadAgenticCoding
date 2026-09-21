package cmd

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/skills"
)

// embeddedSkillNames returns the sorted skill directory names baked into the
// binary.
func embeddedSkillNames(t *testing.T) []string {
	t.Helper()
	entries, err := skills.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded skills: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("no skills embedded in the binary")
	}
	slices.Sort(names)
	return names
}

func writeGoMod(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

// realPath resolves symlinks so t.TempDir paths (logical /var/...) compare
// equal to walking-derived paths (physical /private/var/...).
func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return resolved
}

// skillsListJSON runs skills list --json from the current directory.
func skillsListJSON(t *testing.T) (names []string, source string) {
	t.Helper()
	out, err := runRoot(t, "http://127.0.0.1:1", "skills", "list", "--json")
	if err != nil {
		t.Fatalf("skills list: %v", err)
	}
	var got struct {
		Count  int      `json:"count"`
		Skills []string `json:"skills"`
		Source string   `json:"source"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse skills list --json output %q: %v", out, err)
	}
	return got.Skills, got.Source
}

// The binary is invoked from a directory outside any Go module (the brew /
// go install situation): skills list must serve the embedded copy.
func TestSkillsListEmbedded(t *testing.T) {
	t.Chdir(t.TempDir())

	names, source := skillsListJSON(t)
	want := embeddedSkillNames(t)
	if source != "embedded" {
		t.Errorf("source = %q, want %q", source, "embedded")
	}
	if !slices.Equal(names, want) {
		t.Errorf("skills = %v, want %v", names, want)
	}
}

// Without a checkout, skills link copies the embedded skills (a symlink into
// a binary is impossible), byte-identical. A re-run finds real files, so the
// non-destructive contract skips them unless --force replaces them through a
// recoverable .bak backup — copies are never silently destroyed.
func TestSkillsLinkFromEmbeddedCopy(t *testing.T) {
	t.Chdir(t.TempDir())
	target := t.TempDir()

	out, err := runRoot(t, "http://127.0.0.1:1", "skills", "link", target)
	if err != nil {
		t.Fatalf("skills link: %v\n%s", err, out)
	}
	if !strings.Contains(out, "embedded") {
		t.Errorf("output does not disclose the embedded copy: %q", out)
	}
	assertEmbeddedCopies(t, target)

	// Re-run without --force: the copies are real files, so they are skipped
	// and the command exits non-zero instead of overwriting them.
	out, err = runRoot(t, "http://127.0.0.1:1", "skills", "link", target)
	if err == nil || !strings.Contains(err.Error(), "not symlinks") {
		t.Fatalf("re-run without --force = %v, want the non-symlink skip error\n%s", err, out)
	}
	// --force replaces every copy through a .bak backup, still byte-identical.
	out, err = runRoot(t, "http://127.0.0.1:1", "skills", "link", target, "--force")
	if err != nil {
		t.Fatalf("skills link --force: %v\n%s", err, out)
	}
	assertEmbeddedCopies(t, target)
	for _, agentDir := range agentSkillDirs {
		for _, name := range embeddedSkillNames(t) {
			backups, err := filepath.Glob(filepath.Join(target, agentDir, name) + ".bak-*")
			if err != nil || len(backups) != 1 {
				t.Fatalf("%s/%s backups = %v (%v), want exactly one", agentDir, name, backups, err)
			}
			got, err := os.ReadFile(filepath.Join(backups[0], "SKILL.md"))
			if err != nil {
				t.Fatalf("read backup %s: %v", backups[0], err)
			}
			want, err := fs.ReadFile(skills.FS, name+"/SKILL.md")
			if err != nil {
				t.Fatalf("read embedded %s/SKILL.md: %v", name, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("backup %s content differs from the embedded copy", backups[0])
			}
		}
	}
}

// assertEmbeddedCopies verifies every linked skill in target is a real
// (non-symlink) copy whose SKILL.md is byte-identical to the embedded one.
func assertEmbeddedCopies(t *testing.T, target string) {
	t.Helper()
	for _, agentDir := range agentSkillDirs {
		for _, name := range embeddedSkillNames(t) {
			skillFile := filepath.Join(target, agentDir, name, "SKILL.md")
			info, err := os.Lstat(skillFile)
			if err != nil {
				t.Fatalf("%s: %v", skillFile, err)
			}
			if info.Mode()&fs.ModeSymlink != 0 {
				t.Errorf("%s is a symlink, want a copied file", skillFile)
			}
			got, err := os.ReadFile(skillFile)
			if err != nil {
				t.Fatalf("read %s: %v", skillFile, err)
			}
			want, err := fs.ReadFile(skills.FS, name+"/SKILL.md")
			if err != nil {
				t.Fatalf("read embedded %s/SKILL.md: %v", name, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s content differs from the embedded copy", skillFile)
			}
		}
	}
}

// A foreign Go project is never mistaken for the CupThreadAgenticCoding
// checkout: its decoy skills/ directory must be ignored in favor of the
// embedded copy.
func TestRepoRootRejectsForeignModule(t *testing.T) {
	foreign := t.TempDir()
	writeGoMod(t, foreign, "module example.com/other\n\ngo 1.25\n")
	decoy := filepath.Join(foreign, "skills", "cupthread-fake")
	if err := os.MkdirAll(decoy, 0o755); err != nil {
		t.Fatalf("mkdir decoy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(decoy, "SKILL.md"), []byte("# decoy\n"), 0o644); err != nil {
		t.Fatalf("write decoy: %v", err)
	}
	t.Chdir(foreign)

	if _, err := repoRoot(); err == nil {
		t.Fatal("repoRoot accepted a foreign go.mod as the CupThreadAgenticCoding repository")
	}

	target := t.TempDir()
	if _, err := runRoot(t, "http://127.0.0.1:1", "skills", "link", target); err != nil {
		t.Fatalf("skills link: %v", err)
	}
	for _, agentDir := range agentSkillDirs {
		if _, err := os.Lstat(filepath.Join(target, agentDir, "cupthread-fake")); !os.IsNotExist(err) {
			t.Errorf("decoy skill was linked into %s", agentDir)
		}
		first := filepath.Join(target, agentDir, "cupthread-cli", "SKILL.md")
		if _, err := os.Stat(first); err != nil {
			t.Errorf("embedded cupthread-cli skill missing from %s: %v", agentDir, err)
		}
	}
}

// When a verified checkout exists it wins over the embedded copy: skills are
// symlinked from the checkout's skills directory.
func TestSkillsLinkFromVerifiedCheckout(t *testing.T) {
	repo := t.TempDir()
	writeGoMod(t, repo, "module "+repoModulePath+"\n\ngo 1.25\n")
	checkoutSkill := filepath.Join(repo, "skills", "cupthread-api")
	if err := os.MkdirAll(checkoutSkill, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(checkoutSkill, "SKILL.md"), []byte("# checkout copy\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Chdir(repo)

	names, source := skillsListJSON(t)
	if source != "checkout" {
		t.Errorf("source = %q, want %q", source, "checkout")
	}
	if !slices.Equal(names, []string{"cupthread-api"}) {
		t.Errorf("skills = %v, want only the checkout's [cupthread-api]", names)
	}

	target := t.TempDir()
	if _, err := runRoot(t, "http://127.0.0.1:1", "skills", "link", target); err != nil {
		t.Fatalf("skills link: %v", err)
	}
	link := filepath.Join(target, ".agents", "skills", "cupthread-api")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("%s: %v", link, err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink; a verified checkout must be symlinked, not copied", link)
	}
	if got, want := realPath(t, link), realPath(t, checkoutSkill); got != want {
		t.Errorf("symlink resolves to %s, want %s", got, want)
	}
}

// A foreign go.mod nearer than the checkout is skipped: the walk keeps
// climbing until it finds this repository's module.
func TestRepoRootSkipsForeignModuleAboveCheckout(t *testing.T) {
	repo := t.TempDir()
	writeGoMod(t, repo, "module "+repoModulePath+"\n\ngo 1.25\n")
	if err := os.MkdirAll(filepath.Join(repo, "skills", "cupthread-cli"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	nested := filepath.Join(repo, "examples", "foreign")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeGoMod(t, nested, "module example.com/foreign\n\ngo 1.25\n")
	t.Chdir(nested)

	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	if got, want := realPath(t, root), realPath(t, repo); got != want {
		t.Errorf("repoRoot = %s, want %s", got, want)
	}
}
