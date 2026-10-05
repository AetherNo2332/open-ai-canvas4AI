import { setTimeout as delay } from "node:timers/promises";
import type { PiSnapshot } from "./bridge.js";

type Step = { run: () => Promise<unknown>; resolve: (value: unknown) => void; reject: (error: unknown) => void; signal?: AbortSignal };

/** Fair, short-lived admission slots. A model/tool completion promise never enters this queue. */
export class EventScheduler {
  private queues = new Map<string, Step[]>();
  private projects: string[] = [];
  active = 0;
  peak = 0;
  constructor(public concurrency = 4) {
    if (!Number.isSafeInteger(concurrency) || concurrency < 1 || concurrency > 64) throw new Error("Invalid scheduler concurrency");
  }
  setConcurrency(value:number):void {
    if(!Number.isSafeInteger(value)||value<1||value>16)throw new Error("Invalid scheduler concurrency");
    this.concurrency=value;this.pump();
  }
  get queued():number {return [...this.queues.values()].reduce((sum,queue)=>sum+queue.length,0);}
  step<T>(project: string, run: () => Promise<T>, signal?: AbortSignal): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      if (signal?.aborted) { reject(signal.reason); return; }
      let queue = this.queues.get(project);
      if (!queue) { queue = []; this.queues.set(project, queue); this.projects.push(project); }
      queue.push({ run, resolve: resolve as (value: unknown) => void, reject, signal });
      this.pump();
    });
  }
  private pump(): void {
    while (this.active < this.concurrency && this.projects.length) {
      const project = this.projects.shift()!;
      const queue = this.queues.get(project)!;
      const item = queue.shift()!;
      if (queue.length) this.projects.push(project); else this.queues.delete(project);
      if (item.signal?.aborted) { item.reject(item.signal.reason); continue; }
      this.active += 1;
      this.peak = Math.max(this.peak, this.active);
      void Promise.resolve().then(item.run).then(item.resolve, item.reject).finally(() => { this.active -= 1; this.pump(); });
    }
  }
}

/** Notifications are hints; durable state is always read after waking. */
export class RunEvents {
  private listeners = new Map<string, Set<() => void>>();
  wake(runId: string): void {
    for (const listener of this.listeners.get(runId) ?? []) listener();
  }
  recover(): void { for (const runId of this.listeners.keys()) this.wake(runId); }
  async pause(runId: string, milliseconds: number, signal?: AbortSignal): Promise<void> {
    if (signal?.aborted) return;
    await new Promise<void>((resolve) => {
      const finish = () => { clearTimeout(timer); unsubscribe(); signal?.removeEventListener("abort", finish); resolve(); };
      const unsubscribe = this.subscribe(runId, finish);
      const timer = setTimeout(finish, milliseconds);
      signal?.addEventListener("abort", finish, { once: true });
      if (signal?.aborted) finish();
    });
  }
  subscribe(runId: string, listener: () => void): () => void {
    const listeners = this.listeners.get(runId) ?? new Set();
    listeners.add(listener); this.listeners.set(runId, listeners);
    return () => { listeners.delete(listener); if (!listeners.size) this.listeners.delete(runId); };
  }
  async wait<T>(runId: string, read: () => Promise<T>, pending: (value: T) => boolean, signal?: AbortSignal): Promise<T> {
    for (;;) {
      signal?.throwIfAborted();
      let wake!: () => void;
      const notified = new Promise<void>((resolve) => { wake = resolve; });
      const unsubscribe = this.subscribe(runId, wake);
      try {
        // Install the listener BEFORE the read, closing the completion/read race.
        const value = await read();
        if (!pending(value)) return value;
        if (signal) {
          const abort = () => wake();
          signal.addEventListener("abort", abort, { once: true });
          try { if (!signal.aborted) await notified; } finally { signal.removeEventListener("abort", abort); }
        } else await notified;
      } finally { unsubscribe(); }
    }
  }
}

interface SessionBridge { claim(signal?: AbortSignal): Promise<PiSnapshot | null>; failRun(run: PiSnapshot, reason: string, signal?: AbortSignal): Promise<void>; release?(run: PiSnapshot, signal?: AbortSignal): Promise<void> }

/** Resident sessions own leases, not execution slots. Four claimers can launch sixty-four independent sessions. */
export async function runEventSessions<B extends SessionBridge>(options: {
  concurrency: number; maxSessions: number; signal: AbortSignal;
  createBridge: (index: number) => B;
  run: (bridge: B, snapshot: PiSnapshot, signal: AbortSignal) => Promise<void>;
  onError: (runId: string, error: unknown) => void;
  events: RunEvents;
  reportCapacity?: (active: number, capacity: number) => Promise<void>;
  getConfig?:()=>{dispatchConcurrency:number;maxResidentSessions:number;maxResidentPerCanvas?:number};
  reportState?:(active:number,reserved:number,capacity:number)=>Promise<void>;
}): Promise<void> {
  if (!Number.isSafeInteger(options.maxSessions) || options.maxSessions < options.concurrency || options.maxSessions > 64) throw new Error("Invalid resident session limit");
  const active = new Map<string, Promise<void>>();
  const conversations = new Set<string>();
  const canvasResidents=new Map<string,number>();
  let reserved = 0;
  let identity = 0;
  const config:()=>{dispatchConcurrency:number;maxResidentSessions:number;maxResidentPerCanvas?:number}=()=>options.getConfig?.()??{dispatchConcurrency:options.concurrency,maxResidentSessions:options.maxSessions};
  const report=()=>Promise.all([options.reportCapacity?.(active.size,config().maxResidentSessions),options.reportState?.(active.size,reserved,config().maxResidentSessions)]).catch(error=>options.onError("capacity",error));
  const heartbeat = setInterval(() => {void report();}, 15000);
  void options.reportState?.(active.size,reserved,config().maxResidentSessions).catch(error=>options.onError("capacity",error));
  const loops = Array.from({ length: options.getConfig?16:options.concurrency }, async (_,index) => {
    while (!options.signal.aborted) {
      if (index>=config().dispatchConcurrency || active.size + reserved >= config().maxResidentSessions) { await options.events.pause("capacity",5000,options.signal); continue; }
      reserved += 1;
      const bridge = options.createBridge(identity++);
      let snapshot: PiSnapshot | null = null;
      try { snapshot = await bridge.claim(options.signal); }
      catch (error) { if (!options.signal.aborted) options.onError("claim", error); }
      finally { reserved -= 1; }
      if (!snapshot) { await options.events.pause("dispatch",5000,options.signal); continue; }
      // A configuration can shrink while a claim request is in flight.
      const canvasKey=`${snapshot.userId}:${snapshot.request.canvasId||`session:${snapshot.piSessionId||snapshot.runId}`}`;
      if(options.signal.aborted || active.size>=config().maxResidentSessions || (canvasResidents.get(canvasKey)??0)>=(config().maxResidentPerCanvas??64)) {
        await bridge.release?.(snapshot).catch(error=>options.onError(snapshot!.runId,error));
        continue;
      }
      const key = `${snapshot.userId}:${snapshot.request.canvasId ?? ""}:${snapshot.piSessionId || snapshot.runId}`;
      if (conversations.has(key)) {
        options.onError(snapshot.runId, new Error("Duplicate active session lease"));
        await bridge.release?.(snapshot, options.signal).catch((error) => options.onError(snapshot!.runId, error));
        await delay(1000, undefined, { signal: options.signal }).catch(() => {});
        continue;
      }
      conversations.add(key);
      canvasResidents.set(canvasKey,(canvasResidents.get(canvasKey)??0)+1);
      const run = snapshot;
      const task = Promise.resolve().then(() => options.run(bridge, run, options.signal)).catch((error) => {
        if (!options.signal.aborted) options.onError(run.runId, error);
      }).finally(() => {
        active.delete(run.runId); conversations.delete(key);
        const remaining=(canvasResidents.get(canvasKey)??1)-1;
        if(remaining)canvasResidents.set(canvasKey,remaining);else canvasResidents.delete(canvasKey);
        options.events.wake("capacity");
      });
      active.set(run.runId, task);
    }
  });
  await Promise.allSettled(loops);
  await Promise.allSettled(active.values());
  clearInterval(heartbeat);
}
