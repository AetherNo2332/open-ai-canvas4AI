import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { CANVAS_PI_WIRE_IDENTITY } from "./bridge.js";

export async function validateHarnessArtifacts(directory: string): Promise<void> {
  const identity = JSON.parse(await readFile(join(directory, "PI_WIRE_IDENTITY.json"), "utf8")) as Record<string, unknown>;
  for (const [key, expected] of Object.entries(CANVAS_PI_WIRE_IDENTITY)) {
    if (identity[key] !== expected) throw new Error(`Harness ${key} is incompatible with this worker`);
  }
}
