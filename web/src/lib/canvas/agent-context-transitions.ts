import type { AgentEvent } from "@/services/api/agent";
import type { AgentContextMeterReading, AgentContextMeterState } from "@/lib/canvas/agent-context-meter";

/**
 * "最近变化"（设计方向 §3/§9.1/§9.3/§9.4）：每一次下降都要能解释成
 * 语义压缩、图片裁剪、上游校准或窗口识别，而不是让用户以为上下文凭空丢了。
 *
 * 只读事件，不猜数字：载荷里没有的字段就不写。
 */
export type AgentContextTransitionKind = "semantic_compaction" | "body_eviction" | "image_prune" | "window_resolved" | "calibration";

export type AgentContextTransition = {
    kind: AgentContextTransitionKind;
    /** 给用户看的一句话（含关键数字）。 */
    label: string;
    /** 方向：down=减少了内容，up=补回了内容，note=口径/窗口变化。 */
    direction: "down" | "up" | "note";
    /** 事件序号（用于稳定 key 与排序）。 */
    seq?: number;
};

const maxTransitions = 4;

function integerField(payload: Record<string, unknown>, key: string): number | undefined {
    const value = payload[key];
    return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function stringField(payload: Record<string, unknown>, key: string): string {
    const value = payload[key];
    return typeof value === "string" ? value : "";
}

/**
 * 从 run 事件（时间正序）抽出治理与口径变化。窗口识别与上游校准来自口径层的状态：
 * 它们是"读数换了一把尺子"，不是上下文本身变了（§3 要求这种切换必须断开曲线并标注）。
 */
const unifiedKinds: Record<string, AgentContextTransitionKind> = {
    semantic_compaction: "semantic_compaction",
    body_eviction: "body_eviction",
    image_prune: "image_prune",
    window_resolved: "window_resolved",
    model_changed: "calibration",
    route_changed: "calibration",
};

/** 统一的 context_transition（设计 §4）优先：有它就不再按旧事件各解析一套，避免同一次治理算两遍。 */
function transitionFromUnifiedEvent(event: AgentEvent): AgentContextTransition | undefined {
    const payload = (event.payload ?? {}) as Record<string, unknown>;
    const kind = unifiedKinds[stringField(payload, "kind")];
    if (!kind) return undefined;
    const text = stringField(payload, "text");
    const before = (payload.before ?? {}) as Record<string, unknown>;
    const after = (payload.after ?? {}) as Record<string, unknown>;
    const label =
        text ||
        (() => {
            switch (kind) {
                case "semantic_compaction":
                    return `语义压缩：${integerField(before, "historyMessages") ?? "?"} 条 → ${integerField(after, "historyMessages") ?? "?"} 条`;
                case "image_prune":
                    return `图片裁剪：移出 ${integerField(payload, "prunedImages") ?? 0} 张看图结果`;
                case "body_eviction": {
                    const bytes = (integerField(before, "sourceBytes") ?? 0) - (integerField(after, "sourceBytes") ?? 0);
                    return `正文卸载${bytes > 0 ? ` -${bytes.toLocaleString("zh-CN")} bytes` : ""}`;
                }
                case "window_resolved":
                    return `模型窗口已识别：${(integerField(after, "contextWindowTokens") ?? 0).toLocaleString("zh-CN")} Token（读数改用窗口口径，不是上下文变少）`;
                default:
                    return stringField(payload, "reason") || "口径变化";
            }
        })();
    const direction = kind === "window_resolved" || kind === "calibration" ? "note" : "down";
    return { kind, label, direction, seq: event.seq };
}

export function agentContextTransitions(events: AgentEvent[] | undefined, state?: AgentContextMeterState): AgentContextTransition[] {
    const list = events ?? [];
    const unified = list
        .filter((event) => event.type === "context_transition")
        .map(transitionFromUnifiedEvent)
        .filter((item): item is AgentContextTransition => Boolean(item));
    if (unified.length > 0) {
        const transitions = [...unified];
        const reading = state?.reading;
        if (reading?.calibration && reading.headline.source === "provider" && !reading.headline.carriedOver) {
            const { measuredTokens, estimateTokens, scale } = reading.calibration;
            transitions.push({
                kind: "calibration",
                label: `上游校准：实测 ${measuredTokens.toLocaleString("zh-CN")} / 本地估算 ${estimateTokens.toLocaleString("zh-CN")} Token${scale && Math.abs(scale - 1) > 0.005 ? `（×${scale.toFixed(2)}）` : ""}`,
                direction: "note",
            });
        }
        return transitions.slice(-maxTransitions);
    }
    const transitions: AgentContextTransition[] = [];
    for (const event of list) {
        const payload = (event.payload ?? {}) as Record<string, unknown>;
        switch (event.type) {
            case "context_compacted": {
                const turns = integerField(payload, "compactedTurnCount") ?? 0;
                const kept = integerField(payload, "historyMessages") ?? 0;
                const dropped = integerField(payload, "droppedTurns");
                const mode = stringField(payload, "mode");
                const parts = [`语义压缩：${turns > 0 ? `${turns} 轮` : "历史"} → 保留 ${kept} 条`];
                if (typeof dropped === "number" && dropped > 0) parts.push(`少了 ${dropped} 轮`);
                if (mode === "fallback") parts.push("服务端保底检查点");
                transitions.push({ kind: "semantic_compaction", label: parts.join(" · "), direction: "down", seq: event.seq });
                break;
            }
            case "context_images_pruned": {
                const images = integerField(payload, "prunedImages") ?? 0;
                const rounds = integerField(payload, "retentionRounds");
                transitions.push({
                    kind: "image_prune",
                    label: `图片裁剪：移出 ${images} 张看图结果${typeof rounds === "number" && rounds > 0 ? `（保留最近 ${rounds} 轮）` : ""}`,
                    direction: "down",
                    seq: event.seq,
                });
                break;
            }
            case "context_evicted": {
                const bytes = integerField(payload, "evictedBytes") ?? integerField(payload, "sourceBytes");
                transitions.push({
                    kind: "body_eviction",
                    label: `正文卸载${typeof bytes === "number" && bytes > 0 ? ` -${bytes.toLocaleString("zh-CN")} bytes` : ""}`,
                    direction: "down",
                    seq: event.seq,
                });
                break;
            }
            default:
                break;
        }
    }
    const reading: AgentContextMeterReading | undefined = state?.reading;
    if (state?.windowResolved && reading) {
        transitions.push({
            kind: "window_resolved",
            label: `模型窗口已识别：${reading.windowTokens.toLocaleString("zh-CN")} Token（读数改用窗口口径，不是上下文变少）`,
            direction: "note",
        });
    }
    if (reading?.calibration && reading.headline.source === "provider" && !reading.headline.carriedOver) {
        const { measuredTokens, estimateTokens, scale } = reading.calibration;
        transitions.push({
            kind: "calibration",
            label: `上游校准：实测 ${measuredTokens.toLocaleString("zh-CN")} / 本地估算 ${estimateTokens.toLocaleString("zh-CN")} Token${scale && Math.abs(scale - 1) > 0.005 ? `（×${scale.toFixed(2)}）` : ""}`,
            direction: "note",
        });
    }
    return transitions.slice(-maxTransitions);
}
