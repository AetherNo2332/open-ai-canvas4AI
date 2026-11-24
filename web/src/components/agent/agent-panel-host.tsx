import { useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { AgentBusinessScope, AgentPendingDirectorPreview, AgentTransport } from "@/sdk/agent";
import { createAgentClient } from "@/sdk/agent/client";
import { agentWebTransport } from "@/sdk/agent/http-transport";
import { useAgentPageContext, type AgentPageContext } from "@/services/agent/business-context";
import { useAgentPanelAdapter } from "@/services/agent/panel-adapters";
import { resolveAgentPanelConfig } from "@/services/agent/panel-config";
import { platformAgentPanelAdapter } from "@/services/agent/adapters/platform";
import { dramaAgentPanelAdapter } from "@/services/agent/adapters/drama";
import { closeAgentPanelSurface, openAgentPanelSurface, useAgentPanelSurfaceOpen } from "@/stores/use-agent-panel-surface";
import { useUserStore } from "@/stores/use-user-store";
import { getActiveUserScope } from "@/lib/user-scope";
import { AgentPanel } from "./agent-panel";
import { useAgentCapabilities } from "./use-agent-capabilities";
import "@/services/agent/configs";

function subscribeAccountChanges(listener: () => void) {
    const unsubscribe = useUserStore.subscribe(listener);
    window.addEventListener("storage", listener);
    return () => {
        unsubscribe();
        window.removeEventListener("storage", listener);
    };
}

function accountIsCurrent(userId: string) {
    const session = useUserStore.getState();
    return session.hydrated && session.user?.id === userId && getActiveUserScope() === userId;
}

export function AgentPanelHost({ userId }: { userId?: string } = {}) {
    const context = useAgentPageContext();
    const open = useAgentPanelSurfaceOpen();
    const accountChanged = useSyncExternalStore(
        subscribeAccountChanges,
        () => Boolean(userId && !accountIsCurrent(userId)),
        () => false,
    );
    const transport = useMemo<AgentTransport>(() => {
        const requireCurrentAccount = () => {
            if (!userId || !accountIsCurrent(userId)) throw new DOMException("Agent 账号已变更", "AbortError");
        };
        return {
            async request(call) {
                requireCurrentAccount();
                const response = await agentWebTransport.request(call);
                requireCurrentAccount();
                return response;
            },
            async openEvents(call) {
                requireCurrentAccount();
                return agentWebTransport.openEvents(call);
            },
        };
    }, [userId]);
    useLayoutEffect(() => {
        if (accountChanged) closeAgentPanelSurface();
    }, [accountChanged]);
    useEffect(() => {
        if (!context.declaration.enabled || !userId || accountChanged) return;
        const openFromShortcut = (event: KeyboardEvent) => {
            if (!(event.metaKey || event.ctrlKey) || event.altKey || event.key.toLowerCase() !== "j") return;
            event.preventDefault();
            openAgentPanelSurface();
        };
        window.addEventListener("keydown", openFromShortcut);
        return () => window.removeEventListener("keydown", openFromShortcut);
    }, [accountChanged, context.declaration.enabled, userId]);
    if (!context.declaration.enabled || accountChanged) return null;
    const pendingDirectorEntry = userId ? <AgentPendingDirectorEntry transport={transport} ownCanvasId={context.scope?.type === "canvas" ? context.scope.id : undefined} /> : null;
    if (context.status === "loading") {
        return (
            <>
                {pendingDirectorEntry}
                <AgentEntryStatus label="正在加载 Agent 接入" />
            </>
        );
    }
    if (context.status === "error" || !context.scope) {
        return (
            <>
                {pendingDirectorEntry}
                <AgentEntryStatus label={context.error || "当前页面 Agent 暂不可用"} disabled />
            </>
        );
    }
    return (
        <>
            {pendingDirectorEntry}
            <DeclaredAgentPanel userId={userId} transport={transport} context={context} scope={context.scope} open={open} onOpen={openAgentPanelSurface} onClose={closeAgentPanelSurface} />
        </>
    );
}

function DeclaredAgentPanel({ userId, transport, context, scope, open, onOpen, onClose }: { userId?: string; transport: AgentTransport; context: AgentPageContext; scope: AgentBusinessScope; open: boolean; onOpen: () => void; onClose: () => void }) {
    const configId = context.declaration.enabled ? (context.declaration.kind === "platform" ? "platform" : context.declaration.kind === "business" ? context.declaration.businessType : "") : "";
    const config = resolveAgentPanelConfig(configId);
    const registeredCanvasAdapter = useAgentPanelAdapter(context.declaration.enabled && context.declaration.kind === "business" ? context.declaration.businessType : "");
    const adapter = configId === "canvas" ? registeredCanvasAdapter : configId === "drama" ? dramaAgentPanelAdapter : configId === "platform" ? platformAgentPanelAdapter : null;
    const definitionId = context.agentDefinitionId || config?.agentDefinitionId || "";
    const client = useMemo(() => createAgentClient({ transport, scope, agentDefinitionId: definitionId || undefined }), [definitionId, scope.type, scope.id, transport]);
    const capability = useAgentCapabilities({ active: Boolean(userId), client, scope, agentDefinitionId: definitionId });
    if (!config) {
        return (
            <>
                <AgentEntryStatus label={`当前业务未注册 Agent 配置：${configId || "未知"}`} disabled />
                {open ? null : null}
            </>
        );
    }
    if (!adapter) {
        return (
            <>
                <AgentEntryStatus label="正在加载业务 Agent 适配器" />
                {open ? null : null}
            </>
        );
    }
    if (capability.status === "loading" || capability.status === "idle") {
        return (
            <>
                <AgentEntryStatus label="正在校验 Agent 能力" />
                {open ? null : null}
            </>
        );
    }
    if (capability.status === "error" || !capability.capabilities) {
        return <AgentEntryStatus label={`${capability.error || "Agent 能力暂不可用"}，点击重试`} onRetry={capability.retry} />;
    }
    return (
        <AgentPanel
            key={`${config.id}:${scope.type}:${scope.id}:${definitionId}`}
            userId={userId}
            context={context}
            config={config}
            adapter={adapter}
            client={client}
            capabilities={capability.capabilities}
            open={open}
            onOpen={onOpen}
            onClose={onClose}
            initialRunId={context.target?.openedRunId}
        />
    );
}

function AgentPendingDirectorEntry({ ownCanvasId, transport }: { ownCanvasId?: string; transport: AgentTransport }) {
    const client = useMemo(() => createAgentClient({ transport }), [transport]);
    const [pending, setPending] = useState<AgentPendingDirectorPreview | null>(null);
    useEffect(() => {
        let active = true;
        let controller: AbortController | undefined;
        const refresh = async () => {
            if (document.visibilityState === "hidden") return;
            controller?.abort();
            controller = new AbortController();
            try {
                const page = await client.listPendingDirectorPreviews(controller.signal);
                if (active) setPending(page.items.find((item) => item.canvasId !== ownCanvasId) || null);
            } catch {
                // Optional cross-page notice; a failed poll must not affect the declared Agent surface.
            }
        };
        void refresh();
        const timer = window.setInterval(() => void refresh(), 5_000);
        const onVisibility = () => {
            if (document.visibilityState === "visible") void refresh();
        };
        document.addEventListener("visibilitychange", onVisibility);
        return () => {
            active = false;
            controller?.abort();
            window.clearInterval(timer);
            document.removeEventListener("visibilitychange", onVisibility);
        };
    }, [client, ownCanvasId]);
    if (!pending) return null;
    return (
        <a
            href={`/canvas/${encodeURIComponent(pending.canvasId)}?agent=1`}
            data-agent-pending-director
            className="fixed bottom-24 right-5 z-[69] max-w-72 rounded-xl border border-amber-300/60 bg-amber-50 px-3 py-2 text-left text-xs text-amber-950 shadow-xl"
        >
            <strong className="block">画布预演等待录制</strong>
            <span className="mt-0.5 block text-amber-800">返回画布处理这次预演请求</span>
        </a>
    );
}

function AgentEntryStatus({ label, disabled = false, onRetry }: { label: string; disabled?: boolean; onRetry?: () => void }) {
    return (
        <button type="button" disabled={disabled} onClick={onRetry} className="fixed bottom-5 right-5 z-[70] rounded-full border border-black/10 bg-white/95 px-4 py-3 text-sm text-neutral-600 shadow-xl disabled:opacity-70">
            {label}
        </button>
    );
}
