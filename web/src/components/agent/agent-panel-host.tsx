import { Suspense, useEffect, useMemo, useState } from "react";
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

export function AgentPanelHost() {
    const context = useAgentPageContext();
    const [open, setOpen] = useState(false);
    const canvasAdapter = useAgentPanelAdapter("canvas");
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
        return <PlatformAgentPanel scopeLabel={context.displayName || "平台对话"} open={open} onOpen={() => setOpen(true)} onClose={() => setOpen(false)} />;
    if (context.declaration.kind === "business" && context.declaration.businessType === "drama")
        return (
            <DramaAgentPanel
                context={context}
                scopeLabel={context.displayName || "短剧分析"}
                open={open}
                onOpen={() => setOpen(true)}
                onClose={() => setOpen(false)}
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
    useEffect(() => {
        setEpisodeId(context.target?.dramaEpisodeId || "");
    }, [context.routeKey, context.target?.dramaEpisodeId]);
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
            maxCredits: 20,
            idempotencyKey: createClientUUID(),
        });
    };
    const options = context.target?.episodeOptions || [];
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
            open={open}
            onOpen={onOpen}
            onClose={onClose}
            headerExtra={
                options.length ? (
                    <select
                        aria-label="本轮分析剧集"
                        className="max-w-28 rounded border border-black/10 bg-white px-1 py-1 text-[11px]"
                        value={episodeId || context.target?.dramaEpisodeId}
                        onChange={(event) => setEpisodeId(event.target.value)}
                    >
                        {options.map((option) => (
                            <option key={option.id} value={option.id}>
                                {option.label}
                            </option>
                        ))}
                    </select>
                ) : null
            }
        />
    );
}
