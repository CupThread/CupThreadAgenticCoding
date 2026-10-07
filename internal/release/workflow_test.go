package release

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The release workflow is the trust boundary for the release credentials
// (issue #140): the triggering tag is untrusted input, so tag-controlled Go
// code must never share a job with a secret, and a tag that does not resolve
// to a commit on the approved release branch must pass an ancestry gate
// before any secret-bearing step starts. These tests parse
// .github/workflows/release.yml and fail when a refactor moves a `go`
// invocation into a credential-bearing job, drops or reorders a gate, breaks
// the artifact hand-off, or lets the workflow's embedded formula template
// drift from Formula.Render.

type releaseWorkflow struct {
	Permissions map[string]string      `yaml:"permissions"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Permissions map[string]string `yaml:"permissions"`
	Needs       needsList         `yaml:"needs"`
	Env         map[string]string `yaml:"env"`
	Steps       []workflowStep    `yaml:"steps"`
}

type workflowStep struct {
	Name string               `yaml:"name"`
	Uses string               `yaml:"uses"`
	With map[string]yaml.Node `yaml:"with"`
	Run  string               `yaml:"run"`
	Env  map[string]string    `yaml:"env"`
}

// needsList accepts both `needs: build` and `needs: [build]`.
type needsList []string

func (n *needsList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		*n = needsList{node.Value}
		return nil
	}
	var list []string
	if err := node.Decode(&list); err != nil {
		return err
	}
	*n = list
	return nil
}

func (n needsList) has(job string) bool {
	for _, dep := range n {
		if dep == job {
			return true
		}
	}
	return false
}

var (
	goExecRe    = regexp.MustCompile(`\bgo\s+(build|test|vet|run)\b`)
	secretRefRe = regexp.MustCompile(`secrets\.[A-Za-z_][A-Za-z0-9_]*`)
	// The ancestry gate every job must pass before tag code runs (build) or
	// before any secret enters a step environment (publish).
	ancestryGate = "git merge-base --is-ancestor"
)

func (s workflowStep) runsGo() bool { return goExecRe.MatchString(s.Run) }

func (s workflowStep) referencesSecret() bool {
	for _, v := range s.Env {
		if secretRefRe.MatchString(v) {
			return true
		}
	}
	return false
}

func (j workflowJob) runsGo() bool {
	for _, s := range j.Steps {
		if s.runsGo() {
			return true
		}
	}
	return false
}

func (j workflowJob) referencesSecret() bool {
	for _, v := range j.Env {
		if secretRefRe.MatchString(v) {
			return true
		}
	}
	for _, s := range j.Steps {
		if s.referencesSecret() {
			return true
		}
	}
	return false
}

func readReleaseWorkflow(t *testing.T) releaseWorkflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	var wf releaseWorkflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse release.yml: %v", err)
	}
	return wf
}

func stepIndex(steps []workflowStep, match func(workflowStep) bool) int {
	for i, s := range steps {
		if match(s) {
			return i
		}
	}
	return -1
}

// TestReleaseWorkflowKeepsTagCodeAwayFromReleaseSecrets pins the core
// invariant of issue #140: only the credential-free build job may compile or
// execute tag-controlled Go code, and the credential-bearing publish job may
// only run trusted tools on verified artifacts.
func TestReleaseWorkflowKeepsTagCodeAwayFromReleaseSecrets(t *testing.T) {
	wf := readReleaseWorkflow(t)

	if want := (map[string]string{"contents": "read"}); !reflect.DeepEqual(wf.Permissions, want) {
		t.Fatalf("top-level permissions = %v, want %v (least privilege by default)", wf.Permissions, want)
	}

	build, hasBuild := wf.Jobs["build"]
	publish, hasPublish := wf.Jobs["publish"]
	if !hasBuild || !hasPublish {
		t.Fatalf("release.yml must define the build (untrusted) and publish (credential-bearing) jobs")
	}
	if len(wf.Jobs) != 2 {
		t.Fatalf("release.yml defines %d jobs; want exactly build and publish — any new job must keep tag-controlled code away from secrets", len(wf.Jobs))
	}

	// build: executes tag-controlled Go, so it must be credential-free and
	// read-only.
	if !build.runsGo() {
		t.Fatal("build job no longer compiles or tests the tag checkout")
	}
	if build.referencesSecret() {
		t.Fatal("build job executes tag-controlled Go code but references secrets.* — tag code could read a release credential")
	}
	if want := (map[string]string{"contents": "read"}); !reflect.DeepEqual(build.Permissions, want) {
		t.Fatalf("build permissions = %v, want %v (the untrusted job stays read-only)", build.Permissions, want)
	}
	if stepIndex(build.Steps, func(s workflowStep) bool {
		return strings.Contains(s.Run, "go run ./cmd/genformula")
	}) < 0 {
		t.Fatal("build job no longer generates the formula from the tag — the publish job's byte-comparison would pass vacuously")
	}

	// publish: holds the credentials, so it must not execute repository-
	// controlled code.
	if !publish.Needs.has("build") {
		t.Fatal("publish job must need the gated build job")
	}
	if !publish.referencesSecret() {
		t.Fatal("publish job no longer holds any release credential — has the release flow moved?")
	}
	if publish.runsGo() {
		t.Fatal("publish job holds release credentials but runs `go` — repository-controlled code must not execute with secrets in its environment")
	}
	for _, s := range publish.Steps {
		// Prose may mention the generator; only the `go run ./cmd/genformula`
		// invocation form executes tag-controlled code.
		if strings.Contains(s.Run, "cmd/genformula") {
			t.Fatalf("publish step %q executes the tag's formula generator", s.Name)
		}
	}
	if want := (map[string]string{"contents": "write"}); !reflect.DeepEqual(publish.Permissions, want) {
		t.Fatalf("publish permissions = %v, want %v (only contents:write for releases)", publish.Permissions, want)
	}
}

// TestReleaseWorkflowGatesTagBeforeTagCodeAndSecrets pins the ordering
// invariant of issue #140: both jobs verify the tag resolves to a commit on
// the approved release branch before the build job runs tag code and before
// the publish job exposes any secret.
func TestReleaseWorkflowGatesTagBeforeTagCodeAndSecrets(t *testing.T) {
	wf := readReleaseWorkflow(t)
	build, publish := wf.Jobs["build"], wf.Jobs["publish"]

	gate := stepIndex(build.Steps, func(s workflowStep) bool { return strings.Contains(s.Run, ancestryGate) })
	if gate < 0 {
		t.Fatal("build job lost the origin/main ancestry gate (git merge-base --is-ancestor)")
	}
	if !strings.Contains(build.Steps[gate].Run, "exit 1") {
		t.Fatal("build job's ancestry gate no longer fails the run on a rejected tag")
	}
	for i, s := range build.Steps {
		if s.runsGo() && i < gate {
			t.Fatalf("build step %q executes tag-controlled Go before the ancestry gate (step %d)", s.Name, gate)
		}
	}

	pgate := stepIndex(publish.Steps, func(s workflowStep) bool { return strings.Contains(s.Run, ancestryGate) })
	if pgate < 0 {
		t.Fatal("publish job lost the origin/main ancestry gate (git merge-base --is-ancestor)")
	}
	if !strings.Contains(publish.Steps[pgate].Run, "exit 1") {
		t.Fatal("publish job's ancestry gate no longer fails the run on a rejected tag")
	}
	for i, s := range publish.Steps {
		if s.referencesSecret() && i < pgate {
			t.Fatalf("publish step %q exposes a secret before the ancestry gate (step %d)", s.Name, pgate)
		}
	}
}

// TestReleaseWorkflowArtifactHandoff pins the build→publish artifact wiring:
// publish must consume exactly the artifact build produces, so a renamed
// artifact cannot silently publish stale or empty outputs.
func TestReleaseWorkflowArtifactHandoff(t *testing.T) {
	wf := readReleaseWorkflow(t)
	build, publish := wf.Jobs["build"], wf.Jobs["publish"]

	upload := stepIndex(build.Steps, func(s workflowStep) bool {
		return strings.Contains(s.Uses, "actions/upload-artifact")
	})
	download := stepIndex(publish.Steps, func(s workflowStep) bool {
		return strings.Contains(s.Uses, "actions/download-artifact")
	})
	if upload < 0 || download < 0 {
		t.Fatalf("artifact steps missing (upload=%d, download=%d)", upload, download)
	}
	upName, downName := build.Steps[upload].With["name"], publish.Steps[download].With["name"]
	var upVal, downVal string
	if upName.Decode(&upVal) != nil || downName.Decode(&downVal) != nil || upVal == "" || upVal != downVal {
		t.Fatalf("artifact names do not match: build uploads %v, publish downloads %v", upName.Value, downName.Value)
	}
}

// TestReleaseWorkflowFormulaTemplateMatchesRender pins the workflow's
// trusted-formula heredoc to release.Formula.Render: the tap and the in-repo
// mirror must only ever receive bytes the workflow itself can vouch for, so
// template drift in either direction must fail CI rather than a release.
func TestReleaseWorkflowFormulaTemplateMatchesRender(t *testing.T) {
	wf := readReleaseWorkflow(t)
	publish := wf.Jobs["publish"]

	idx := stepIndex(publish.Steps, func(s workflowStep) bool { return strings.Contains(s.Run, "<<RUBY") })
	if idx < 0 {
		t.Fatal("publish job lost the trusted-formula heredoc")
	}
	lines := strings.Split(publish.Steps[idx].Run, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, "<<RUBY") {
			start = i
			break
		}
	}
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "RUBY" {
			end = i
			break
		}
	}
	if start < 0 || end < 0 {
		t.Fatal("could not extract the heredoc body from the render step")
	}

	const (
		tmplVersion = "9.9.9"
		tmplSHA     = "ab"
	)
	tmpl := strings.Join(lines[start+1:end], "\n") + "\n"
	tmpl = strings.ReplaceAll(tmpl, "${GITHUB_REPOSITORY}", defaultRepo)
	tmpl = strings.ReplaceAll(tmpl, "${VERSION}", tmplVersion)
	tmpl = strings.ReplaceAll(tmpl, "${FORMULA_SHA256}", strings.Repeat(tmplSHA, 32))
	if strings.Contains(tmpl, "${") {
		t.Fatalf("workflow formula template still contains unexpanded shell variables:\n%s", tmpl)
	}

	want, err := (Formula{Version: tmplVersion, SHA256: strings.Repeat(tmplSHA, 32)}).Render()
	if err != nil {
		t.Fatalf("Render() errored: %v", err)
	}
	if tmpl != want {
		t.Fatalf("workflow formula template drifted from Formula.Render:\n--- workflow template ---\n%s\n--- Formula.Render ---\n%s", tmpl, want)
	}
}
