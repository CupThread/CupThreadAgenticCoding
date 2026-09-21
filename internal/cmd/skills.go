package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// install places the named skill at link. From a checkout it symlinks; the
// embedded copy is materialized as a real directory tree.
func (s skillSource) install(name, link string) error {
	if s.embedded() {
		return copyEmbeddedSkill(name, link)
	}
	return os.Symlink(filepath.Join(s.dir, name), link)
}

// copyEmbeddedSkill copies the named skill directory from the embedded FS to
// dest, which must not exist yet.
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
	return &cobra.Command{
		Use:   "link [targetDir]",
		Short: "Link the bundled skills into .agents, .claude and .zcode of a project",
		Long: `Link every bundled skill into the agent skill directories of the target
project (default: the current directory):

  <target>/.agents/skills/<skill>
  <target>/.claude/skills/<skill>
  <target>/.zcode/skills/<skill>

The skills come from a verified CupThreadAgenticCoding checkout when one
exists (a go.mod declaring module ` + repoModulePath + ` above the current
directory or the executable) and are symlinked from there; without a
checkout — e.g. for Homebrew or go install binaries — the skills embedded
in this binary are copied into place instead. Either way, existing links
at those paths are replaced.`,
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
					if err := src.install(name, link); err != nil {
						return fmt.Errorf("link %s: %w", link, err)
					}
				}
				rel, err := filepath.Rel(mustWD(), dest)
				if err != nil {
					rel = dest
				}
				A.out.Printf("✓ Linked %d skills into %s", len(names), rel)
			}
			if src.embedded() {
				A.out.Printf("  (copied from the skills embedded in this binary; no source checkout found)")
			}
			return nil
		},
	}
}
