import { setTimeout as delay } from 'node:timers/promises';
import { CanvasLeaseLost, CanvasRunTerminated, type PiSnapshot } from './bridge.js';
import { FatalWorkerError } from './tool-disclosure.js';
import { RetryableBridgeError, safeWorkerDetail } from './worker-errors.js';

interface FailureBridge {
  failRun(run: PiSnapshot, reason: string, signal?: AbortSignal): Promise<void>;
  snapshot?(run: PiSnapshot, signal?: AbortSignal): Promise<PiSnapshot>;
}

export function workerErrorSummary(error: unknown): string {
  if (error instanceof RetryableBridgeError || error instanceof FatalWorkerError) return safeWorkerDetail(error.message);
  if (error instanceof CanvasLeaseLost || error instanceof CanvasRunTerminated) return error.name;
  if (error instanceof Error && ['Error','TypeError','SyntaxError','RangeError','ReferenceError'].includes(error.name)) return `Pi worker internal ${error.name}`;
  return 'Pi worker internal error';
}

/** One leased session has one error boundary, shared by both scheduler entry points. */
export async function runLeasedPiSession<B extends FailureBridge>(
  bridge: B, run: PiSnapshot, signal: AbortSignal,
  execute: (bridge: B, run: PiSnapshot, signal: AbortSignal) => Promise<void>,
): Promise<void> {
  try { await execute(bridge, run, signal); }
  catch (error) {
    if (signal.aborted || error instanceof CanvasLeaseLost || error instanceof CanvasRunTerminated) return;
    // Until recovery is durably acknowledged, leave ownership to expire.
    if (error instanceof RetryableBridgeError) throw error;
    const reason = error instanceof FatalWorkerError ? safeWorkerDetail(error.message) : 'Pi worker 内部异常，本轮已停止，请重试';
    const reportSignal = AbortSignal.any([signal, AbortSignal.timeout(15000)]);
    for (let attempt = 0; attempt < 3; attempt++) {
      try { await bridge.failRun(run, reason, reportSignal); throw error; }
      catch (reportError) {
        if (reportError === error) throw error;
        if (signal.aborted || reportError instanceof CanvasLeaseLost || reportError instanceof CanvasRunTerminated) return;
        if (!(reportError instanceof RetryableBridgeError) || reportSignal.aborted) {
          throw new FatalWorkerError('Pi worker 终态上报未确认：' + workerErrorSummary(reportError));
        }
        if (bridge.snapshot) {
          try {
            const current = await bridge.snapshot(run, reportSignal);
            if (['failed','completed','cancelled','rejected'].includes(current.status)) throw error;
          } catch (checkError) {
            if (checkError === error) throw error;
            if (checkError instanceof CanvasLeaseLost || checkError instanceof CanvasRunTerminated) return;
          }
        }
        if (attempt < 2) await delay(250 * (attempt + 1), undefined, {signal:reportSignal}).catch(() => {});
      }
    }
    throw new FatalWorkerError('Pi worker 终态上报未确认：后端暂时不可用');
  }
}
