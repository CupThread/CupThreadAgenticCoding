package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// repoRoot locates the CupThreadAgenticCoding checkout by walking up from the
// working directory (then from the executable) until a go.mod is found.
func repoRoot() (string, error) {
	for _, start := range []string{mustWD(), executableDir()} {
		if dir, ok := findFileUp(start, "go.mod"); ok {
			return dir, nil
		}
	}
	return "", errors.New("could not locate the CupThreadAgenticCoding repository (no go.mod above the current directory)")
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

func findFileUp(start, name string) (string, bool) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
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

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show branch/commit status of the local CupThread repositories",
		Long: `Show the git branch and last commit of the repositories in this workspace:
the Swift SDK, the Android SDK, and this agentic-coding repository.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := repoRoot()
			if err != nil {
				return err
			}
			workspace := filepath.Dir(root)
			repos := []struct {
				name string
				path string
			}{
				{"apple-sdk", filepath.Join(workspace, "CupThreadSwiftSDK")},
				{"android-sdk", filepath.Join(workspace, "CupThreadAndroidSDK")},
				{"react-native-sdk", filepath.Join(workspace, "CupThreadReactNativeSDK")},
				{"flutter-sdk", filepath.Join(workspace, "CupThreadFlutterSDK")},
				{"agentic-coding", root},
			}

			statuses := make([]repoStatus, 0, len(repos))
			for _, repo := range repos {
				st := repoStatus{Repo: repo.name, Path: repo.path}
				if info, err := os.Stat(repo.path); err == nil && info.IsDir() {
					st.Exists = true
					st.Branch, _ = gitIn(repo.path, "branch", "--show-current")
					st.LastCommit, _ = gitIn(repo.path, "log", "-1", "--oneline")
				}
				statuses = append(statuses, st)
			}

			if A.structured() {
				return A.out.Structured(statuses)
			}
			rows := make([][]string, 0, len(statuses))
			for _, st := range statuses {
				state := "✗ missing"
				if st.Exists {
					state = "✓ " + orDash(st.Branch) + " · " + orDash(st.LastCommit)
				}
				rows = append(rows, []string{st.Repo, st.Path, state})
			}
			A.out.Table([]string{"Repo", "Path", "Status"}, rows)
			return nil
		},
	}
}
