import assert from "node:assert/strict";
import test from "node:test";

// @ts-expect-error -- Node 原生 TypeScript 测试运行器需要保留扩展名。
import { agentContextMeterReading } from "./agent-context-meter.ts";

const base = {
    contextWindowTokens: 128_000,
    reservedOutputTokens: 16_000,
    usableInputTokens: 100_000,
    pressureRatio: 0.2,
    sourceBytes: 40_000,
    promptChars: 10,
    promptLimitChars: 0,
    modelLimitConfigured: true,
    estimate: true as const,
    compactionSourceBytes: 20_000,
    compactionThresholdBytes: 48 * 1024,
    historyMessages: 8,
    historyMessageThreshold: 16,
    compactionPressureRatio: 0.2,
};

test("headline meter stays comparable when provider anchor replaces estimate", () => {
    const estimated = agentContextMeterReading({ ...base, estimatedInputTokens: 20_000, projectedTokens: 20_000, tokenSource: "estimate", compactionTokenSource: "estimate" });
    const anchored = agentContextMeterReading({ ...base, estimatedInputTokens: 20_000, projectedTokens: 11_000, pressureTokens: 10_500, tokenSource: "provider", compactionTokenSource: "provider", compactionPressureRatio: 0.11 });

    assert.equal(estimated.displayTokens, 20_000);
    assert.equal(anchored.displayTokens, 20_000);
    assert.equal(estimated.displayRatio, anchored.displayRatio);
    assert.equal(anchored.decisionTokens, 11_000);
    assert.equal(anchored.decisionSource, "provider");
});

test("unconfigured models keep the byte fallback as the headline", () => {
    const reading = agentContextMeterReading({ ...base, estimatedInputTokens: 20_000, usableInputTokens: 0, modelLimitConfigured: false, compactionBasis: "bytes", compactionPressureRatio: 0.75 });
    assert.equal(reading.configured, false);
    assert.equal(reading.displayRatio, 0.75);
    assert.equal(reading.decisionSource, "bytes");
});
