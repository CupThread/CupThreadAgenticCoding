import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
export const AGENTIC_DIR = path.resolve(__dirname, "../..");
export const WORKSPACE_ROOT = path.resolve(AGENTIC_DIR, "..");

export const REPOS = {
  appleSdk: path.join(WORKSPACE_ROOT, "CupThreadSwiftSDK"),
  androidSdk: path.join(WORKSPACE_ROOT, "CupThreadAndroidSDK"),
  agentic: AGENTIC_DIR,
};
