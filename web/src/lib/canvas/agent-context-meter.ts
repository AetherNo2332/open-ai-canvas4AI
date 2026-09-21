import type { AgentContextPressure } from "@/services/api/agent";

export type AgentContextMeterReading = {
    configured: boolean;
    displayTokens: number;
    displayRatio: number;
    decisionTokens: number;
    decisionRatio: number;
    decisionSource: "provider" | "estimate" | "bytes";
};

/**
 * Keep the headline meter on one comparable scale. Provider usage describes the
 * previous request while the local estimate describes the next request; mixing
 * those two samples in one line creates a false drop when the anchor arrives.
 */
export function agentContextMeterReading(pressure: AgentContextPressure): AgentContextMeterReading {
    const configured = pressure.modelLimitConfigured && pressure.usableInputTokens > 0;
    const displayTokens = Math.max(0, pressure.estimatedInputTokens || 0);
    const displayRatio = configured ? displayTokens / Math.max(1, pressure.usableInputTokens) : Math.max(0, pressure.compactionPressureRatio || 0);
    const decisionTokens = Math.max(0, pressure.projectedTokens ?? displayTokens);
    const tokenDecision = pressure.compactionBasis !== "bytes" && configured;
    return {
        configured,
        displayTokens,
        displayRatio,
        decisionTokens,
        decisionRatio: tokenDecision ? Math.max(0, pressure.compactionPressureRatio || decisionTokens / Math.max(1, pressure.usableInputTokens)) : Math.max(0, pressure.compactionPressureRatio || 0),
        decisionSource: tokenDecision ? (pressure.compactionTokenSource ?? pressure.tokenSource ?? "estimate") : "bytes",
    };
}
