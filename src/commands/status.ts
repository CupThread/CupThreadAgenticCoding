import { Command } from "commander";
import { existsSync } from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { REPOS } from "../utils/paths.js";

export function registerStatusCommand(program: Command) {
  program
    .command("status")
    .alias("info")
    .description("Display health and status of CupThread SDKs and Agentic Coding tools")
    .option("--json", "Output status as JSON for AI agents")
    .action((opts) => {
      const statusData: Record<string, any> = {};

      for (const [key, dir] of Object.entries(REPOS)) {
        const exists = existsSync(dir);
        let gitBranch = null;
        let lastCommit = null;

        if (exists && existsSync(path.join(dir, ".git"))) {
          const b = spawnSync("git", ["branch", "--show-current"], { cwd: dir, encoding: "utf8" });
          gitBranch = b.stdout?.trim() || "detached";
          const l = spawnSync("git", ["log", "-1", "--oneline"], { cwd: dir, encoding: "utf8" });
          lastCommit = l.stdout?.trim() || "";
        }

        statusData[key] = {
          path: dir,
          exists,
          gitBranch,
          lastCommit
        };
      }

      if (opts.json) {
        console.log(JSON.stringify(statusData, null, 2));
        return;
      }

      console.log("\n📦 CupThread Ecosystem Status\n");
      for (const [name, info] of Object.entries(statusData)) {
        const icon = info.exists ? "✓" : "✗";
        console.log(`${icon} ${name.padEnd(12)}: ${info.path}`);
        if (info.exists && info.gitBranch) {
          console.log(`  └─ branch: ${info.gitBranch} | commit: ${info.lastCommit}`);
        }
      }
      console.log("");
    });
}
