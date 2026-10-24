import type { ToolCall } from "@earendil-works/pi-ai";
import type { PiToolCall } from "./bridge.js";

const rejectedArguments = new WeakMap<object, CanvasToolArgumentsRejected>();
const marker = "__canvasInvalidToolArguments";
type InvalidCall = ToolCall & { canvasInvalidArguments?: { rawArguments: string } };

/** Safe diagnostic only: JSON.parse messages may contain prompt or credential excerpts. */
export class CanvasToolArgumentsRejected extends Error {
  readonly code = "invalid_tool_arguments";
  constructor(readonly issue: "invalid_json" | "type_mismatch", readonly position?: number) {
    super(`Tool arguments rejected: ${issue}${position === undefined ? "" : ` at position ${position}`}. ` +
      "This call was not executed. Correct only the rejected call; do not repeat successful writes.");
    this.name = "CanvasToolArgumentsRejected";
  }
}

export function decodeCanvasToolArguments(raw: string): Record<string, unknown> {
  let parsed: unknown;
  try { parsed = JSON.parse(raw); }
  catch (error) {
    const match = error instanceof SyntaxError ? /\bposition\s+(\d+)\b/.exec(error.message) : undefined;
    const position = match ? Number(match[1]) : undefined;
    throw new CanvasToolArgumentsRejected("invalid_json", position !== undefined && position <= raw.length ? position : undefined);
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new CanvasToolArgumentsRejected("type_mismatch");
  return parsed as Record<string, unknown>;
}

/** The diagnostic object is SDK-local and never becomes a business argument. */
export function createCanvasToolCall(call: PiToolCall): ToolCall {
  const base = { type: "toolCall" as const, id: call.id, name: call.function.name,
    ...(call.thoughtSignature ? { thoughtSignature: call.thoughtSignature } : {}) };
  try { return { ...base, arguments: decodeCanvasToolArguments(call.function.arguments) as ToolCall["arguments"] }; }
  catch (error) {
    if (!(error instanceof CanvasToolArgumentsRejected)) throw error;
    const args = { [marker]: true };
    rejectedArguments.set(args, error);
    return { ...base, arguments: args, canvasInvalidArguments: { rawArguments: call.function.arguments } } as InvalidCall;
  }
}

/** Rehydrate only our complete outer annotation, never a similarly named model argument. */
export function canvasToolCallToRaw(call: ToolCall): PiToolCall {
  const info = (call as InvalidCall).canvasInvalidArguments;
  let raw = JSON.stringify(call.arguments);
  if (info && Object.keys(info).length === 1 && typeof info.rawArguments === "string" &&
      call.arguments && Object.keys(call.arguments).length === 1 && call.arguments[marker] === true) {
    try { decodeCanvasToolArguments(info.rawArguments); }
    catch (error) {
      if (!(error instanceof CanvasToolArgumentsRejected)) throw error;
      rejectedArguments.set(call.arguments, error);
      raw = info.rawArguments;
    }
  }
  return { id: call.id, type: "function", function: { name: call.name, arguments: raw },
    ...(call.thoughtSignature ? { thoughtSignature: call.thoughtSignature } : {}) };
}

/** Pi calls this public hook before schema validation, including zero-argument tools. */
export function prepareCanvasToolArguments(args: unknown): Record<string, unknown> {
  const rejected = args && typeof args === "object" ? rejectedArguments.get(args) : undefined;
  if (rejected) throw rejected;
  return args as Record<string, unknown>;
}
