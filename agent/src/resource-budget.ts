export interface ResourceBudgetOptions {
  maxResidentSessions: number;
  memoryBudgetMb: number;
  eventLoopLimitMs: number;
}

export interface ResourceSample { rssBytes: number; eventLoopLagMs: number }

/** Admission control only: leased sessions continue until their own terminal cleanup. */
export class ResourceBudget {
  readonly maxResidentSessions: number;
  readonly memoryBudgetBytes: number;
  readonly eventLoopLimitMs: number;
  private recoverySamples = 0;
  reason = "";
  sample: ResourceSample = { rssBytes: 0, eventLoopLagMs: 0 };

  constructor(options: ResourceBudgetOptions) {
    if (!Number.isSafeInteger(options.maxResidentSessions) || options.maxResidentSessions < 1 || options.maxResidentSessions > 64 ||
        !Number.isSafeInteger(options.memoryBudgetMb) || options.memoryBudgetMb < 128 || options.memoryBudgetMb > 65536 ||
        !Number.isSafeInteger(options.eventLoopLimitMs) || options.eventLoopLimitMs < 10 || options.eventLoopLimitMs > 5000) {
      throw new Error("Invalid Agent resource budget");
    }
    this.maxResidentSessions = options.maxResidentSessions;
    this.memoryBudgetBytes = options.memoryBudgetMb * 1024 * 1024;
    this.eventLoopLimitMs = options.eventLoopLimitMs;
  }

  get pressured(): boolean { return this.reason !== ""; }

  observe(sample: ResourceSample): boolean {
    if (!Number.isFinite(sample.rssBytes) || sample.rssBytes < 0 || !Number.isFinite(sample.eventLoopLagMs) || sample.eventLoopLagMs < 0) {
      throw new Error("Invalid Agent resource sample");
    }
    this.sample = sample;
    const previous = this.pressured;
    const highMemory = sample.rssBytes >= this.memoryBudgetBytes * 0.8;
    const highLag = sample.eventLoopLagMs >= this.eventLoopLimitMs;
    if (highMemory || highLag) {
      this.reason = highMemory ? "rss budget pressure" : "event loop pressure";
      this.recoverySamples = 0;
    } else if (this.pressured) {
      const recovered = sample.rssBytes <= this.memoryBudgetBytes * 0.65 && sample.eventLoopLagMs <= this.eventLoopLimitMs * 0.4;
      this.recoverySamples = recovered ? this.recoverySamples + 1 : 0;
      if (this.recoverySamples >= 2) { this.reason = ""; this.recoverySamples = 0; }
    }
    return previous !== this.pressured;
  }

  canClaim(active: number, reserved: number): boolean {
    return !this.pressured && active + reserved < this.maxResidentSessions;
  }
}

export function resourceBudgetFromEnv(env: NodeJS.ProcessEnv = process.env): ResourceBudget {
  const parse = (name: string, fallback: number) => {
    const raw = env[name];
    if (raw === undefined || raw === "") return fallback;
    if (!/^\d+$/.test(raw)) throw new Error(`Invalid ${name}`);
    return Number(raw);
  };
  return new ResourceBudget({
    maxResidentSessions: parse("CANVAS_AGENT_RESIDENT_LIMIT", 16),
    memoryBudgetMb: parse("CANVAS_AGENT_MEMORY_BUDGET_MB", 768),
    eventLoopLimitMs: parse("CANVAS_AGENT_EVENT_LOOP_LIMIT_MS", 250),
  });
}
