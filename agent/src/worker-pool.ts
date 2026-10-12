import { setTimeout as delay } from "node:timers/promises";
import type { PiSnapshot } from "./bridge.js";
import { runLeasedPiSession } from "./worker-runtime.js";

export interface PiWorkerBridge {
  readonly workerId: string;
  claim(signal?: AbortSignal): Promise<PiSnapshot | null>;
  failRun(run: PiSnapshot, reason: string, signal?: AbortSignal): Promise<void>;
}

export interface PiWorkerPoolOptions {
  concurrency: number;
  signal: AbortSignal;
  createBridge(index: number): PiWorkerBridge;
  run(bridge: PiWorkerBridge, run: PiSnapshot, signal: AbortSignal): Promise<void>;
  onError?(workerId: string, error: unknown): void;
  sleep?(milliseconds: number, signal: AbortSignal): Promise<void>;
  idleDelayMs?: number;
  errorDelayMs?: number;
}

const MAX_PI_WORKERS = 16;

export function parsePiWorkerConcurrency(value: string | undefined): number {
  if (value === undefined || value.trim() === "") return 4;
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < 1 || parsed > MAX_PI_WORKERS) {
    throw new Error(`CANVAS_AGENT_CONCURRENCY must be an integer from 1 to ${MAX_PI_WORKERS}`);
  }
  return parsed;
}

/**
 * Run a bounded set of independent claim loops. Each loop owns one worker identity
 * and one run at a time, while Go remains authoritative for session lease fencing.
 */
export async function runPiWorkerPool(options: PiWorkerPoolOptions): Promise<void> {
  const { concurrency, signal } = options;
  if (!Number.isSafeInteger(concurrency) || concurrency < 1 || concurrency > MAX_PI_WORKERS) {
    throw new Error(`Pi worker concurrency must be an integer from 1 to ${MAX_PI_WORKERS}`);
  }

  const sleep = options.sleep ?? (async (milliseconds: number, abortSignal: AbortSignal) => {
    await delay(milliseconds, undefined, { signal: abortSignal });
  });
  const workers = Array.from({ length: concurrency }, (_, index) => options.createBridge(index));

  await Promise.all(workers.map(async (bridge) => {
    while (!signal.aborted) {
      try {
        const run = await bridge.claim(signal);
        if (run) {
          await runLeasedPiSession(bridge, run, signal, options.run);
          continue;
        }
        await sleep(options.idleDelayMs ?? 1000, signal);
      } catch (error) {
        if (signal.aborted) return;
        options.onError?.(bridge.workerId, error);
        try {
          await sleep(options.errorDelayMs ?? 3000, signal);
        } catch {
          if (signal.aborted) return;
          throw error;
        }
      }
    }
  }));
}
