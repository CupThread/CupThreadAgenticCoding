package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeWorkspace builds a t.TempDir workspace laid out like the canonical
// checkout: an agentic-coding root plus the sibling repos `status` reports
// on. present names the siblings that exist; each is git-initialized with one
// empty commit so branch/commit probes have something to report.
func fakeWorkspace(t *testing.T, present ...string) (workspace, root string) {
	t.Helper()
	workspace = t.TempDir()
	root = filepath.Join(workspace, "CupThreadAgenticCoding")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range present {
		dir := filepath.Join(workspace, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := gitInitWithCommit(dir); err != nil {
			t.Fatalf("init fake repo %s: %v", name, err)
		}
	}
	return workspace, root
}

func gitInitWithCommit(dir string) error {
	for _, args := range [][]string{
		{"init"},
		{"-c", "user.email=agent@example.com", "-c", "user.name=agent", "-c", "commit.gpgsign=false",
			"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return nil
}

func TestStatusReposCoverAllSixRepos(t *testing.T) {
	_, root := fakeWorkspace(t,
		"SaaS", "CupThreadSwiftSDK", "CupThreadAndroidSDK", "CupThreadReactNativeSDK", "CupThreadFlutterSDK")

	refs := statusRepos(root)
	wantPaths := map[string]string{
		"platform":         filepath.Join(root, "..", "SaaS"),
		"apple-sdk":        filepath.Join(root, "..", "CupThreadSwiftSDK"),
		"android-sdk":      filepath.Join(root, "..", "CupThreadAndroidSDK"),
		"react-native-sdk": filepath.Join(root, "..", "CupThreadReactNativeSDK"),
		"flutter-sdk":      filepath.Join(root, "..", "CupThreadFlutterSDK"),
		"agentic-coding":   root,
	}
	if len(refs) != len(wantPaths) {
		t.Fatalf("statusRepos returned %d entries, want %d: %+v", len(refs), len(wantPaths), refs)
	}
	for i, ref := range refs {
		want, ok := wantPaths[ref.name]
		if !ok {
			t.Errorf("refs[%d].name = %q, not one of the six expected repos", i, ref.name)
			continue
		}
		if ref.path != want {
			t.Errorf("refs[%d] (%s).path = %q, want %q", i, ref.name, ref.path, want)
		}
	}
}

func TestCollectRepoStatusesReportsPlatformGitState(t *testing.T) {
	_, root := fakeWorkspace(t,
		"SaaS", "CupThreadSwiftSDK", "CupThreadAndroidSDK", "CupThreadReactNativeSDK", "CupThreadFlutterSDK")

	byName := map[string]repoStatus{}
	for _, st := range collectRepoStatuses(root) {
		byName[st.Repo] = st
	}
	if len(byName) != 6 {
		t.Fatalf("collected %d statuses, want 6", len(byName))
	}
	platform, ok := byName["platform"]
	if !ok {
		t.Fatal("platform row missing from status output")
	}
	if !platform.Exists {
		t.Errorf("platform Exists = false, want true (path %s)", platform.Path)
	}
	if platform.Branch == "" {
		t.Error("platform Branch = empty, want the git-initialized branch")
	}
	if !strings.Contains(platform.LastCommit, "init") {
		t.Errorf("platform LastCommit = %q, want it to contain %q", platform.LastCommit, "init")
	}
	for _, name := range []string{"apple-sdk", "android-sdk", "react-native-sdk", "flutter-sdk", "agentic-coding"} {
		if st := byName[name]; !st.Exists {
			t.Errorf("%s Exists = false, want true (path %s)", name, st.Path)
		}
	}
}

func TestCollectRepoStatusesMissingPlatformStillReported(t *testing.T) {
	// No SaaS sibling: the row must surface as missing, not vanish, and
	// collection must not fail.
	_, root := fakeWorkspace(t,
		"CupThreadSwiftSDK", "CupThreadAndroidSDK", "CupThreadReactNativeSDK", "CupThreadFlutterSDK")

	rows := statusRows(collectRepoStatuses(root))
	sawPlatform := false
	for _, row := range rows {
		if row[0] != "platform" {
			continue
		}
		sawPlatform = true
		if row[2] != "✗ missing" {
			t.Errorf("platform state = %q, want %q", row[2], "✗ missing")
		}
	}
	if !sawPlatform {
		t.Fatal("platform row missing from status rows")
	}
	for _, row := range rows {
		if row[0] == "apple-sdk" && row[2] == "✗ missing" {
			t.Errorf("apple-sdk reported missing though present: %v", row)
		}
	}
}

func TestStatusRowsRenderPresentState(t *testing.T) {
	rows := statusRows([]repoStatus{
		{Repo: "platform", Path: "/x/SaaS", Exists: true, Branch: "main", LastCommit: "abc123 init"},
		{Repo: "apple-sdk", Path: "/x/CupThreadSwiftSDK", Exists: true},
	})
	if got := rows[0][2]; got != "✓ main · abc123 init" {
		t.Errorf("state = %q, want %q", got, "✓ main · abc123 init")
	}
	if got := rows[1][2]; got != "✓ — · —" {
		t.Errorf("state without git info = %q, want %q", got, "✓ — · —")
	}
}

func TestStatusLongHelpNamesEveryRepo(t *testing.T) {
	cmd := newStatusCmd()
	// Map each enumeration entry to the human term the help text must
	// contain, so the Long description cannot drift from the repo list again.
	wantTerm := map[string]string{
		"platform":         "SaaS",
		"apple-sdk":        "Swift",
		"android-sdk":      "Android",
		"react-native-sdk": "React Native",
		"flutter-sdk":      "Flutter",
		"agentic-coding":   "agentic-coding",
	}
	for _, ref := range statusRepos(t.TempDir()) {
		term, ok := wantTerm[ref.name]
		if !ok {
			t.Fatalf("repo %q has no help-term mapping — mention it in the Long help and add it here", ref.name)
		}
		if !strings.Contains(cmd.Long, term) {
			t.Errorf("Long help does not mention %q (required by repo %q)", term, ref.name)
		}
	}
}

func TestModulePathInParsesModuleDirective(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "clean directive",
			content: "module " + repoModulePath + "\n\ngo 1.25\n",
			want:    repoModulePath,
		},
		{
			// A trailing // comment is legal go.mod syntax on any directive;
			// it must not become part of the module path.
			name:    "trailing line comment",
			content: "module " + repoModulePath + " // agentic coding CLI + skills\n\ngo 1.25\n",
			want:    repoModulePath,
		},
		{
			name:    "tab separator",
			content: "module\t" + repoModulePath + "\n\ngo 1.25\n",
			want:    repoModulePath,
		},
		{
			name:    "quoted path",
			content: "module \"" + repoModulePath + "\"\n\ngo 1.25\n",
			want:    repoModulePath,
		},
		{
			// Lines that merely contain the letters "module" — a comment, a
			// require, a token like moduletools.example — must never shadow
			// the real directive, whichever line order they appear in.
			name: "foreign module tokens do not shadow the directive",
			content: "# module notes\n" +
				"require example.com/mod v1.0.0 // module helper\n" +
				"moduletools.example/legacy v1.0.0\n" +
				"\nmodule " + repoModulePath + "\n\ngo 1.25\n",
			want: repoModulePath,
		},
		{
			name:    "no module directive",
			content: "go 1.25\n",
			want:    "",
		},
		{
			name:    "bare module keyword without a path",
			content: "module\n\ngo 1.25\n",
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeGoMod(t, dir, tt.content)
			if got := modulePathIn(dir); got != tt.want {
				t.Errorf("modulePathIn = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestModulePathInMissingGoMod(t *testing.T) {
	if got := modulePathIn(t.TempDir()); got != "" {
		t.Errorf("modulePathIn without go.mod = %q, want %q", got, "")
	}
}

// A go.mod whose module line carries a trailing // comment — legal go.mod
// syntax — must still be recognized as this repository's checkout: repoRoot
// resolves to it and `status` runs instead of aborting with "could not
// locate the CupThreadAgenticCoding repository".
func TestStatusSucceedsWithCommentedModuleDirective(t *testing.T) {
	repo := t.TempDir()
	writeGoMod(t, repo, "module "+repoModulePath+" // agentic coding CLI + skills\n\ngo 1.25\n")
	t.Chdir(repo)

	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	if got, want := realPath(t, root), realPath(t, repo); got != want {
		t.Fatalf("repoRoot = %s, want %s", got, want)
	}

	out, err := runRoot(t, "http://127.0.0.1:1", "status", "--json")
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	var statuses []repoStatus
	if err := json.Unmarshal([]byte(out), &statuses); err != nil {
		t.Fatalf("parse status --json output %q: %v", out, err)
	}
	sawSelf := false
	for _, st := range statuses {
		if st.Repo != "agentic-coding" {
			continue
		}
		sawSelf = true
		if got, want := realPath(t, st.Path), realPath(t, repo); got != want {
			t.Errorf("agentic-coding path = %s, want %s", got, want)
		}
	}
	if !sawSelf {
		t.Fatal("agentic-coding row missing from status --json output")
	}
}
