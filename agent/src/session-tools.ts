import type { SessionEntry } from "@earendil-works/pi-coding-agent";
import type { ExtensionAPI, InlineExtension } from "@earendil-works/pi-coding-agent";
import { Unsafe, type TSchema } from "typebox";
import type { CanvasToolSpec, ExecuteCanvasTool } from "./tool-disclosure.js";
import { prepareCanvasToolArguments } from "./raw-tool-calls.js";

export interface SessionToolDefinitionLike {
  name: string;
  label: string;
  description: string;
  parameters: unknown;
  prepareArguments?: (args: unknown) => Record<string, unknown>;
  executionMode?: "sequential" | "parallel";
  execute: (
    toolCallId: string,
    params: Record<string, unknown>,
    signal: AbortSignal | undefined,
  ) => Promise<{ content: { type: "text"; text: string }[]; details: unknown; terminate?: boolean }>;
}

const legacyCategoryPrefix = "agent_tools_";

/**
 * Run-scoped registry for concrete Canvas tools. Each model boundary can add
 * an advertised L1 tool; only allowed concrete tools are registered with Pi.
 * Categories remain metadata and never become tools.
 *
 * This registry is not an authorization boundary: Pi's tool_call hook checks
 * the current model batch, and Go revalidates ownership, permissions, schemas,
 * approvals, and canvas versions before executing any operation.
 */
export class SessionToolDisclosure {
  private readonly specs: CanvasToolSpec[];
  private readonly byName: Map<string, CanvasToolSpec>;
  private onAdded?: (tools: SessionToolDefinitionLike[]) => void;

  constructor(
    specs: CanvasToolSpec[],
    private readonly executeCanvas: ExecuteCanvasTool,
  ) {
    this.specs = specs.filter((spec) => spec.allowed && !spec.name.startsWith(legacyCategoryPrefix));
    this.byName = new Map(this.specs.map((spec) => [spec.name, spec]));
  }

  /** Advertised concrete tools are active from the first model step. */
  activeNames(): string[] {
    return this.specs.map((spec) => spec.name);
  }

  isVisible(name: string): boolean {
    return this.byName.has(name);
  }

  /**
   * Called only at model boundaries, after draining durable checkpoints.
   * Newly allowed tools are registered; tools no longer allowed (a mid-run
   * permission tightening) leave the active loadout. Go still rejects them.
   */
  refresh(specs: CanvasToolSpec[]): void {
    const allowed = new Set(specs.filter(spec => spec.allowed && !spec.name.startsWith(legacyCategoryPrefix)).map(spec => spec.name));
    const removed = this.specs.filter(spec => !allowed.has(spec.name));
    for (const spec of removed) this.byName.delete(spec.name);
    if (removed.length > 0) this.specs.splice(0, this.specs.length, ...this.specs.filter(spec => allowed.has(spec.name)));
    const added = specs.filter(spec => spec.allowed && !spec.name.startsWith(legacyCategoryPrefix) && !this.byName.has(spec.name));
    for (const spec of added) {this.specs.push(spec); this.byName.set(spec.name, spec);}
    if (added.length > 0 || removed.length > 0) this.onAdded?.(added.map(spec => this.makeTool(spec)));
  }

  onToolsAdded(listener: (tools: SessionToolDefinitionLike[]) => void): void {
    this.onAdded = listener;
  }

  /** Concrete tool definitions consumed by the Pi extension registration hook. */
  tools(): SessionToolDefinitionLike[] {
    return this.specs.map((spec) => this.makeTool(spec));
  }

  private makeTool(spec: CanvasToolSpec): SessionToolDefinitionLike {
    return {
      name: spec.name,
      label: spec.name,
      description: spec.description,
      parameters: Unsafe<TSchema>(spec.parameters as TSchema),
      prepareArguments: prepareCanvasToolArguments,
      executionMode: "sequential",
      execute: async (toolCallId, params, signal) => {
        prepareCanvasToolArguments(params);
        // Defense in depth if a Pi adapter invokes execute without passing the hook.
        if (!this.isVisible(spec.name)) throw new Error(`Tool ${spec.name} is not eligible for this run`);
        const receipt = await this.executeCanvas(spec.name, params, toolCallId, signal);
        if (receipt.isError && !receipt.terminate) {
          throw new Error(typeof receipt.result === "string" ? receipt.result : JSON.stringify(receipt.result));
        }
        return {
          content: [{ type: "text", text: typeof receipt.result === "string" ? receipt.result : JSON.stringify(receipt.result) }],
          details: receipt.result,
          ...(receipt.terminate ? { terminate: true } : {}),
        };
      },
    };
  }
}

/** Register all eligible tools and gate each Pi call through the extension hook. */
export function createCanvasToolsExtension(
  registry: SessionToolDisclosure,
  admissionError: (toolCallId: string, toolName: string) => string | undefined,
  nativeRead?: SessionToolDefinitionLike,
): InlineExtension {
  return {
    name: "canvas-tools",
    hidden: true,
    factory: (pi: ExtensionAPI) => {
      const register = (tool: SessionToolDefinitionLike): void => {
        pi.registerTool({
          ...tool,
          execute: async (toolCallId: string, params: Record<string, unknown>, signal?: AbortSignal) =>
            tool.execute(toolCallId, params, signal),
        } as never);
      };
      for (const tool of registry.tools()) register(tool);
      registry.onToolsAdded(tools => {
        for (const tool of tools) register(tool);
        pi.setActiveTools([...registry.activeNames(), ...(nativeRead ? [nativeRead.name] : [])]);
      });
      if (nativeRead) {
        pi.registerTool({ ...nativeRead, execute: async (callId: string, params: Record<string, unknown>, signal?: AbortSignal) =>
          nativeRead.execute(callId, params, signal) } as never);
      }
      pi.on("tool_call", (event) => {
        if (nativeRead && event.toolName === nativeRead.name) return;
        if (!registry.isVisible(event.toolName)) {
          return { block: true, terminate: true, reason: `Tool ${event.toolName} is not eligible for this run` };
        }
        const denied = admissionError(event.toolCallId, event.toolName);
        if (denied) return { block: true, reason: denied };
      });
    },
  };
}

/** Rebuild Pi session entries from Go's durable messages while preserving IDs. */
export function sessionEntriesFromMessages(runId: string, messages: readonly unknown[]): SessionEntry[] {
  return messages.map((message, index) => ({
    type: "message",
    id: `${runId}:message:${index + 1}`,
    parentId: index === 0 ? null : `${runId}:message:${index}`,
    timestamp: new Date().toISOString(),
    message,
  })) as unknown as SessionEntry[];
}
