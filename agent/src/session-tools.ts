import type { SessionEntry } from "@earendil-works/pi-coding-agent";
import type { ExtensionAPI, InlineExtension } from "@earendil-works/pi-coding-agent";
import { Unsafe, type TSchema } from "typebox";
import type { CanvasToolSpec, ExecuteCanvasTool } from "./tool-disclosure.js";

export interface SessionToolDefinitionLike {
  name: string;
  label: string;
  description: string;
  parameters: unknown;
  executionMode?: "sequential" | "parallel";
  execute: (
    toolCallId: string,
    params: Record<string, unknown>,
    signal: AbortSignal | undefined,
  ) => Promise<{ content: { type: "text"; text: string }[]; details: unknown; terminate?: boolean }>;
}

const legacyCategoryPrefix = "agent_tools_";

/**
 * Run-scoped registry for concrete Canvas tools. The snapshot already contains
 * the permission/capability-filtered set, so every allowed tool is registered
 * with Pi at session startup. Categories remain metadata and never become tools.
 *
 * This registry is not an authorization boundary: Pi's tool_call hook checks
 * the current model batch, and Go revalidates ownership, permissions, schemas,
 * approvals, and canvas versions before executing any operation.
 */
export class SessionToolDisclosure {
  readonly previousStepCalls: string[] = [];
  private readonly specs: CanvasToolSpec[];
  private readonly byName: Map<string, CanvasToolSpec>;

  constructor(
    specs: CanvasToolSpec[],
    private readonly executeCanvas: ExecuteCanvasTool,
    private readonly previousStepTemplate = "",
  ) {
    this.specs = specs.filter((spec) => spec.allowed && !spec.name.startsWith(legacyCategoryPrefix));
    this.byName = new Map(this.specs.map((spec) => [spec.name, spec]));
  }

  /** Every eligible concrete tool is active from the first model step. */
  activeNames(): string[] {
    return this.specs.map((spec) => spec.name);
  }

  isVisible(name: string): boolean {
    return this.byName.has(name);
  }

  /** Concrete tool definitions consumed by the Pi extension registration hook. */
  tools(): SessionToolDefinitionLike[] {
    return this.specs.map((spec) => this.makeTool(spec));
  }

  recordStepCalls(names: string[]): void {
    const concrete = names.filter((name) => this.byName.has(name));
    this.previousStepCalls.splice(0, this.previousStepCalls.length, ...[...new Set(concrete)].slice(0, 6));
  }

  /**
   * Add the previous step's concrete tool call names to the actual model-facing
   * function descriptions. This keeps the short-lived context in the schema
   * sent upstream and naturally resets when a new run creates a new registry.
   */
  decorateCanonicalTools(tools: Record<string, unknown>[]): Record<string, unknown>[] {
    if (!this.previousStepTemplate || this.previousStepCalls.length === 0) return tools;
    const record = this.previousStepTemplate.replace("{names}", this.previousStepCalls.join("、"));
    return tools.map((tool) => {
      const functionSpec = tool.function;
      if (!functionSpec || typeof functionSpec !== "object") return tool;
      const fn = functionSpec as Record<string, unknown>;
      if (typeof fn.name !== "string" || !this.byName.has(fn.name)) return tool;
      return { ...tool, function: { ...fn, description: `${String(fn.description || "")} ${record}`.trim() } };
    });
  }

  private makeTool(spec: CanvasToolSpec): SessionToolDefinitionLike {
    return {
      name: spec.name,
      label: spec.name,
      description: spec.description,
      parameters: Unsafe<TSchema>(spec.parameters as TSchema),
      executionMode: "sequential",
      execute: async (toolCallId, params, signal) => {
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
): InlineExtension {
  return {
    name: "canvas-tools",
    hidden: true,
    factory: (pi: ExtensionAPI) => {
      for (const tool of registry.tools()) {
        pi.registerTool({
          ...tool,
          execute: async (toolCallId: string, params: Record<string, unknown>, signal?: AbortSignal) =>
            tool.execute(toolCallId, params, signal),
        } as never);
      }
      pi.on("tool_call", (event) => {
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
