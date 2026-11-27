import { setTimeout as delay } from 'node:timers/promises';
import { CanvasLeaseLost, CanvasRunTerminated, type PiSnapshot } from './bridge.js';
import { FatalWorkerError } from './tool-disclosure.js';
import { RetryableBridgeError, safeWorkerDetail } from './worker-errors.js';

interface FailureBridge {
  failRun(run: PiSnapshot, reason: string, signal?: AbortSignal): Promise<void>;
  snapshot?(run: PiSnapshot, signal?: AbortSignal): Promise<PiSnapshot>;
  reportRecovery?(run: PiSnapshot, error: RetryableBridgeError, signal?: AbortSignal): Promise<void>;
}

// Worker ids are part of the backend lease contract: `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`
// (see CanvasBridge). Hostnames are user-controlled and often non-ASCII, so they are
// slugged here instead of throwing "Invalid worker slot" at claim/capacity time.
export function workerSlotId(host: string, pid: number, suffix: string): string {
  const slug = host
    .replace(/[^A-Za-z0-9._-]+/g, "-")
    .replace(/^[-._]+|[-.]+$/g, "")
    .slice(0, 40) || "agent-host";
  return `${slug}-${pid}-${suffix}`.slice(0, 64);
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
    if (error instanceof RetryableBridgeError) {
      if (!bridge.reportRecovery) throw error;
      const reportSignal=AbortSignal.any([signal,AbortSignal.timeout(15000)]);
      for(let attempt=0;attempt<3;attempt++){
        try {await bridge.reportRecovery(run,error,reportSignal);return;}
        catch(reportError){
          if(signal.aborted || reportError instanceof CanvasLeaseLost || reportError instanceof CanvasRunTerminated)return;
          if(!(reportError instanceof RetryableBridgeError) || reportSignal.aborted)break;
          if(attempt<2)await delay(250*(attempt+1),undefined,{signal:reportSignal}).catch(()=>{});
        }
      }
      throw new FatalWorkerError('Pi worker 恢复上报未确认：后端暂时不可用');
    }
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
