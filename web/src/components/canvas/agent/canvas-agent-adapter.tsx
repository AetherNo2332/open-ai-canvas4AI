import { useCallback, useEffect, useMemo, useRef } from "react";
import { dispatchDirectorOpen } from "@/lib/canvas/agent-director-launch";
import { notifyAgentApproval } from "@/lib/canvas/agent-browser-notifications";
import { getActiveUserScope } from "@/lib/user-scope";
import { createAgentCanvasSync } from "@/services/agent-canvas-sync";
import { canvasRealtimeConnected } from "@/services/canvas-realtime";
import { undoAgentRun } from "@/services/api/agent";
import { applyAgentCanvasPatches, refreshCanvasAfterAgent, saveRemoteUserDataNow } from "@/services/user-data-sync";
import type { AgentPanelAdapter, AgentPanelBindings, AgentPanelPrefill } from "@/services/agent/panel-adapters";
import { settleAgentPrefill, takeAgentPrefill, useGlobalAgentStore } from "@/stores/use-global-agent-store";
import type { AgentPromptEditorProps } from "@/components/agent/agent-prompt-editor";
import { CanvasResourceMentionTextarea } from "@/components/canvas/canvas-resource-mention-textarea";
import type { CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";
import { useAgentPanelDock } from "../use-agent-panel-dock";

const EMPTY_REFERENCES: NonNullable<AgentPanelBindings["references"]> = [];
const EMPTY_IDS: string[] = [];

function CanvasAgentPromptEditor(props: AgentPromptEditorProps) {
    return <CanvasResourceMentionTextarea {...props} references={(props.references || []) as CanvasResourceReference[]} onChange={props.onChange} onSubmit={props.onSubmit} onContentSizeChange={props.onContentSizeChange} />;
}

function useCanvasAgentBindings(): AgentPanelBindings {
    const canvas = useGlobalAgentStore((state) => state.canvas);
    const prefillRequest = useGlobalAgentStore((state) => state.prefillRequest);
    const setDocked = useGlobalAgentStore((state) => state.setDocked);
    const setDockWidth = useGlobalAgentStore((state) => state.setDockWidth);
    const dock = useAgentPanelDock();
    const canvasId = canvas?.canvasId || "";
    const userScope = getActiveUserScope();
    const syncRef = useRef<ReturnType<typeof createAgentCanvasSync> | null>(null);

    useEffect(() => {
        if (!canvasId) {
            syncRef.current?.dispose();
            syncRef.current = null;
            return;
        }
        const sync = createAgentCanvasSync({
            canvasId,
            applyPatches: (patches) => applyAgentCanvasPatches(canvasId, patches),
            refresh: () => refreshCanvasAfterAgent(canvasId),
            realtimeConnected: () => canvasRealtimeConnected(canvasId),
            onError: () => {},
        });
        syncRef.current = sync;
        return () => {
            sync.dispose();
            if (syncRef.current === sync) syncRef.current = null;
        };
    }, [canvasId]);

    const prepareSubmission = useCallback(async () => {
        if (!canvasId) throw new Error("请先在画布中打开 Agent");
        await saveRemoteUserDataNow(canvasId);
    }, [canvasId]);

    const references = canvas?.references || EMPTY_REFERENCES;
    const selectedNodeIds = canvas?.selectedNodeIds || EMPTY_IDS;
    const onFocusNode = canvas?.onFocusNode;
    const insertAgentFiles = canvas?.insertAgentFiles;
    const addFiles = useMemo(
        () =>
            insertAgentFiles
                ? async (files: FileList | File[] | null): Promise<string[]> => {
                      if (!files || !canvas) return [];
                      const values = files instanceof FileList ? Array.from(files) : files;
                      if (!values.length) return [];
                      const nodeIds = await insertAgentFiles(values);
                      if (nodeIds.length) useGlobalAgentStore.getState().setCanvas({ ...canvas, selectedNodeIds: nodeIds.slice(0, 8) });
                      return nodeIds;
                  }
                : undefined,
        [canvas, insertAgentFiles],
    );

    const takePrefill = useCallback((): AgentPanelPrefill | null => {
        if (!prefillRequest || prefillRequest.canvasId !== canvasId || prefillRequest.userScope !== userScope || getActiveUserScope() !== userScope) return null;
        const request = takeAgentPrefill(prefillRequest.id);
        if (!request) return null;
        if (!canvasId || !request.text.trim()) {
            settleAgentPrefill(request.id, false);
            return null;
        }
        return {
            id: request.id,
            text: request.text,
            autoSubmit: request.autoSubmit,
            focusNodeIds: request.focusNodeIds,
            settle: (outcome) => settleAgentPrefill(request.id, outcome),
        };
    }, [canvasId, prefillRequest, userScope]);

    return {
        references,
        focusReference: (reference) => {
            if (reference.nodeId) onFocusNode?.(reference.nodeId);
        },
        addFiles,
        prepareSubmission,
        undoRun: async (runId) => {
            await undoAgentRun(runId);
            await refreshCanvasAfterAgent(canvasId);
        },
        promptEditor: CanvasAgentPromptEditor,
        onEvent: (event) => {
            dispatchDirectorOpen(event);
            syncRef.current?.receive(event);
            if (event.type === "approval_requested") {
                void notifyAgentApproval({
                    approvalId: String(event.payload?.approvalId || "approval"),
                    summary: String(event.payload?.summary || event.payload?.reason || ""),
                });
            }
        },
        takePrefill,
        dock: {
            available: dock.available,
            active: dock.active,
            width: dock.width,
            onToggle: () => setDocked(!useGlobalAgentStore.getState().dock.docked),
            onReset: () => setDockWidth(420),
            onResize: (width) => setDockWidth(width),
        },
        headerAccessory:
            selectedNodeIds.length > 0 ? (
                <span className="rounded-full bg-black/[0.055] px-2 py-1 text-[11px] text-neutral-600" title="本轮会引用当前选中的节点">
                    选中 {selectedNodeIds.length}
                </span>
            ) : null,
    };
}

export const canvasAgentPanelAdapter: AgentPanelAdapter = {
    configId: "canvas",
    owner: "canvas-project",
    useBindings: useCanvasAgentBindings,
    buildRequest: ({ context, capabilities, prompt, idempotencyKey, selection }) => ({
        scope: { type: "canvas", id: context.scope?.id || "" },
        canvasId: context.scope?.id || "",
        prompt,
        permissionMode: selection.permissionMode,
        contextScope: ["canvas"],
        reasoningMode: selection.reasoningMode,
        ...(selection.model ? { model: selection.model } : {}),
        ...(selection.logicalModelId ? { logicalModelId: selection.logicalModelId } : {}),
        ...(!selection.logicalModelId && selection.channelId ? { channelId: selection.channelId, channelModelKey: selection.channelModelKey } : {}),
        ...(selection.skillIds.length ? { skillIds: selection.skillIds } : {}),
        ...(selection.focusNodeIds?.length ? { focusNodeIds: selection.focusNodeIds.slice(0, 8) } : {}),
        budget: {
            maxCredits: selection.maxCredits ?? 200,
            maxGenerationTasks: capabilities.writeTools && selection.permissionMode !== "read_only" ? 4 : 0,
            maxVideoSeconds: capabilities.writeTools && selection.permissionMode !== "read_only" ? 60 : 0,
        },
        idempotencyKey,
    }),
};
