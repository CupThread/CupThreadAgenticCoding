import { Command } from "commander";

export function registerSyncCommand(program: Command) {
  const sync = program.command("sync").description("Cross-project sync operations");

  sync
    .command("skills [targetDir]")
    .description("Sync skills into target directory")
    .action((targetDir) => {
      console.log("Use `cupthread skills link` to link skills into target directories.");
    });
}
