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
 * 重试无用的配置/协议错误：Harness 读取失败或固定技能身份无效等。
 * worker 遇到这类错误必须上报给 Go（否则运行会一直停在 running，前端表现为
 * "输出完了却永远在运行"），而网络抖动等瞬时错误应留待租约过期后重试。
 */
export class FatalWorkerError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "FatalWorkerError";
  }
}
