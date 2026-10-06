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
  private readonly specs: CanvasToolSpec[];
  private readonly byName: Map<string, CanvasToolSpec>;

  constructor(
    specs: CanvasToolSpec[],
    private readonly executeCanvas: ExecuteCanvasTool,
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
  nativeRead?: SessionToolDefinitionLike,
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
