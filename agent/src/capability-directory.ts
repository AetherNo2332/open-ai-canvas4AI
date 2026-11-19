import { isDeepStrictEqual } from "node:util";
import type { PiSnapshot } from "./bridge.js";
import { FatalWorkerError } from "./tool-disclosure.js";

export function validateCapabilityDirectory(run: PiSnapshot, previous?: PiSnapshot): void {
  const reject = (detail: string): never => {
    throw new FatalWorkerError(`Agent capability directory ${detail} (run ${run.runId})`);
  };
  const directory = run.capabilityDirectory;
  if (!directory || ![0, 1].includes(directory.version) || !/^[a-f0-9]{64}$/.test(directory.hash)) {
    return reject("version or identity unavailable");
  }
  if (directory.version === 1 && (!directory.definitionId ||
      !Number.isSafeInteger(directory.definitionVersion) || (directory.definitionVersion ?? 0) < 1)) {
    return reject("definition identity unavailable");
  }
  if (!Array.isArray(run.canonical?.tools) || !Array.isArray(run.tools)) {
    return reject("schema list unavailable");
  }
  const expected = new Map(run.canonical.tools.map(schema => [toolName(schema), schema]));
  const names = run.tools.map(tool => tool.name);
  if (!isDeepStrictEqual(run.toolCatalog, names) || new Set(names).size !== names.length ||
      names.length !== expected.size || run.canonical.tools.length !== expected.size) {
    return reject("allowlist disagrees with the frozen schemas");
  }
  for (const tool of run.tools) {
    const canonical = { type: "function", function: { name: tool.name, description: tool.description, parameters: tool.parameters } };
    if (!isDeepStrictEqual(expected.get(tool.name), canonical)) {
      return reject(`schema disagrees for ${tool.name}`);
    }
  }
  if (previous && (previous.runId !== run.runId ||
      !isDeepStrictEqual(previous.capabilityDirectory, directory) ||
      !isDeepStrictEqual(previous.canonical.tools, run.canonical.tools))) {
    return reject("changed during recovery");
  }
}

function toolName(schema: Record<string, unknown>): unknown {
  return (schema.function as Record<string, unknown> | undefined)?.name;
}
