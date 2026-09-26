import type { Agent, AgentTool } from "@earendil-works/pi-agent-core";
import { Unsafe, type TSchema } from "typebox";

export interface CanvasToolSpec {
  name: string;
  description: string;
  parameters: Record<string, unknown>;
  category?: string;
  allowed: boolean;
}

export interface CanvasToolReceipt {
  result: unknown;
  terminate?: boolean;
}

export type ExecuteCanvasTool = (name: string, args: Record<string, unknown>, callId: string, signal?: AbortSignal) => Promise<CanvasToolReceipt>;

const categoryPrefix = "agent_tools_";

/** Builds the executable loadout from the same eligibility snapshot used by Go. */
export class ToolDisclosure {
  readonly opened = new Set<string>();
  readonly previousStepCalls: string[] = [];
  private agent?: Agent;
  private readonly specs: CanvasToolSpec[];

  constructor(specs: CanvasToolSpec[], private readonly executeCanvas: ExecuteCanvasTool,
    private readonly previousStepTemplate = "") {
    this.specs = specs.filter((spec) => spec.allowed);
  }

  attach(agent: Agent): void {
    this.agent = agent;
    agent.state.tools = this.visibleTools();
    const previous = agent.prepareNextTurnWithContext;
    agent.prepareNextTurnWithContext = async (turn, signal) => {
      const update = await previous?.(turn, signal);
      return { ...update, context: { ...(update?.context || turn.context), tools: this.visibleTools() } };
    };
  }

  isVisible(name: string): boolean {
    const spec = this.specs.find((item) => item.name === name);
    if (!spec) return false;
    if (name.startsWith(categoryPrefix)) return this.specs.some((item) => item.category === name);
    return Boolean(spec.category && this.opened.has(spec.category));
  }

  visibleTools(): AgentTool<any>[] {
    return this.specs.filter((spec) => this.isVisible(spec.name)).map((spec) => this.makeTool(spec));
  }

  recordStepCalls(names: string[]): void {
    this.previousStepCalls.splice(0, this.previousStepCalls.length, ...names.slice(0, 6));
  }

  private makeTool(spec: CanvasToolSpec): AgentTool<any> {
    const description = spec.name.startsWith(categoryPrefix) && this.previousStepCalls.length && this.previousStepTemplate
      ? `${spec.description} ${this.previousStepTemplate.replace("{names}", this.previousStepCalls.join("、"))}` : spec.description;
    return {
      name: spec.name,
      label: spec.name,
      description,
      parameters: Unsafe<Record<string, unknown>>(spec.parameters as TSchema),
      executionMode: "sequential",
      replay: "safe",
      execute: async (callId, args, signal) => {
        if (!this.isVisible(spec.name)) throw new Error(`Tool ${spec.name} is not disclosed`);
        if (spec.name.startsWith(categoryPrefix)) {
          const receipt = await this.executeCanvas(spec.name, args as Record<string, unknown>, callId, signal);
          this.opened.add(spec.name);
          if (this.agent) this.agent.state.tools = this.visibleTools();
          const names = this.specs.filter((item) => item.category === spec.name).map((item) => item.name);
          return { content: [{ type: "text", text: JSON.stringify(receipt.result) }], details: { category: spec.name, tools: names } };
        }
        const receipt = await this.executeCanvas(spec.name, args as Record<string, unknown>, callId, signal);
        return {
          content: [{ type: "text", text: typeof receipt.result === "string" ? receipt.result : JSON.stringify(receipt.result) }],
          details: receipt.result,
          ...(receipt.terminate ? { terminate: true } : {}),
        };
      },
    };
  }
}
