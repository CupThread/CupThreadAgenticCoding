package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

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
	var force bool
	cmd := &cobra.Command{
		Use:   "link [targetDir]",
		Short: "Symlink the skills into .agents, .claude and .zcode of a project",
		Long: `Symlink every skill in this repository into the agent skill directories of
the target project (default: the current directory):

  <target>/.agents/skills/<skill>
  <target>/.claude/skills/<skill>
  <target>/.zcode/skills/<skill>

Existing symlinks at those paths are replaced. A path holding a real file or
directory (for example a locally customized copy of a skill) is never deleted:
the skill is skipped with a warning and the command exits non-zero. Pass
--force to replace such entries anyway; the previous entry is moved aside to
<skill>.bak-<timestamp> instead of being deleted, so even a forced
replacement stays recoverable.`,
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

			total := len(names) * len(agentSkillDirs)
			var skipped int
			for _, agentDir := range agentSkillDirs {
				dest := filepath.Join(absTarget, agentDir)
				if err := os.MkdirAll(dest, 0o755); err != nil {
					return fmt.Errorf("create %s: %w", dest, err)
				}
				linked := 0
				for _, name := range names {
					ok, err := installSkillLink(filepath.Join(dest, name), filepath.Join(skills, name), force)
					if err != nil {
						return err
					}
					if !ok {
						skipped++
						rel, err := filepath.Rel(mustWD(), dest)
						if err != nil {
							rel = dest
						}
						A.out.Printf("⚠ Skipped %s in %s: exists and is not a symlink (use --force to replace)", name, rel)
						continue
					}
					linked++
				}
				rel, err := filepath.Rel(mustWD(), dest)
				if err != nil {
					rel = dest
				}
				if linked == len(names) {
					A.out.Printf("✓ Linked %d skills into %s", linked, rel)
				} else {
					A.out.Printf("✓ Linked %d of %d skills into %s", linked, len(names), rel)
				}
			}
			if skipped > 0 {
				return fmt.Errorf("skipped %d of %d skill destinations: existing entries are not symlinks (use --force to replace)", skipped, total)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace existing non-symlink files/directories (moved to <skill>.bak-<timestamp>, not deleted)")
	return cmd
}

// installSkillLink points link at target as a symlink and reports whether the
// link was installed. An existing symlink — including a broken one — is
// replaced; only the link is removed, never its referent. A real file or
// directory is never deleted: without force the install is skipped (false,
// nil), with force the entry is moved to a <name>.bak-<timestamp> sibling
// first so the replacement stays recoverable.
func installSkillLink(link, target string, force bool) (bool, error) {
	info, err := os.Lstat(link)
	switch {
	case err == nil && info.Mode()&os.ModeSymlink != 0:
		if err := os.Remove(link); err != nil {
			return false, fmt.Errorf("replace %s: %w", link, err)
		}
	case err == nil:
		if !force {
			return false, nil
		}
		backup := fmt.Sprintf("%s.bak-%d", link, time.Now().UnixNano())
		if err := os.Rename(link, backup); err != nil {
			return false, fmt.Errorf("back up %s: %w", link, err)
		}
	case os.IsNotExist(err):
		// nothing to preserve; fall through to the symlink
	default:
		return false, fmt.Errorf("inspect %s: %w", link, err)
	}
	if err := os.Symlink(target, link); err != nil {
		return false, fmt.Errorf("symlink %s: %w", link, err)
	}
	return true, nil
}
