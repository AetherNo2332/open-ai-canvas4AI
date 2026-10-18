import { randomUUID } from "node:crypto";
import type { Stats } from "node:fs";
import { open, stat } from "node:fs/promises";
import { hostname } from "node:os";
import { join } from "node:path";
import type { ToolSchemaArtifact } from "./tool-disclosure.js";

export type ArtifactFileState = "loaded" | "changed" | "unavailable" | "unknown";
export interface LoadedToolSchema { artifact?: ToolSchemaArtifact; loadError?: string; observeFileState?: () => Promise<ArtifactFileState> }
export interface RegisteredToolIdentity { instanceId: string; bootId: string; snapshotId?: string; loadGeneration: 1 }
export interface ToolSnapshotRegistrationRequest extends RegisteredToolIdentity {
  buildCommit: string;
  artifact: ToolSchemaArtifact;
  loadError?: string;
}

/** Read exactly once. Failed loads stay observable but cannot execute runs. */
export async function loadToolSchema(directory?: string): Promise<LoadedToolSchema> {
  if (!directory) return { loadError: "Tool schema harness directory is not configured" };
  const path = join(directory, "TOOL_SCHEMA.json");
  let baseline: Stats | undefined;
  // Metadata is diagnostic evidence only: it neither proves content equality nor reloads it.
  const observeFileState = async (): Promise<ArtifactFileState> => {
    try {
      const current = await stat(path);
      if (!baseline) return "unknown";
      return current.mtimeMs === baseline.mtimeMs && current.size === baseline.size && current.ino === baseline.ino ? "loaded" : "changed";
    } catch { return "unavailable"; }
  };
  try {
    const file = await open(path, "r");
    let contents: string;
    try {
      baseline = await file.stat();
      const buffer = Buffer.alloc(MAX_ARTIFACT_BYTES + 1);
      let length = 0;
      while (length < buffer.length) {
        const { bytesRead } = await file.read(buffer, length, buffer.length - length, null);
        if (!bytesRead) break;
        length += bytesRead;
      }
      if (length > MAX_ARTIFACT_BYTES) return { loadError: "Tool schema artifact exceeds 2 MiB", observeFileState };
      contents = buffer.toString("utf8", 0, length);
    } finally { await file.close(); }
    const artifact: unknown = JSON.parse(contents);
    if (!validArtifact(artifact)) return { loadError: "Tool schema artifact has invalid, duplicate, or excessive tool definitions", observeFileState };
    return { artifact, observeFileState };
  } catch (error) {
    // Never report host paths or file contents through the diagnostic API.
    return { loadError: error instanceof SyntaxError ? "Tool schema artifact is not valid JSON" : "Tool schema artifact could not be read", observeFileState };
  }
}

const MAX_ARTIFACT_BYTES = 2 * 1024 * 1024;
function validArtifact(value: unknown): value is ToolSchemaArtifact {
  if (!record(value) || typeof value.schemaVersion !== "string" || !value.schemaVersion.trim() || Buffer.byteLength(value.schemaVersion) > 120 || !Array.isArray(value.tools) || !value.tools.length || value.tools.length > 256 || !withinDepth(value, 0)) return false;
  const names = new Set<string>();
  const validTools = value.tools.every(tool => {
    if (!record(tool) || tool.type !== "function" || !record(tool.function)) return false;
    const fn = tool.function;
    if (typeof fn.name !== "string" || !fn.name.trim() || Buffer.byteLength(fn.name) > 160 || names.has(fn.name) || !record(fn.parameters) || fn.parameters.type !== "object" || (fn.description !== undefined && typeof fn.description !== "string")) return false;
    names.add(fn.name); return true;
  });
  if (!validTools) return false;
  // Go's JSON encoder escapes HTML and Unicode separators before enforcing its size bound.
  const encoded = JSON.stringify({ schemaVersion: value.schemaVersion, tools: value.tools })
    .replace(/[<>&\u2028\u2029]/g, character => `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`);
  return Buffer.byteLength(encoded) <= MAX_ARTIFACT_BYTES;
}
function record(value: unknown): value is Record<string, unknown> { return !!value && typeof value === "object" && !Array.isArray(value); }
function withinDepth(value: unknown, depth: number): boolean {
  if (depth > 48) return false;
  if (!value || typeof value !== "object") return true;
  return Object.values(value).every(item => withinDepth(item, depth + 1));
}

export class ToolSnapshotRegistrationRequired extends Error {}

/** Shared by every slot. Registration is single-flight and stale failures cannot clear a newer ID. */
export class ToolSnapshotRegistration {
  private snapshotId?: string;
  private pending?: Promise<void>;
  private retryAt = 0;
  private failures = 0;
  private lastError?: unknown;
  readonly request: ToolSnapshotRegistrationRequest;
  readonly eligible: boolean;
  private readonly observeFileState?: () => Promise<ArtifactFileState>;
  constructor(loaded: LoadedToolSchema, buildCommit: string, instanceId = `${hostname()}-${process.pid}`, bootId: string = randomUUID()) {
    this.observeFileState = loaded.observeFileState;
    this.eligible = !!loaded.artifact && !loaded.loadError;
    this.request = { instanceId, bootId, loadGeneration: 1, buildCommit,
      artifact: loaded.artifact ?? { schemaVersion: "unavailable", tools: [] }, ...(loaded.loadError ? { loadError: loaded.loadError } : {}) };
  }
  get identity(): RegisteredToolIdentity {
    return { instanceId: this.request.instanceId, bootId: this.request.bootId, loadGeneration: 1, ...(this.snapshotId ? { snapshotId: this.snapshotId } : {}) };
  }
  async artifactFileState(): Promise<ArtifactFileState> { return this.observeFileState ? this.observeFileState() : "unknown"; }
  invalidate(expectedId?: string): void {
    if (expectedId !== this.snapshotId) return;
    this.snapshotId = undefined;
  }
  async ensure(register: (request: ToolSnapshotRegistrationRequest) => Promise<{ snapshotId: string }>): Promise<void> {
    if (this.snapshotId) return;
    if (this.pending) return this.pending;
    if (Date.now() < this.retryAt) throw this.lastError;
    this.pending = (async () => {
      try {
        const response = await register(this.request);
        if (!response || typeof response.snapshotId !== "string" || !response.snapshotId.trim()) throw new Error("Tool snapshot registration returned no snapshot ID");
        this.snapshotId = response.snapshotId; this.failures = 0; this.retryAt = 0;
      } catch (error) {
        this.lastError = error;
        this.retryAt = Date.now() + Math.min(30_000, 1000 * 2 ** Math.min(this.failures++, 5));
        throw error;
      }
    })();
    try { await this.pending; } finally { this.pending = undefined; }
  }
}
