import type { AgentContextPressure } from "@/services/api/agent";

/**
 * Context Meter v2 的口径层（设计方向 §1/§2/§7）。
 *
 * 一条硬规则：**不同量纲不共用一个百分比**。
 * - 主读数（headline）＝"下一次模型调用的输入窗口占用"，分母是输入预算；
 * - 服务端治理读数（governance）＝压缩判据（token 口径优先，没配置窗口才退回字节/条数兜底），
 *   它与主读数是两个问题，界面上必须是两根独立的条；
 * - 本地估算（estimate，`local_v1`）与上游实测（provider）各自保留原值，谁也不覆盖谁。
 *
 * 另外两条界面契约由这里的结果驱动（不要在下游另算一遍）：
 * - 模型窗口未确认时 `headline.ratio` 为 undefined —— **不得显示百分比**（设计方向 §2/§9.1）；
 * - 上一次有效实测跨轮保留（`carriedOver`），新轮的本地估算不得把它顶掉（§7.1/§7.5/§9.5）。
 */
export type AgentContextReadingSource = "provider" | "estimate";

export type AgentContextHeadline = {
    tokens: number;
    /** 窗口未确认时为 undefined：此时禁止渲染百分比。 */
    ratio?: number;
    source: AgentContextReadingSource;
    /** 该实测取自第几步（沿用旧实测时是那一步）。 */
    measuredStep?: number;
    /** 本轮（当前 run）还没有实测，主读数是沿用的上一次有效实测。 */
    carriedOver: boolean;
    /** 沿用值是在另一个窗口/预算下测的，界面要说明"按当前窗口换算"。 */
    rebasedToCurrentBudget: boolean;
};

export type AgentContextGovernance = {
    ratio: number;
    thresholdRatio: number;
    basis: "tokens" | "bytes";
    source: AgentContextReadingSource | "bytes";
    compactAtTokens?: number;
    sourceBytes: number;
    thresholdBytes: number;
    historyMessages: number;
};

export type AgentContextCalibration = {
    /** 上游实测的那一次请求规模。 */
    measuredTokens: number;
    /** 同一次请求的本地估算。 */
    estimateTokens: number;
    /** 两次之间的变化量（本地估算口径）。 */
    deltaTokens?: number;
    /** 上游 ÷ 本地，用来解释"两个数为什么对不上"。 */
    scale?: number;
};

export type AgentContextMeterReading = {
    /** 模型窗口是否已确认：false 时不得给百分比。 */
    configured: boolean;
    windowTokens: number;
    budgetTokens: number;
    /** 主读数：下一次模型调用的输入窗口占用。 */
    headline: AgentContextHeadline;
    /** 下一步输入的本地估算（永远可用，作为降级口径单独展示）。 */
    estimate: { tokens: number; ratio?: number; method: "local_v1" };
    governance: AgentContextGovernance;
    calibration?: AgentContextCalibration;
};

export type AgentContextMeterState = {
    reading: AgentContextMeterReading;
    /** 最近一次**有效实测**的主读数：跨轮保留，只被新的实测替换。 */
    lastMeasured?: {
        tokens: number;
        step?: number;
        runId?: string;
        budgetTokens: number;
        governanceEpoch: number;
    };
    /** 窗口从"未确认"变为"已确认"（界面要标"模型窗口已识别"，不要画成上下文骤降）。 */
    windowResolved: boolean;
};

export type AgentContextMeterOptions = {
    runId?: string;
    /**
     * 治理代次：压缩/裁剪发生后自增。实测早于当前代次时不再沿用（§7.3），
     * 改为显示本地估算并说明"压缩后待上游重新测量"。
     */
    governanceEpoch?: number;
};

function numberOrZero(value: unknown): number {
    return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

export function agentContextMeterReading(pressure: AgentContextPressure): AgentContextMeterReading {
    const configured = Boolean(pressure.modelLimitConfigured) && numberOrZero(pressure.usableInputTokens) > 0;
    const budgetTokens = numberOrZero(pressure.usableInputTokens);
    const estimateTokens = Math.max(0, numberOrZero(pressure.estimatedInputTokens));
    const measured = pressure.tokenSource === "provider" && typeof pressure.pressureTokens === "number";
    // 有实测时主读数是"锚点实测 + 本地增量"的下一步预计输入；没有实测就退回本地估算。
    const headlineTokens = measured ? Math.max(0, numberOrZero(pressure.projectedTokens ?? pressure.pressureTokens)) : estimateTokens;
    const ratioOf = (tokens: number) => (configured ? tokens / Math.max(1, budgetTokens) : undefined);
    const byteBasis = pressure.compactionBasis === "bytes" || !configured;
    const thresholdRatio = typeof pressure.compactionThresholdRatio === "number" ? pressure.compactionThresholdRatio : 0.85;
    const estimateRatio = ratioOf(estimateTokens);
    return {
        configured,
        windowTokens: numberOrZero(pressure.contextWindowTokens),
        budgetTokens,
        headline: {
            tokens: headlineTokens,
            ratio: ratioOf(headlineTokens),
            source: measured ? "provider" : "estimate",
            measuredStep: measured ? pressure.anchorStep : undefined,
            carriedOver: false,
            rebasedToCurrentBudget: false,
        },
        estimate: { tokens: estimateTokens, ratio: estimateRatio, method: "local_v1" },
        governance: {
            ratio: Math.max(0, numberOrZero(pressure.compactionPressureRatio)),
            thresholdRatio,
            basis: byteBasis ? "bytes" : "tokens",
            source: byteBasis ? "bytes" : (pressure.compactionTokenSource ?? pressure.tokenSource ?? "estimate"),
            compactAtTokens: pressure.compactAtTokens,
            sourceBytes: Math.max(0, numberOrZero(pressure.compactionSourceBytes ?? pressure.sourceBytes)),
            thresholdBytes: Math.max(0, numberOrZero(pressure.compactionThresholdBytes)),
            historyMessages: Math.max(0, numberOrZero(pressure.historyMessages)),
        },
        calibration: measured
            ? {
                  measuredTokens: Math.max(0, numberOrZero(pressure.pressureTokens)),
                  estimateTokens,
                  deltaTokens: typeof pressure.anchorDeltaTokens === "number" ? pressure.anchorDeltaTokens : undefined,
                  scale: pressure.breakdown?.tokenScale,
              }
            : undefined,
    };
}

/**
 * 事件 → 读数状态。只做两件跨事件的事：
 * 1. 记住最近一次有效实测（§7.1：不被后续估算覆盖）；
 * 2. 标记"窗口已识别"与"实测已过期"（§7.3/§7.4/§9.1）。
 */
export function agentContextMeterState(previous: AgentContextMeterState | undefined, pressure: AgentContextPressure, options: AgentContextMeterOptions = {}): AgentContextMeterState {
    const reading = agentContextMeterReading(pressure);
    const governanceEpoch = options.governanceEpoch ?? 0;
    const windowResolved = Boolean(previous) && !previous!.reading.configured && reading.configured;

    let lastMeasured = previous?.lastMeasured;
    if (reading.headline.source === "provider") {
        lastMeasured = {
            tokens: reading.headline.tokens,
            step: reading.headline.measuredStep,
            runId: options.runId ?? lastMeasured?.runId,
            budgetTokens: reading.budgetTokens,
            governanceEpoch,
        };
    }

    // 沿用条件：这条读数没有实测、有历史实测、且历史实测没有落在更早的治理代次里。
    const carry = reading.headline.source !== "provider" && lastMeasured && lastMeasured.governanceEpoch === governanceEpoch ? lastMeasured : undefined;
    if (!carry) {
        return { reading, lastMeasured, windowResolved };
    }
    const ratio = reading.configured ? carry.tokens / Math.max(1, reading.budgetTokens) : undefined;
    return {
        reading: {
            ...reading,
            headline: {
                tokens: carry.tokens,
                ratio,
                source: "provider",
                measuredStep: carry.step,
                carriedOver: true,
                rebasedToCurrentBudget: carry.budgetTokens !== reading.budgetTokens,
            },
        },
        lastMeasured,
        windowResolved,
    };
}

/** 主读数与治理读数是否指向同一个数（界面据此决定要不要额外的"治理"标记）。 */
export function agentContextMeterNeedsGovernanceMarker(reading: AgentContextMeterReading): boolean {
    if (reading.governance.basis !== "tokens") return false;
    const headline = reading.headline.ratio;
    if (typeof headline !== "number") return reading.governance.ratio >= reading.governance.thresholdRatio;
    return reading.governance.ratio >= reading.governance.thresholdRatio && reading.governance.ratio - headline > 0.05;
}
