import { Command } from "commander";
import { existsSync, readdirSync, mkdirSync, symlinkSync, rmSync } from "node:fs";
import path from "node:path";
import { AGENTIC_DIR, REPOS } from "../utils/paths.js";

export function registerSkillsCommand(program: Command) {
  const skillsCmd = program.command("skills").description("Manage and install agent skills");

  skillsCmd
    .command("list")
    .description("List all available CupThread and Clerk skills")
    .action(() => {
      const skillsDir = path.join(AGENTIC_DIR, "skills");
      if (!existsSync(skillsDir)) {
        console.log("No skills found.");
        return;
      }
      const items = readdirSync(skillsDir, { withFileTypes: true })
        .filter((d) => d.isDirectory())
        .map((d) => d.name);

      console.log(`\nAvailable Skills (${items.length}):\n`);
      for (const item of items) {
        console.log(`  • ${item}`);
      }
      console.log("");
    });

  skillsCmd
    .command("link [targetDir]")
    .description("Link skills into target directory's .agents, .claude, and .zcode folders")
    .action((targetDir) => {
      const root = targetDir ? path.resolve(targetDir) : process.cwd();
      const skillsDir = path.join(AGENTIC_DIR, "skills");

      const targets = [
        path.join(root, ".agents/skills"),
        path.join(root, ".claude/skills"),
        path.join(root, ".zcode/skills")
      ];

      const skills = readdirSync(skillsDir, { withFileTypes: true })
        .filter((d) => d.isDirectory())
        .map((d) => d.name);

      for (const target of targets) {
        mkdirSync(target, { recursive: true });
        for (const skill of skills) {
          const source = path.join(skillsDir, skill);
          const dest = path.join(target, skill);
          rmSync(dest, { force: true, recursive: true });
          symlinkSync(source, dest, "dir");
        }
        console.log(`✓ Linked ${skills.length} skills into ${path.relative(root, target)}`);
      }
    });
}
