export interface CanvasToolSpec {
  name: string;
  description: string;
  parameters: Record<string, unknown>;
  category?: string;
  allowed: boolean;
}

export interface CanvasToolReceipt {
  result: unknown;
  isError?: boolean;
  terminate?: boolean;
}

export type ExecuteCanvasTool = (name: string, args: Record<string, unknown>, callId: string, signal?: AbortSignal) => Promise<CanvasToolReceipt>;

/**
 * 重试无用的配置/协议错误：工具 schema 与制品漂移、制品缺失、Harness 读取失败等。
 * worker 遇到这类错误必须上报给 Go（否则运行会一直停在 running，前端表现为
 * "输出完了却永远在运行"），而网络抖动等瞬时错误应留待租约过期后重试。
 */
export class FatalWorkerError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "FatalWorkerError";
  }
}

export interface ToolSchemaArtifact {
  schemaVersion: string;
  tools: { type: string; function: { name: string; parameters: Record<string, unknown> } }[];
}

/**
 * 用 Go 生成的共用 schema 制品（agent/harness/TOOL_SCHEMA.json）交叉校验服务端快照。
 * 双方漂移时宁可启动失败，也不要让 worker 拿着与 Go 不一致的工具定义跑模型：
 * 那会让"模型请求里的工具"与"服务端合格目录"悄悄打架，只在运行期表现为难查的准入拒绝。
 */
export function assertToolSnapshotMatchesSchema(specs: CanvasToolSpec[], artifact: ToolSchemaArtifact): void {
  if (!artifact.schemaVersion || artifact.tools.length === 0) {
    throw new FatalWorkerError("tool schema artifact is empty");
  }
  const declared = new Map(artifact.tools.map((tool) => [tool.function.name, tool.function.parameters]));
  const unknown = specs.filter((spec) => !declared.has(spec.name)).map((spec) => spec.name);
  if (unknown.length > 0) {
    throw new FatalWorkerError(`server snapshot has tools missing from ${artifact.schemaVersion}: ${unknown.slice(0, 5).join(", ")}`);
  }
  // 只比名字不够：同名工具的**参数定义**漂移同样会让服务端预检与模型看到的 schema 不一致，
  // 表现为难查的准入拒绝。这里做稳定序列化后逐字比较。
  const drifted = specs
    .filter((spec) => stableJson(spec.parameters) !== stableJson(declared.get(spec.name) ?? {}))
    .map((spec) => spec.name);
  if (drifted.length > 0) {
    throw new FatalWorkerError(`server snapshot schema differs from ${artifact.schemaVersion}: ${drifted.slice(0, 5).join(", ")}`);
  }
}

/** 键序无关的稳定序列化：Go 与 Node 的 map/对象键序不保证一致。 */
function stableJson(value: unknown): string {
  if (value === null || typeof value !== "object") return JSON.stringify(value) ?? "null";
  if (Array.isArray(value)) return `[${value.map(stableJson).join(",")}]`;
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, item]) => item !== undefined)
    .sort(([left], [right]) => (left < right ? -1 : left > right ? 1 : 0));
  return `{${entries.map(([key, item]) => `${JSON.stringify(key)}:${stableJson(item)}`).join(",")}}`;
}
