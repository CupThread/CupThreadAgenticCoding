import { Command } from "commander";

export function registerApiCommand(program: Command) {
  const apiCmd = program.command("api").description("Inspect and test CupThread API endpoints");

  apiCmd
    .command("ping [baseUrl]")
    .description("Test reachability of CupThread API server")
    .action(async (baseUrl = "http://127.0.0.1:8787") => {
      console.log(`• Pinging CupThread API at ${baseUrl}...`);
      try {
        const res = await fetch(`${baseUrl}/api/v1/public/config/ping`, { signal: AbortSignal.timeout(3000) });
        console.log(`✓ Response status: ${res.status}`);
      } catch (err: any) {
        console.warn(`! Ping returned: ${err.message}`);
      }
    });
}
