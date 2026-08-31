package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
)

// agentSkillDirs are the per-agent directories that receive symlinks, matching
// the historical behavior of the JS CLI.
var agentSkillDirs = []string{".agents/skills", ".claude/skills", ".zcode/skills"}

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "List and link this repo's agent skills",
	}
	cmd.AddCommand(newSkillsListCmd(), newSkillsLinkCmd())
	return cmd
}

// skillsDir returns <repoRoot>/skills.
func skillsDir() (string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "skills"), nil
}

// listSkillDirs returns the sorted names of the skill directories.
func listSkillDirs() ([]string, error) {
	dir, err := skillsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read skills directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func newSkillsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the bundled agent skills",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := listSkillDirs()
			if err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(map[string]any{"count": len(names), "skills": names})
			}
			A.out.Printf("%d skills:", len(names))
			for _, n := range names {
				A.out.Printf("  • %s", n)
			}
			return nil
		},
	}
}

func newSkillsLinkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "link [targetDir]",
		Short: "Symlink the skills into .agents, .claude and .zcode of a project",
		Long: `Symlink every skill in this repository into the agent skill directories of
the target project (default: the current directory):

  <target>/.agents/skills/<skill>
  <target>/.claude/skills/<skill>
  <target>/.zcode/skills/<skill>

Existing links at those paths are replaced.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) == 1 {
				target = args[0]
			}
			absTarget, err := filepath.Abs(target)
			if err != nil {
				return fmt.Errorf("resolve target directory: %w", err)
			}
			info, err := os.Stat(absTarget)
			if err != nil || !info.IsDir() {
				return fmt.Errorf("target directory %s does not exist", absTarget)
			}

			skills, err := skillsDir()
			if err != nil {
				return err
			}
			names, err := listSkillDirs()
			if err != nil {
				return err
			}

			for _, agentDir := range agentSkillDirs {
				dest := filepath.Join(absTarget, agentDir)
				if err := os.MkdirAll(dest, 0o755); err != nil {
					return fmt.Errorf("create %s: %w", dest, err)
				}
				for _, name := range names {
					link := filepath.Join(dest, name)
					if err := os.RemoveAll(link); err != nil {
						return fmt.Errorf("replace %s: %w", link, err)
					}
					if err := os.Symlink(filepath.Join(skills, name), link); err != nil {
						return fmt.Errorf("symlink %s: %w", link, err)
					}
				}
				rel, err := filepath.Rel(mustWD(), dest)
				if err != nil {
					rel = dest
				}
				A.out.Printf("✓ Linked %d skills into %s", len(names), rel)
			}
			return nil
		},
	}
}
