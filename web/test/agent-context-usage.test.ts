import { describe, expect, it } from "bun:test";
import { continueAgentContextUsage, contextInputTokens, contextPressureRatio, emptyAgentContextUsage, presentAgentContextUsage, reduceAgentContextUsage, sameAgentContextSelection } from "@/lib/canvas/agent-context-usage";

const event = (runId: string, type: string, payload: Record<string, unknown>, seq = 1) => ({ runId, type, payload, seq });

describe("Agent context usage events", () => {
    it("preserves readings only for the same model selection and clears actual route transitions", () => {
        const selected = { model: "logical", logicalModelId: "l" };
        expect(sameAgentContextSelection(selected, { ...selected })).toBe(true);
        expect(sameAgentContextSelection({ model: "auto" }, { model: "auto" })).toBe(true);
        expect(sameAgentContextSelection({}, {})).toBe(false);
        expect(sameAgentContextSelection(selected, { ...selected, logicalModelId: "other" })).toBe(false);
        expect(sameAgentContextSelection({ model: "m", channelId: "a" }, { model: "m", channelId: "b" })).toBe(false);
        expect(sameAgentContextSelection({ model: "m" }, { model: "m", channelId: "a" })).toBe(false);
        const state = reduceAgentContextUsage(emptyAgentContextUsage("run"), event("run", "context_pressure", {
            tokenSource: "provider", normalizedInputTokens: 80000,
        }));
        for (const kind of ["model_changed", "route_changed"]) {
            expect(reduceAgentContextUsage(state, event("run", "context_transition", { kind })).reading).toBeNull();
        }
    });
    it("uses the same provider input for pressure, remaining budget and cache hit rate", () => {
        const state = reduceAgentContextUsage(emptyAgentContextUsage("run-1"), event("run-1", "context_pressure", {
            modelLimitConfigured: true, usableInputTokens: 80_000, projectedTokens: 40_000, tokenSource: "provider",
            providerUsage: { inputTokens: 10_000, cacheReadTokens: 2_500 },
        }));
        expect(presentAgentContextUsage(state)).toMatchObject({ inputTokens: 10_000, remainingTokens: 70_000, ratio: 0.125, cacheHitRate: 0.25, estimate: false });
        const continued = continueAgentContextUsage(state, "run-2");
        expect(presentAgentContextUsage(continued).cacheHitRate).toBe(0.25);
    });

    it("distinguishes zero cache hits from missing or invalid provider usage", () => {
        const view = (providerUsage?: unknown) => presentAgentContextUsage({
            ...emptyAgentContextUsage("run-1"), reading: { providerUsage },
        });
        expect(view({ inputTokens: 10_000, cacheReadTokens: 0 }).cacheHitRate).toBe(0);
        expect(view({ inputTokens: 10_000, cacheReadTokens: 10_000 }).cacheHitRate).toBe(1);
        for (const usage of [undefined, null, {}, { inputTokens: 10_000 }, { inputTokens: 0, cacheReadTokens: 0 },
            { inputTokens: 10, cacheReadTokens: 11 }, { inputTokens: 10, cacheReadTokens: -1 },
            { inputTokens: Infinity, cacheReadTokens: 1 }, { inputTokens: 10, cacheReadTokens: "5" }]) {
            expect(view(usage).cacheHitRate).toBeUndefined();
        }
    });

    it("retains a session measurement across runs and replaces it with the next reading", () => {
        const previous = reduceAgentContextUsage(emptyAgentContextUsage("run-1"), event("run-1", "context_pressure", { estimatedInputTokens: 12000 }));
        const continued = continueAgentContextUsage(previous, "run-2");
        expect(continued.reading).toEqual(previous.reading);
        expect(continued.readingSeq).toBe(0);
        expect(reduceAgentContextUsage(continued, event("run-2", "context_pressure", { estimatedInputTokens: 15000 })).reading).toEqual({ estimatedInputTokens: 15000 });
        expect(emptyAgentContextUsage("").reading).toBeNull();
    });
    it("does not add a local increment to valid provider usage", () => {
        const state = reduceAgentContextUsage(
            emptyAgentContextUsage("run-1"),
            event("run-1", "context_pressure", {
                modelLimitConfigured: true,
                usableInputTokens: 80_000,
                pressureTokens: 40_000,
                projectedNextInputTokens: 48_000,
                projectedPressureRatio: 0.6,
                tokenSource: "provider",
            }),
        );
        expect(contextInputTokens(state.reading)).toBe(40_000);
        expect(contextPressureRatio(state.reading)).toBe(0.5);
    });

    it("retains provider pressure across continuation and refreshes from the completed final step", () => {
        let state = reduceAgentContextUsage(emptyAgentContextUsage("run-1"), event("run-1", "context_pressure", {
            modelLimitConfigured: true, inputBudgetTokens: 100_000, compactAtTokens: 85_000,
            tokenSource: "provider", normalizedInputTokens: 80_000,
            providerUsage: { inputTokens: 80_000, cacheReadTokens: 40_000 },
        }));
        state = continueAgentContextUsage(state, "run-2");
        state = reduceAgentContextUsage(state, event("run-2", "context_pressure", {
            modelLimitConfigured: true, inputBudgetTokens: 100_000, compactAtTokens: 85_000,
            tokenSource: "provider", projectedTokens: 80_000, estimatedInputTokens: 99_000,
            providerUsage: { inputTokens: 80_000, cacheReadTokens: 40_000 },
        }));
        expect(presentAgentContextUsage(state)).toMatchObject({ inputTokens: 80_000, ratio: 0.8, phase: "watch", estimate: false });
        state = reduceAgentContextUsage(state, event("run-2", "context_pressure", {
            phase: "after_request", modelLimitConfigured: true, inputBudgetTokens: 100_000, compactAtTokens: 85_000,
            tokenSource: "provider", providerUsage: { inputTokens: 85_000, cacheReadTokens: 80_000 },
        }, 2));
        state = reduceAgentContextUsage(state, event("run-2", "run_status", { status: "completed" }, 3));
        expect(presentAgentContextUsage(state)).toMatchObject({ inputTokens: 85_000, ratio: 0.85, phase: "watch", remainingTokens: 15_000 });
        expect(presentAgentContextUsage(state).cacheHitRate).toBeCloseTo(80_000 / 85_000, 5);
    });

    it("does not invent percentages when the model window is unknown", () => {
        const state = reduceAgentContextUsage(
            emptyAgentContextUsage("run-1"),
            event("run-1", "context_pressure", {
                estimatedInputTokens: 12_000,
                modelLimitConfigured: false,
            }),
        );
        expect(contextInputTokens(state.reading)).toBe(12_000);
        expect(contextPressureRatio(state.reading)).toBeUndefined();
    });

    it("marks pre-compaction readings stale until a fresh pressure event arrives", () => {
        let state = reduceAgentContextUsage(emptyAgentContextUsage("run-1"), event("run-1", "context_pressure", { pressureRatio: 0.8, modelLimitConfigured: true, usableInputTokens: 100 }, 2));
        state = reduceAgentContextUsage(state, event("run-1", "context_compaction_requested", { basis: "tokens" }, 3));
        expect(state.compactionPending).toEqual({ basis: "tokens" });
        state = reduceAgentContextUsage(state, event("run-1", "context_compacted", { mode: "checkpoint", resume: true }, 4));
        expect(state.readingStale).toBe(true);
        expect(state.compactionPending).toBeNull();
        expect(state.lastCompaction).toEqual({ mode: "checkpoint", resume: true });
        state = reduceAgentContextUsage(state, event("run-1", "context_pressure", { pressureRatio: 0.25, modelLimitConfigured: true, usableInputTokens: 100 }, 5));
        expect(state.readingStale).toBe(false);
        expect(contextPressureRatio(state.reading)).toBe(0.25);
    });

    it("fills the ring against the displayed input budget", () => {
        const state = reduceAgentContextUsage(
            emptyAgentContextUsage("run-1"),
            event("run-1", "context_pressure", {
                modelLimitConfigured: true,
                tokenSource: "provider",
                usableInputTokens: 100_000,
                compactAtTokens: 85_000,
                contextWindowTokens: 128_000,
                normalizedInputTokens: 42_500,
                projectedNextInputTokens: 42_500,
                projectedPressureRatio: 0.425,
                breakdown: {
                    buckets: [
                        { key: "system", label: "系统提示（含画布摘要）", tokens: 8_000, scaledTokens: 9_000, bytes: 12_000 },
                        { key: "tools", tokens: 4_000, scaledTokens: 4_500, bytes: 18_000 },
                        { key: "messages", tokens: 20_000, scaledTokens: 22_000, bytes: 228_000 },
                    ],
                    envelopeBytes: 558,
                },
            }),
        );
        const view = presentAgentContextUsage(state);
        expect(view.phase).toBe("ok");
        expect(view.ring).toBeCloseTo(0.425, 5);
        expect(view.label).toBe("43%");
        expect(view.inputTokens).toBe(42_500);
        expect(view.remainingTokens).toBe(57_500);
        expect(view.protocolBytes).toBe(558);
        expect(view.breakdown.map((item) => item.label)).toEqual(["系统提示（含画布摘要）", "工具 schema", "会话消息（含工具结果）"]);
        expect(view.breakdown[0]?.tokens).toBe(9_000);
        expect(view.breakdown[0]?.bytes).toBe(12_000);
    });

    it("does not infer Pi compaction from the displayed input pressure", () => {
        let state = reduceAgentContextUsage(
            emptyAgentContextUsage("run-1"),
            event("run-1", "context_pressure", {
                modelLimitConfigured: true,
                usableInputTokens: 100,
                compactAtTokens: 85,
                estimatedInputTokens: 90,
                pressureRatio: 0.9,
            }),
        );
        expect(presentAgentContextUsage(state).phase).toBe("watch");
        expect(presentAgentContextUsage(state).ring).toBe(0.9);
        state = reduceAgentContextUsage(state, event("run-1", "context_compaction_requested", { basis: "tokens" }, 2));
        expect(presentAgentContextUsage(state).phase).toBe("compacting");
        state = reduceAgentContextUsage(state, event("run-1", "context_compacted", { mode: "fallback", resume: true }, 3));
        expect(presentAgentContextUsage(state).phase).toBe("stale");
    });

    it("does not draw a ring when the window is unknown or unread", () => {
        expect(presentAgentContextUsage(emptyAgentContextUsage("")).phase).toBe("idle");
        const state = reduceAgentContextUsage(
            emptyAgentContextUsage("run-1"),
            event("run-1", "context_pressure", {
                estimatedInputTokens: 12_000,
                modelLimitConfigured: false,
            }),
        );
        const view = presentAgentContextUsage(state);
        expect(view.phase).toBe("unknown");
        expect(view.ratio).toBeUndefined();
        expect(view.ring).toBe(0);
    });

    it("isolates usage across runs", () => {
        const state = reduceAgentContextUsage(
            {
                ...emptyAgentContextUsage("run-1"),
                reading: { estimatedInputTokens: 300 },
                lastCompaction: { mode: "checkpoint" },
            },
            event("run-2", "assistant_message", {}),
        );
        expect(state.runId).toBe("run-2");
        expect(state.reading).toBeNull();
        expect(state.lastCompaction).toBeNull();
    });
});
