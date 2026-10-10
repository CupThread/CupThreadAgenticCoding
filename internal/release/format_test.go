package release

import (
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The tree must stay gofmt-clean and CI must enforce it (issue #195): build,
// vet and test never check formatting, so without an explicit gate an
// unformatted PR lands silently and every format-on-save editor turns into
// diff noise. These tests pin both halves of the gate — the module tree is
// formatted right now, and ci.yml's build job fails on drift before it spends
// minutes compiling.

// unformattedGoFiles walks root and returns the module-relative paths of every
// .go file whose bytes differ from go/format's canonical output. A file that
// fails to parse is a hard error: the tree must always compile.
func unformattedGoFiles(root string) ([]string, error) {
	var unformatted []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		canon, err := format.Source(src)
		if err != nil {
			return err
		}
		if string(canon) != string(src) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				rel = path
			}
			unformatted = append(unformatted, rel)
		}
		return nil
	})
	return unformatted, err
}

// TestModuleTreeIsGofmtClean pins the invariant the ci.yml gofmt gate
// enforces remotely: no file in the module may differ from gofmt's canonical
// bytes, so format-on-save editors and audit tooling stay diff-free.
func TestModuleTreeIsGofmtClean(t *testing.T) {
	unformatted, err := unformattedGoFiles(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("walk module tree: %v", err)
	}
	if len(unformatted) > 0 {
		t.Fatalf("%d files are not gofmt-clean:\n%s\nrun `gofmt -w .` and commit the result",
			len(unformatted), strings.Join(unformatted, "\n"))
	}
}

// TestGofmtGuardDetectsMisalignedFixture exercises the guard's failure mode on
// a fixture the tree must not contain: a struct with unaligned field tags is
// reported by path, a formatted sibling passes, and non-Go files are ignored.
func TestGofmtGuardDetectsMisalignedFixture(t *testing.T) {
	formatted := "package x\n\ntype T struct {\n\tA      int `json:\"a\"`\n\tLonger int `json:\"longer\"`\n}\n"
	misaligned := "package x\n\ntype T struct {\n\tA int `json:\"a\"`\n\tLonger int `json:\"longer\"`\n}\n"

	dir := t.TempDir()
	files := map[string]string{
		"clean.go":      formatted,
		"misaligned.go": misaligned,
		"notes.txt":     misaligned,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	unformatted, err := unformattedGoFiles(dir)
	if err != nil {
		t.Fatalf("walk fixture tree: %v", err)
	}
	if len(unformatted) != 1 || unformatted[0] != "misaligned.go" {
		t.Fatalf("unformattedGoFiles() = %v, want [misaligned.go]", unformatted)
	}

	canon, err := format.Source([]byte(misaligned))
	if err != nil {
		t.Fatalf("format fixture: %v", err)
	}
	if string(canon) != formatted {
		t.Fatalf("format.Source on the fixture did not produce the expected alignment:\n%s", canon)
	}
}

// TestCIWorkflowGatesFormatting pins the ci.yml half of issue #195: the build
// job must run a gofmt check that fails the run on drift, before any `go`
// step — otherwise the workflow re-learns to ignore formatting the moment the
// step is dropped or reordered.
func TestCIWorkflowGatesFormatting(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	var wf struct {
		Jobs map[string]workflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse ci.yml: %v", err)
	}
	build, ok := wf.Jobs["build"]
	if !ok {
		t.Fatal("ci.yml must define the build job")
	}

	gate := stepIndex(build.Steps, func(s workflowStep) bool { return strings.Contains(s.Run, "gofmt -l") })
	if gate < 0 {
		t.Fatal("build job lost the gofmt gate — unformatted PRs would land silently again (issue #195)")
	}
	if !strings.Contains(build.Steps[gate].Run, "exit 1") {
		t.Fatal("the gofmt gate no longer fails the run when gofmt -l reports files")
	}
	for i, s := range build.Steps {
		if s.runsGo() && i < gate {
			t.Fatalf("build step %q runs before the gofmt gate (step %d) — formatting must fail first", s.Name, gate)
		}
	}
}
