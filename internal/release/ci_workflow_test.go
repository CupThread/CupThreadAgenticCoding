package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The CI workflow is the only place the platform-specific source files are
// ever compiled (issue #193): lock_windows.go, the !unix && !windows
// config-lock stub, and the confirm_tty variants are invisible to a
// linux-only build/test run, so a compile-breaking change to them ships
// silently unless CI compiles every GOOS target and runs the suite on
// Windows. These tests parse .github/workflows/ci.yml and fail when the
// Windows gates are dropped or hollowed out.

func readCiWorkflow(t *testing.T) releaseWorkflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	var wf releaseWorkflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse ci.yml: %v", err)
	}
	return wf
}

func stepMatching(steps []workflowStep, substr string) *workflowStep {
	for i := range steps {
		if strings.Contains(steps[i].Run, substr) {
			return &steps[i]
		}
	}
	return nil
}

// TestCiWorkflowKeepsWindowsTestJob pins the full-parity Windows job: the
// suite must build, vet, and run on a real Windows runner, because the
// Windows config lock (lock_windows.go) and the confirm_tty fallback are
// only exercised there — a hollowed-out job would reintroduce the silent
// !unix breakage of issue #193.
func TestCiWorkflowKeepsWindowsTestJob(t *testing.T) {
	wf := readCiWorkflow(t)
	windows, has := wf.Jobs["windows"]
	if !has {
		t.Fatal("ci.yml lost the windows job — the platform-specific sources are never compiled or tested again")
	}
	if windows.RunsOn != "windows-latest" {
		t.Fatalf("windows job runs-on = %q, want windows-latest", windows.RunsOn)
	}
	for _, cmd := range []string{"go build ./...", "go vet ./...", "go test ./..."} {
		if stepMatching(windows.Steps, cmd) == nil {
			t.Fatalf("windows job lost the %q step", cmd)
		}
	}
}

// TestCiWorkflowKeepsWindowsCrossCompileGate pins the cross-compile gates on
// the linux build job: GOOS=windows build+vet catch a break to the windows
// sources before the runner job does, and GOOS=plan9 is the only compile of
// the !unix && !windows config-lock stub — drop it and a break to the stub
// ships silently to plan9/js builds.
func TestCiWorkflowKeepsWindowsCrossCompileGate(t *testing.T) {
	wf := readCiWorkflow(t)
	build, has := wf.Jobs["build"]
	if !has {
		t.Fatal("ci.yml lost the build job")
	}

	gate := stepMatching(build.Steps, "GOOS=windows go build")
	if gate == nil {
		t.Fatal("build job lost the GOOS=windows cross-compile build gate")
	}
	if stepMatching(build.Steps, "GOOS=windows go vet") == nil {
		t.Fatal("build job lost the GOOS=windows cross-compile vet gate")
	}
	if stepMatching(build.Steps, "GOOS=plan9 go build") == nil {
		t.Fatal("build job lost the GOOS=plan9 gate — the !unix && !windows stub is never compiled anywhere")
	}

	// The native linux steps must survive the gate additions.
	for _, cmd := range []string{"go build ./...", "go vet ./...", "go test ./..."} {
		if stepMatching(build.Steps, cmd) == nil {
			t.Fatalf("build job lost the native %q step", cmd)
		}
	}
}
