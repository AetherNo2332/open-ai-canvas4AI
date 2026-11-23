import { Suspense, useCallback, useEffect, useMemo, useState } from "react";
import { InputNumber } from "antd";
import { useAgentPageContext } from "@/services/agent/business-context";
import { useAgentPanelAdapter } from "@/services/agent/panel-adapters";
import { getActiveUserScope } from "@/lib/user-scope";
import { createAgentClient } from "@/sdk/agent/client";
import { agentWebTransport } from "@/sdk/agent/http-transport";
import { useEffectiveConfig, logicalModelIDForConfig, modelOptionName, resolveModelRequestConfig, selectableModelsByCapability } from "@/stores/use-config-store";
import { dramaAgentAnalysisRequest } from "@/services/api/drama-agent";
import { AgentConversationPanel, type BuildAgentRequest } from "./agent-conversation-panel";
import { createClientUUID } from "@/lib/client-id";
import { useAgentCapabilities } from "./use-agent-capabilities";
import { closeAgentPanelSurface, openAgentPanelSurface, useAgentPanelSurfaceOpen } from "@/stores/use-agent-panel-surface";
import type { AgentConversationRecord } from "@/services/agent/conversation-types";

export function AgentPanelHost() {
    const context = useAgentPageContext();
    const open = useAgentPanelSurfaceOpen();
    const canvasAdapter = useAgentPanelAdapter("canvas");
    useEffect(() => {
        if (!context.declaration.enabled) return;
        const openFromShortcut = (event: KeyboardEvent) => {
            if (!(event.metaKey || event.ctrlKey) || event.altKey || event.key.toLowerCase() !== "j") return;
            event.preventDefault();
            openAgentPanelSurface();
        };
        window.addEventListener("keydown", openFromShortcut);
        return () => window.removeEventListener("keydown", openFromShortcut);
    }, [context.declaration.enabled]);
    if (!context.declaration.enabled) return null;
    if (context.declaration.kind === "business" && context.declaration.businessType === "canvas") {
        if (context.status === "loading") return <AgentEntryStatus label="正在加载画布 Agent" />;
        if (context.status === "error" || !context.scope) return <AgentEntryStatus label={context.error || "当前画布 Agent 暂不可用"} disabled />;
        if (!canvasAdapter) return <AgentEntryStatus label="正在加载画布 Agent 面板" />;
        const CanvasPanel = canvasAdapter;
        return (
            <Suspense fallback={null}>
                <CanvasPanel />
            </Suspense>
        );
    }
    if (context.status === "loading") return <AgentEntryStatus label="正在加载 Agent 接入" />;
    if (context.status === "error" || !context.scope) return <AgentEntryStatus label={context.error || "当前页面 Agent 暂不可用"} disabled />;
    if (context.declaration.kind === "platform")
        return <PlatformAgentPanel scopeLabel={context.displayName || "平台对话"} open={open} onOpen={openAgentPanelSurface} onClose={closeAgentPanelSurface} />;
    if (context.declaration.kind === "business" && context.declaration.businessType === "drama")
        return (
            <DramaAgentPanel
                context={context}
                scopeLabel={context.displayName || "短剧分析"}
                open={open}
                onOpen={openAgentPanelSurface}
                onClose={closeAgentPanelSurface}
            />
        );
    return <AgentEntryStatus label={`当前业务未注册 Agent 接入：${context.declaration.kind}`} disabled />;
}

function AgentEntryStatus({ label, disabled = false }: { label: string; disabled?: boolean }) {
    return (
        <button type="button" disabled={disabled} className="fixed bottom-5 right-5 z-[70] rounded-full border border-black/10 bg-white/95 px-4 py-3 text-sm text-neutral-600 shadow-xl disabled:opacity-70">
            {label}
        </button>
    );
}

function PlatformAgentPanel({ scopeLabel, open, onOpen, onClose }: { scopeLabel: string; open: boolean; onOpen: () => void; onClose: () => void }) {
    const userScope = getActiveUserScope();
    const config = useEffectiveConfig();
    const client = useMemo(
        () => createAgentClient({ transport: agentWebTransport, scope: { type: "platform", id: "self" }, agentDefinitionId: "platform.default" }),
        [],
    );
    const capability = useAgentCapabilities({
        active: true,
        client,
        scope: { type: "platform", id: "self" },
        agentDefinitionId: "platform.default",
    });
    if (capability.status === "loading" || capability.status === "idle") return <AgentEntryStatus label="正在校验平台 Agent 能力" />;
    if (capability.status === "error") return <AgentEntryStatus label={capability.error || "平台 Agent 暂不可用"} disabled />;
    const buildRequest: BuildAgentRequest = ({ prompt }) => {
        const textModels = selectableModelsByCapability(config, "text");
        const model = textModels.includes(config.textModel || config.model || "") ? config.textModel || config.model : textModels[0] || "";
        const requestConfig = model ? resolveModelRequestConfig({ ...config, model }, model) : { channelId: "", channelModelKey: "" };
        const logicalModelId = model ? logicalModelIDForConfig({ ...config, model }) : undefined;
        return {
            scope: { type: "platform", id: "self" },
            canvasId: "",
            prompt,
            permissionMode: "read_only",
            contextScope: [],
            budget: { maxCredits: 20 },
            idempotencyKey: createClientUUID(),
            ...(model ? { model: modelOptionName(model) || undefined } : {}),
            ...(logicalModelId ? { logicalModelId } : requestConfig.channelId ? { channelId: requestConfig.channelId, channelModelKey: modelOptionName(model) || undefined } : {}),
        };
    };
    return (
        <AgentConversationPanel
            context={{ owner: "platform", routeKey: "platform", declaration: { enabled: true, kind: "platform" }, status: "ready", scope: { type: "platform", id: "self" }, agentDefinitionId: "platform.default", displayName: scopeLabel }}
            client={client}
            buildRequest={buildRequest}
            scopeLabel={scopeLabel}
            userScope={userScope}
            capabilities={capability.capabilities}
            open={open}
            onOpen={onOpen}
            onClose={onClose}
        />
    );
}

function DramaAgentPanel({
    context,
    scopeLabel,
    open,
    onOpen,
    onClose,
}: {
    context: NonNullable<ReturnType<typeof useAgentPageContext>>;
    scopeLabel: string;
    open: boolean;
    onOpen: () => void;
    onClose: () => void;
}) {
    const userScope = getActiveUserScope();
    const scope = context.scope!;
    const client = useMemo(() => createAgentClient({ transport: agentWebTransport, scope, agentDefinitionId: context.agentDefinitionId || "drama.analysis" }), [scope.type, scope.id, context.agentDefinitionId]);
    const [episodeId, setEpisodeId] = useState(context.target?.dramaEpisodeId || "");
    const [credits, setCredits] = useState<number | null>(20);
    const routeEpisodeId = context.target?.dramaEpisodeId || "";
    useEffect(() => {
        setEpisodeId(routeEpisodeId);
    }, [context.routeKey, routeEpisodeId]);
    const capability = useAgentCapabilities({
        active: context.status === "ready",
        client,
        scope,
        agentDefinitionId: context.agentDefinitionId || "drama.analysis",
    });
    const buildRequest: BuildAgentRequest = ({ prompt }) => {
        const targetEpisode = episodeId || context.target?.dramaEpisodeId || "";
        const model = context.target?.model;
        if (!targetEpisode || !model) throw new Error("请选择本轮分析剧集");
        return dramaAgentAnalysisRequest({
            projectId: scope.id,
            episodeId: targetEpisode,
            model: { logicalModelId: model.logicalModelId, channelId: model.channelId, modelKey: model.modelKey, label: model.label },
            prompt,
            maxCredits: credits ?? 0,
            idempotencyKey: createClientUUID(),
        });
    };
    const options = context.target?.episodeOptions || [];
    const syncActiveEpisode = useCallback(
        ({ run }: { run: AgentConversationRecord["run"] }) => {
            setEpisodeId(run?.dramaEpisodeId || routeEpisodeId);
        },
        [routeEpisodeId],
    );
    const selectedLabel = options.find((option) => option.id === episodeId)?.label || context.target?.displayLabel || "本轮剧集";
    if (capability.status === "loading" || capability.status === "idle") return <AgentEntryStatus label="正在校验短剧 Agent 能力" />;
    if (capability.status === "error") return <AgentEntryStatus label={capability.error || "短剧 Agent 暂不可用"} disabled />;
    return (
        <AgentConversationPanel
            context={context}
            client={client}
            buildRequest={buildRequest}
            scopeLabel={scopeLabel}
            userScope={userScope}
            capabilities={capability.capabilities}
            initialRunId={context.target?.openedRunId}
            onActiveContextChange={syncActiveEpisode}
            open={open}
            onOpen={onOpen}
            onClose={onClose}
            headerExtra={() => (
                <div className="flex items-center gap-1">
                    <select
                        aria-label="本轮分析剧集"
                        title={selectedLabel}
                        className="max-w-32 rounded border border-black/10 bg-white px-1 py-1 text-[11px]"
                        value={episodeId}
                        onChange={(event) => setEpisodeId(event.target.value)}
                    >
                        {options.map((option) => (
                            <option key={option.id} value={option.id}>
                                {option.label}
                            </option>
                        ))}
                    </select>
                    <InputNumber aria-label="分析积分上限" size="small" min={0.01} max={1_000_000} value={credits} onChange={setCredits} className="w-20" />
                </div>
            )}
        />
    );
}
