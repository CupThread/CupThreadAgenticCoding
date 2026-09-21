package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// repoModulePath is the go.mod module path that identifies this repository's
// checkout. Foreign Go modules are never accepted.
const repoModulePath = "github.com/CupThread/CupThreadAgenticCoding"

// repoRoot locates the CupThreadAgenticCoding checkout by walking up from the
// working directory (then from the executable) until a go.mod declaring this
// repository's module path is found; unrelated go.mod files are skipped.
func repoRoot() (string, error) {
	for _, start := range []string{mustWD(), executableDir()} {
		dir := start
		for {
			if modulePathIn(dir) == repoModulePath {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("could not locate the CupThreadAgenticCoding repository (no go.mod declaring module %s above the current directory)", repoModulePath)
}

// modulePathIn reads dir/go.mod and returns its module path, or "" when the
// file is missing or declares no module.
func modulePathIn(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module")
		if !ok {
			continue
		}
		return strings.Trim(strings.TrimSpace(rest), `"`)
	}
	return ""
}

func mustWD() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// gitIn runs a git command inside dir and returns trimmed stdout.
func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type repoStatus struct {
	Repo       string `json:"repo"`
	Path       string `json:"path"`
	Exists     bool   `json:"exists"`
	Branch     string `json:"branch,omitempty"`
	LastCommit string `json:"lastCommit,omitempty"`
}

type repoRef struct {
	name string
	path string
}

// statusRepos lists the repositories `status` reports on, in table order.
// root is the agentic-coding checkout; every other repo is a sibling of it in
// the workspace layout (e.g. ~/g/CupThread/SaaS next to ~/g/CupThread/
// CupThreadAgenticCoding). The platform monorepo defines the API contract the
// CLI mirrors, so it leads the list.
func statusRepos(root string) []repoRef {
	workspace := filepath.Dir(root)
	return []repoRef{
		{"platform", filepath.Join(workspace, "SaaS")},
		{"apple-sdk", filepath.Join(workspace, "CupThreadSwiftSDK")},
		{"android-sdk", filepath.Join(workspace, "CupThreadAndroidSDK")},
		{"react-native-sdk", filepath.Join(workspace, "CupThreadReactNativeSDK")},
		{"flutter-sdk", filepath.Join(workspace, "CupThreadFlutterSDK")},
		{"agentic-coding", root},
	}
}

func collectRepoStatuses(root string) []repoStatus {
	refs := statusRepos(root)
	statuses := make([]repoStatus, 0, len(refs))
	for _, repo := range refs {
		st := repoStatus{Repo: repo.name, Path: repo.path}
		if info, err := os.Stat(repo.path); err == nil && info.IsDir() {
			st.Exists = true
			st.Branch, _ = gitIn(repo.path, "branch", "--show-current")
			st.LastCommit, _ = gitIn(repo.path, "log", "-1", "--oneline")
		}
		statuses = append(statuses, st)
	}
	return statuses
}

// statusRows renders the human-readable table body: one row per repo with a
// "✓ branch · commit" state, or "✗ missing" for absent checkouts.
func statusRows(statuses []repoStatus) [][]string {
	rows := make([][]string, 0, len(statuses))
	for _, st := range statuses {
		state := "✗ missing"
		if st.Exists {
			state = "✓ " + orDash(st.Branch) + " · " + orDash(st.LastCommit)
		}
		rows = append(rows, []string{st.Repo, st.Path, state})
	}
	return rows
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show branch/commit status of the local CupThread repositories",
		Long: `Show the git branch and last commit of the repositories in this workspace:
the SaaS platform monorepo, the four SDK checkouts (Swift, Android, React Native, and Flutter),
and this agentic-coding repository.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := repoRoot()
			if err != nil {
				return err
			}
			statuses := collectRepoStatuses(root)
			if A.structured() {
				return A.out.Structured(statuses)
			}
			A.out.Table([]string{"Repo", "Path", "Status"}, statusRows(statuses))
			return nil
		},
	}
}
