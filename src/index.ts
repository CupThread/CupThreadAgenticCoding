import { Command } from "commander";
import { registerStatusCommand } from "./commands/status.js";
import { registerSyncCommand } from "./commands/sync.js";
import { registerSkillsCommand } from "./commands/skills.js";
import { registerApiCommand } from "./commands/api.js";

const program = new Command();

program
  .name("cupthread")
  .description("CupThread AI-friendly CLI & cross-repo sync toolkit")
  .version("0.1.0");

registerStatusCommand(program);
registerSyncCommand(program);
registerSkillsCommand(program);
registerApiCommand(program);

program.parse(process.argv);
