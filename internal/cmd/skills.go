package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/CupThread/CupThreadAgenticCoding/skills"
	"github.com/spf13/cobra"
)

// agentSkillDirs are the per-agent directories that receive symlinks, matching
// the historical behavior of the JS CLI.
var agentSkillDirs = []string{".agents/skills", ".claude/skills", ".zcode/skills"}

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "List and link this repo's agent skills",
		Long: `List and install the bundled CupThread agent skills.

The skills are read from a verified CupThreadAgenticCoding checkout when one
exists (a go.mod declaring module ` + repoModulePath + ` above the current
directory or the executable); otherwise the copy embedded in this binary is
used, so Homebrew and go install builds work from anywhere.`,
	}
	cmd.AddCommand(newSkillsListCmd(), newSkillsLinkCmd())
	return cmd
}

// skillSource describes where the bundled skills are read from: a verified
// source checkout on disk, or the copy embedded in the binary.
type skillSource struct {
	// dir is the checkout's skills directory; empty in embedded mode.
	dir string
}

// resolveSkillSource returns the checkout's skills directory, or the embedded
// source when no verified CupThreadAgenticCoding checkout exists.
func resolveSkillSource() skillSource {
	root, err := repoRoot()
	if err != nil {
		return skillSource{}
	}
	dir := filepath.Join(root, "skills")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return skillSource{}
	}
	return skillSource{dir: dir}
}

func (s skillSource) embedded() bool { return s.dir == "" }

// label names the source for human output.
func (s skillSource) label() string {
	if s.embedded() {
		return "embedded copy"
	}
	return s.dir
}

// list returns the sorted names of the skill directories.
func (s skillSource) list() ([]string, error) {
	var entries []fs.DirEntry
	var err error
	if s.embedded() {
		entries, err = skills.FS.ReadDir(".")
		if err != nil {
			return nil, fmt.Errorf("read embedded skills: %w", err)
		}
	} else {
		entries, err = os.ReadDir(s.dir)
		if err != nil {
			return nil, fmt.Errorf("read skills directory: %w", err)
		}
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

// install places the named skill at link, whether the source is a checkout
// (symlink) or the embedded copy (materialized as a real directory tree).
// Both modes honor the non-destructive contract of clearForInstall; the
// return reports whether the install happened.
func (s skillSource) install(name, link string, force bool) (bool, error) {
	ok, err := clearForInstall(link, force)
	if err != nil || !ok {
		return ok, err
	}
	if s.embedded() {
		return true, copyEmbeddedSkill(name, link)
	}
	if err := os.Symlink(filepath.Join(s.dir, name), link); err != nil {
		return false, fmt.Errorf("symlink %s: %w", link, err)
	}
	return true, nil
}

// clearForInstall makes room at link for a fresh install and reports whether
// the install may proceed. An existing symlink — including a broken one — is
// replaced; only the link is removed, never its referent. A real file or
// directory is never deleted: without force the install is skipped (false,
// nil), with force the entry is moved to a <name>.bak-<timestamp> sibling
// first so the replacement stays recoverable.
func clearForInstall(link string, force bool) (bool, error) {
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
		// nothing to preserve; the path is free
	default:
		return false, fmt.Errorf("inspect %s: %w", link, err)
	}
	return true, nil
}

// copyEmbeddedSkill copies the named skill directory from the embedded FS to
// dest, whose path clearForInstall has already cleared.
func copyEmbeddedSkill(name, dest string) error {
	return fs.WalkDir(skills.FS, name, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dest, strings.TrimPrefix(path, name))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := skills.FS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func newSkillsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the bundled agent skills",
		Long: `List the bundled agent skills.

The skills come from a verified CupThreadAgenticCoding checkout when one
exists, and from the copy embedded in this binary otherwise; the active
source is included in the output.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			src := resolveSkillSource()
			names, err := src.list()
			if err != nil {
				return err
			}
			if A.structured() {
				source := "checkout"
				if src.embedded() {
					source = "embedded"
				}
				return A.out.Structured(map[string]any{"count": len(names), "skills": names, "source": source})
			}
			A.out.Printf("%d skills (%s):", len(names), src.label())
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
		Short: "Link the bundled skills into .agents, .claude and .zcode of a project",
		Long: `Link every bundled skill into the agent skill directories of the target
project (default: the current directory):

  <target>/.agents/skills/<skill>
  <target>/.claude/skills/<skill>
  <target>/.zcode/skills/<skill>

Existing symlinks at those paths are replaced. A path holding a real file or
directory (for example a locally customized copy of a skill) is never deleted:
the skill is skipped with a warning and the command exits non-zero. Pass
--force to replace such entries anyway; the previous entry is moved aside to
<skill>.bak-<timestamp> instead of being deleted, so even a forced
replacement stays recoverable.

The skills come from a verified CupThreadAgenticCoding checkout when one
exists (a go.mod declaring module ` + repoModulePath + ` above the current
directory or the executable) and are symlinked from there; without a
checkout — e.g. for Homebrew or go install binaries — the skills embedded
in this binary are copied into place instead.`,
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

			src := resolveSkillSource()
			names, err := src.list()
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
					link := filepath.Join(dest, name)
					ok, err := src.install(name, link, force)
					if err != nil {
						return fmt.Errorf("link %s: %w", link, err)
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
			if src.embedded() {
				A.out.Printf("  (copied from the skills embedded in this binary; no source checkout found)")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace existing non-symlink files/directories (moved to <skill>.bak-<timestamp>, not deleted)")
	return cmd
}
