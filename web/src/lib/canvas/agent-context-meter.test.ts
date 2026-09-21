import assert from "node:assert/strict";
import test from "node:test";

// @ts-expect-error -- Node 原生 TypeScript 测试运行器需要保留扩展名。
import { agentContextMeterReading, agentContextMeterState, agentContextMeterNeedsGovernanceMarker } from "./agent-context-meter.ts";
// @ts-expect-error -- 同上。
import { agentContextTransitions } from "./agent-context-transitions.ts";

const WINDOW = { contextWindowTokens: 1_000_000, reservedOutputTokens: 32_768, usableInputTokens: 967_232, modelLimitConfigured: true as const };

/** 真机样本：上一轮最后一个压力事件（上游实测已采信，锚点取自第 4 步）。 */
const parentTail = {
    ...WINDOW,
    pressureRatio: 0.065804,
    promptChars: 10,
    promptLimitChars: 0,
    estimate: true as const,
    historyMessageThreshold: 16,

    estimatedInputTokens: 66_105,
    projectedTokens: 63_648,
    pressureTokens: 20_035,
    tokenSource: "provider" as const,
    compactionTokenSource: "provider" as const,
    anchorStep: 4,
    anchorDeltaTokens: 44_080,
    compactionPressureRatio: 0.065804,
    compactionBasis: "tokens" as const,
    compactionSourceBytes: 222_253,
    compactionThresholdBytes: 49_152,
    historyMessages: 37,
    sourceBytes: 222_253,
    breakdown: { tokenScale: 0.88 } as never,
};

/** 真机样本：下一轮第一个压力事件（本轮还没有锚点，只有本地估算）。 */
const childHead = {
    ...WINDOW,
    pressureRatio: 0.065804,
    promptChars: 10,
    promptLimitChars: 0,
    estimate: true as const,
    historyMessageThreshold: 16,
    estimatedInputTokens: 22_125,
    projectedTokens: 22_125,
    tokenSource: "estimate" as const,
    compactionTokenSource: "estimate" as const,
    compactionPressureRatio: 0.022875,
    compactionBasis: "tokens" as const,
    compactionSourceBytes: 73_477,
    compactionThresholdBytes: 49_152,
    historyMessages: 7,
    sourceBytes: 73_477,
};

test("主读数：有上游实测时用实测（下一步预计输入），估算单独保留", () => {
    const reading = agentContextMeterReading(parentTail);
    assert.equal(reading.headline.source, "provider");
    assert.equal(reading.headline.tokens, 63_648);
    assert.equal(Math.round(reading.headline.ratio! * 1000) / 10, 6.6);
    assert.equal(reading.estimate.tokens, 66_105);
    assert.equal(reading.estimate.method, "local_v1");
    assert.deepEqual(reading.calibration, { measuredTokens: 20_035, estimateTokens: 66_105, deltaTokens: 44_080, scale: 0.88 });
});

test("窗口未确认时不给百分比，且治理读数退回字节兜底（设计 §2/§9.1）", () => {
    const reading = agentContextMeterReading({
        contextWindowTokens: 0,
        reservedOutputTokens: 0,
        usableInputTokens: 0,
        modelLimitConfigured: false,
        pressureRatio: 0.3779,
        promptChars: 10,
        promptLimitChars: 0,
        estimate: true as const,
        historyMessageThreshold: 16,
        sourceBytes: 108_359,
        estimatedInputTokens: 21_213,
        pressureTokens: 17_832,
        projectedTokens: 19_145,
        tokenSource: "provider",
        compactionPressureRatio: 0.3779,
        compactionBasis: "bytes",
        compactionSourceBytes: 108_359,
        compactionThresholdBytes: 49_152,
        historyMessages: 23,
    });
    assert.equal(reading.configured, false);
    assert.equal(reading.headline.ratio, undefined);
    assert.equal(reading.governance.basis, "bytes");
    assert.notEqual(reading.headline.ratio, reading.governance.ratio);
});

test("窗口从未确认变为已确认：标记 windowResolved，不再画成上下文骤降（设计 §9.1）", () => {
    const before = agentContextMeterState(undefined, {
        contextWindowTokens: 0,
        reservedOutputTokens: 0,
        usableInputTokens: 0,
        modelLimitConfigured: false,
        pressureRatio: 0.9375,
        promptChars: 10,
        promptLimitChars: 0,
        estimate: true as const,
        historyMessageThreshold: 16,
        sourceBytes: 120_000,
        estimatedInputTokens: 21_213,
        compactionPressureRatio: 0.9375,
        compactionBasis: "bytes",
        compactionSourceBytes: 120_000,
        compactionThresholdBytes: 49_152,
        historyMessages: 40,
    });
    const after = agentContextMeterState(before, {
        ...WINDOW,
        pressureRatio: 0.065804,
        promptChars: 10,
        promptLimitChars: 0,
        estimate: true as const,
        historyMessageThreshold: 16,
        sourceBytes: 74_000,
        estimatedInputTokens: 22_492,
        projectedTokens: 20_093,
        pressureTokens: 20_035,
        tokenSource: "provider",
        compactionPressureRatio: 0.0207,
        compactionBasis: "tokens",
        compactionSourceBytes: 74_000,
        compactionThresholdBytes: 49_152,
        historyMessages: 12,
    });
    assert.equal(after.windowResolved, true);
    const transitions = agentContextTransitions(undefined, after);
    const resolved = transitions.filter((item) => item.kind === "window_resolved");
    assert.equal(resolved.length, 1);
    assert.match(resolved[0].label, /模型窗口已识别/);
});

test("跨轮不归零：新轮的本地估算不覆盖上一次有效实测（设计 §7.5/§9.5）", () => {
    const parentState = agentContextMeterState(undefined, parentTail, { runId: "parent" });
    assert.equal(parentState.reading.headline.source, "provider");

    const childState = agentContextMeterState(parentState, childHead, { runId: "child" });
    // 本轮第一个事件没有实测：主读数沿用上一次有效实测，不回退到 22,125。
    assert.equal(childState.reading.headline.source, "provider");
    assert.equal(childState.reading.headline.tokens, 63_648);
    assert.equal(childState.reading.headline.carriedOver, true);
    assert.equal(childState.reading.headline.measuredStep, 4);
    assert.equal(childState.reading.headline.rebasedToCurrentBudget, false);
    // 本地估算原样保留，作为降级口径单独展示。
    assert.equal(childState.reading.estimate.tokens, 22_125);
});

test("压缩让旧实测过期：代次推进后主读数退回本地估算并说明（设计 §7.3）", () => {
    const parentState = agentContextMeterState(undefined, parentTail, { runId: "parent" });
    const compacted = agentContextMeterState(parentState, childHead, { runId: "child", governanceEpoch: 1 });
    assert.equal(compacted.reading.headline.source, "estimate");
    assert.equal(compacted.reading.headline.tokens, 22_125);
    assert.equal(compacted.reading.headline.carriedOver, false);
});

test("同轮内上游校准：主读数切到实测并记录校准，估算不被丢弃（设计 §9.2）", () => {
    const estimated = agentContextMeterState(undefined, { ...childHead });
    const measured = agentContextMeterState(estimated, {
        ...childHead,
        pressureTokens: 19_467,
        projectedTokens: 46_266,
        tokenSource: "provider",
        compactionTokenSource: "provider",
        anchorStep: 2,
        anchorDeltaTokens: 24_765,
    });
    assert.equal(measured.reading.headline.source, "provider");
    assert.equal(measured.reading.headline.carriedOver, false);
    assert.equal(measured.reading.estimate.tokens, 22_125);
    assert.equal(measured.reading.calibration?.measuredTokens, 19_467);
    const notes = agentContextTransitions(undefined, measured).filter((item) => item.kind === "calibration");
    assert.equal(notes.length, 1);
    assert.match(notes[0].label, /上游校准/);
});

test("治理读数与主读数分开：阈值高于主读数时给出标记，而不是染红圆环（设计 §2/§8）", () => {
    const byteBasis = agentContextMeterReading({ ...childHead, usableInputTokens: 100_000, estimatedInputTokens: 2_000, compactionPressureRatio: 0.875, compactionBasis: "bytes" });
    // 字节兜底的治理读数不染红 token 口径的圆环。
    assert.equal(agentContextMeterNeedsGovernanceMarker(byteBasis), false);

    const tokenBasis = agentContextMeterReading({ ...childHead, usableInputTokens: 100_000, estimatedInputTokens: 2_000, compactionPressureRatio: 0.875, compactionBasis: "tokens", compactionTokenSource: "provider" });
    assert.equal(agentContextMeterNeedsGovernanceMarker(tokenBasis), true);
    assert.equal(tokenBasis.headline.ratio, 0.02);
    assert.equal(tokenBasis.governance.ratio, 0.875);
});

test("真实下降要有事件解释：压缩与图片裁剪都进最近变化（设计 §9.3/§9.4）", () => {
    const transitions = agentContextTransitions([
        { eventId: "e1", runId: "r", type: "context_compacted", seq: 12, payload: { compactedTurnCount: 23, historyMessages: 6, droppedTurns: 17, mode: "semantic" } } as never,
        { eventId: "e2", runId: "r", type: "context_images_pruned", seq: 21, payload: { prunedImages: 3, retentionRounds: 2 } } as never,
    ]);
    assert.equal(transitions.length, 2);
    assert.equal(transitions[0].kind, "semantic_compaction");
    assert.equal(transitions[0].direction, "down");
    assert.match(transitions[0].label, /23 轮/);
    assert.match(transitions[0].label, /保留 6 条/);
    assert.equal(transitions[1].kind, "image_prune");
    assert.match(transitions[1].label, /移出 3 张/);
});

test("统一的 context_transition 优先，旧事件不再重复计数（设计 §4）", () => {
    const events = [
        { eventId: "t1", runId: "r", type: "context_transition", seq: 30, payload: { kind: "semantic_compaction", reason: "threshold", before: { historyMessages: 23, sourceBytes: 108_359 }, after: { historyMessages: 6, sourceBytes: 71_364 } } } as never,
        // 同一次压缩的旧事件：有统一事件时不应再产生第二条。
        { eventId: "e1", runId: "r", type: "context_compacted", seq: 31, payload: { compactedTurnCount: 23, historyMessages: 6, droppedTurns: 17 } } as never,
        { eventId: "t2", runId: "r", type: "context_transition", seq: 40, payload: { kind: "window_resolved", reason: "window_resolved", after: { contextWindowTokens: 1_000_000, usableInputTokens: 967_232 } } } as never,
    ];
    const transitions = agentContextTransitions(events);
    assert.equal(transitions.filter((item) => item.kind === "semantic_compaction").length, 1);
    assert.equal(transitions.filter((item) => item.kind === "window_resolved").length, 1);
    assert.match(transitions.find((item) => item.kind === "semantic_compaction")!.label, /23 条 → 6 条/);
});
