import { mergeAgentCanvasEditor } from "@/lib/canvas/agent-canvas-patch";
import { useCallback, useEffect, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction } from "react";
import { App } from "antd";
import { useNavigate } from "react-router";

import { canvasAppearanceBaseTheme, canvasAppearanceForTheme, DEFAULT_CANVAS_BACKGROUND_MODE, normalizeCanvasAppearance, type CanvasAppearance } from "@/lib/canvas/canvas-appearance";
import type { CanvasBackgroundMode } from "@/lib/canvas-theme";
import { removeCanvasDrawing } from "@/lib/canvas/canvas-drawing-storage";
import { normalizeCanvasNodeTimestamps } from "@/lib/canvas/canvas-node-timestamps";
import { hydrateAssistantImages, resetInterruptedGeneration } from "@/lib/canvas/canvas-project-generation";
import { listAddedSkills, type Skill } from "@/services/api/skills";
import { getCanvasHistoryEntry } from "@/services/api/user-data";
import { createCanvasProjectWithRemoteSync, deleteCanvasProjectsWithRemoteSync, forceOverwriteRemoteCanvasSync, loadCanvasProjectForEditing, saveRemoteUserDataNow, subscribeAgentCanvasRefresh } from "@/services/user-data-sync";
import { flushCanvasStorePersistence, useCanvasStore, withCanvasStorePersistenceSuppressed, type CanvasProject } from "@/stores/canvas/use-canvas-store";
import { useCanvasSessionStore, openCanvasCommandSession, canvasCommandSession, canvasPersonalViewStorage, registerCanvasCommandEditor, commitCanvasCommandEditor } from "@/stores/canvas/use-canvas-session-store";
import { attachCanvasCommandProject } from "@/services/canvas-command-project-lifecycle";
import type { CanvasDocument } from "@/lib/canvas/canvas-event-projection";
import { useCanvasThemeStore } from "@/stores/canvas/use-canvas-theme-store";
import { useSyncProgressStore } from "@/stores/use-sync-progress-store";
import { readCanvasSyncDrafts } from "@/services/canvas-sync-drafts";
import { useUserStore } from "@/stores/use-user-store";
import { ensureOrdinaryCanvasCommandWorkspace } from "@/services/canvas-command-bootstrap";
import type { CanvasAssistantSession, CanvasConnection, CanvasNodeData, ViewportTransform } from "@/types/canvas";
import type { CanvasHistorySnapshot } from "./use-canvas-history";

type UseCanvasProjectLifecycleOptions = {
    projectId: string;
    projectLoaded: boolean;
    nodes: CanvasNodeData[];
    connections: CanvasConnection[];
    chatSessions: CanvasAssistantSession[];
    activeChatId: string | null;
    canvasAppearance: CanvasAppearance;
    backgroundMode: CanvasBackgroundMode;
    showImageInfo: boolean;
    viewport: ViewportTransform;
    nodesRef: MutableRefObject<CanvasNodeData[]>;
    connectionsRef: MutableRefObject<CanvasConnection[]>;
    chatSessionsRef: MutableRefObject<CanvasAssistantSession[]>;
    activeChatIdRef: MutableRefObject<string | null>;
    viewportRef: MutableRefObject<ViewportTransform>;
    historyPausedRef: MutableRefObject<boolean>;
    setNodes: Dispatch<SetStateAction<CanvasNodeData[]>>;
    setConnections: Dispatch<SetStateAction<CanvasConnection[]>>;
    setChatSessions: Dispatch<SetStateAction<CanvasAssistantSession[]>>;
    setActiveChatId: Dispatch<SetStateAction<string | null>>;
    setCanvasAppearance: Dispatch<SetStateAction<CanvasAppearance>>;
    setBackgroundMode: Dispatch<SetStateAction<CanvasBackgroundMode>>;
    setShowImageInfo: Dispatch<SetStateAction<boolean>>;
    setViewport: Dispatch<SetStateAction<ViewportTransform>>;
    setProjectLoaded: Dispatch<SetStateAction<boolean>>;
    resetHistory: (snapshot: CanvasHistorySnapshot) => void;
    cleanupAssetImages: (options?: unknown) => void;
    cleanupCanvasFiles: (extra?: unknown) => void;
};

export function useCanvasProjectLifecycle({
    projectId,
    projectLoaded,
    nodes,
    connections,
    chatSessions,
    activeChatId,
    canvasAppearance,
    backgroundMode,
    showImageInfo,
    viewport,
    nodesRef,
    connectionsRef,
    chatSessionsRef,
    activeChatIdRef,
    viewportRef,
    historyPausedRef,
    setNodes,
    setConnections,
    setChatSessions,
    setActiveChatId,
    setCanvasAppearance,
    setBackgroundMode,
    setShowImageInfo,
    setViewport,
    setProjectLoaded,
    resetHistory,
    cleanupAssetImages,
    cleanupCanvasFiles,
}: UseCanvasProjectLifecycleOptions) {
    const { message } = App.useApp();
    const navigate = useNavigate();
    const hydrated = useCanvasStore((state) => state.hydrated);
    const sessionHydrated = useUserStore((state) => state.hydrated);
    const openProject = useCanvasStore((state) => state.openProject);
    const updateProject = useCanvasStore((state) => state.updateProject);
    const renameProject = useCanvasStore((state) => state.renameProject);
    const currentProject = useCanvasStore((state) => state.projects.find((project) => project.id === projectId));
    const commandScope = useCanvasSessionStore((state) => state.scope);
    const commandConnectionGeneration = useCanvasSessionStore((state) => state.connectionGeneration);
    const commandBootstrapState = useCanvasSessionStore((state) => state.bootstrapState);
    const commandBootstrapError = useCanvasSessionStore((state) => state.bootstrapError);
    const commandEditorRef = useRef<Awaited<ReturnType<typeof attachCanvasCommandProject<CanvasNodeData, CanvasConnection>>> | null>(null);
    const sharedSettingsRef = useRef({ canvasAppearance, backgroundMode, showImageInfo });
    sharedSettingsRef.current = { canvasAppearance, backgroundMode, showImageInfo };
    const [addedSkills, setAddedSkills] = useState<Skill[]>([]);
    const [loadError, setLoadError] = useState("");
    const [loadAttempt, setLoadAttempt] = useState(0);
    const [agentCreatedNodes, setAgentCreatedNodes] = useState<{ projectId: string; nodes: CanvasNodeData[] } | null>(null);
    const viewportSaveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    const observedContentRef = useRef<CanvasHistorySnapshot | null>(null);
    const loadLatestRef = useRef(false);
    const historyRestoreRef = useRef<{ snapshotId: string; revision: number; resolve: () => void; reject: (error: unknown) => void } | null>(null);
    const pendingReloadRef = useRef<{ resolve: () => void; reject: (error: unknown) => void } | null>(null);
    const editorReadyRef = useRef(false);
    useEffect(() => {
        if (commandBootstrapState === "failed") setLoadError(commandBootstrapError || "画布同步连接暂不可用，请重试");
    }, [commandBootstrapError, commandBootstrapState]);

    useEffect(() => {
        if (!commandScope || !hydrated || !sessionHydrated) return;
        const abort = new AbortController();
        let dispose: (() => void) | undefined;
        setProjectLoaded(false);
        setLoadError("");
        editorReadyRef.current = false;
        observedContentRef.current = null;
        void (async () => {
            const session = await openCanvasCommandSession(projectId);
            if (loadAttempt) await session.reconnect();
            abort.signal.throwIfAborted();
            const capabilities = useCanvasSessionStore.getState().capabilities;
            if (!capabilities || useCanvasSessionStore.getState().scope !== commandScope) throw new Error("canvas_command_workspace_unavailable");
            const editor = await attachCanvasCommandProject({
                canvasId: projectId,
                session,
                capabilities,
                signal: abort.signal,
                refs: { nodes: nodesRef, connections: connectionsRef },
                view: canvasPersonalViewStorage(),
                readViewport: () => viewportRef.current,
                presentViewport: (value) => {
                    viewportRef.current = value;
                    setViewport(value);
                },
                read: () => {
                    const stored = useCanvasStore.getState().projects.find((project) => project.id === projectId);
                    const settings = sharedSettingsRef.current;
                    return {
                        ...session.state.projected,
                        ...(stored ? { previsScenes: stored.previsScenes, timeline: stored.timeline } : {}),
                        nodes: nodesRef.current,
                        connections: connectionsRef.current,
                        appearance: settings.canvasAppearance,
                        backgroundMode: settings.backgroundMode,
                        showImageInfo: settings.showImageInfo,
                    } as CanvasDocument;
                },
                present: (document, state) => {
                    if (abort.signal.aborted) return;
                    const fallbackTheme = useCanvasThemeStore.getState().theme;
                    const appearance = document.appearance ? normalizeCanvasAppearance(document.appearance, fallbackTheme) : canvasAppearanceForTheme(fallbackTheme);
                    const background = (document.backgroundMode || DEFAULT_CANVAS_BACKGROUND_MODE) as CanvasBackgroundMode;
                    const info = Boolean(document.showImageInfo);
                    sharedSettingsRef.current = { canvasAppearance: appearance, backgroundMode: background, showImageInfo: info };
                    const snapshot: CanvasHistorySnapshot = { nodes: nodesRef.current, connections: connectionsRef.current, chatSessions: [], activeChatId: null, canvasAppearance: appearance, backgroundMode: background, showImageInfo: info };
                    observedContentRef.current = snapshot;
                    const cached = useCanvasStore.getState().projects.find((project) => project.id === projectId);
                    const project = {
                        ...cached,
                        ...document,
                        id: projectId,
                        revision: state.acknowledged?.revision,
                        title: String(document.title || cached?.title || "未命名画布"),
                        createdAt: String(document.createdAt || cached?.createdAt || new Date().toISOString()),
                        updatedAt: String(document.updatedAt || cached?.updatedAt || new Date().toISOString()),
                        nodes: snapshot.nodes,
                        connections: snapshot.connections,
                        chatSessions: [],
                        activeChatId: null,
                        appearance,
                        backgroundMode: background,
                        showImageInfo: info,
                        viewport: viewportRef.current,
                        previsScenes: Array.isArray(document.previsScenes) ? document.previsScenes : [],
                    } as CanvasProject;
                    withCanvasStorePersistenceSuppressed(() =>
                        useCanvasStore.setState((previous) => ({ projects: previous.projects.some((p) => p.id === projectId) ? previous.projects.map((p) => (p.id === projectId ? project : p)) : [...previous.projects, project] })),
                    );
                    setNodes(snapshot.nodes);
                    setConnections(snapshot.connections);
                    setChatSessions([]);
                    setActiveChatId(null);
                    setCanvasAppearance(appearance);
                    setBackgroundMode(background);
                    setShowImageInfo(info);
                    useCanvasThemeStore.getState().setTheme(canvasAppearanceBaseTheme(appearance, fallbackTheme));
                    if (!editorReadyRef.current) {
                        resetHistory(snapshot);
                        editorReadyRef.current = true;
                        setProjectLoaded(true);
                    }
                    useSyncProgressStore.getState().setProjectProgress(projectId, {
                        phase: state.saveState === "conflict" ? "conflict" : state.saveState === "offline" ? "error" : state.saveState === "saved" ? "done" : "saving",
                        draftCount: state.pending.length,
                        message: state.saveState === "conflict" ? "编辑草稿已保留，请处理冲突" : state.saveState === "offline" ? "主机暂不可用，未确认编辑已保留" : undefined,
                    });
                },
            });
            if (abort.signal.aborted) {
                editor.dispose();
                abort.signal.throwIfAborted();
            }
            commandEditorRef.current = editor;
            dispose = registerCanvasCommandEditor(projectId, editor);
        })().catch((error) => {
            if (!abort.signal.aborted) setLoadError(error instanceof Error ? error.message : "读取本地画布失败");
        });
        return () => {
            abort.abort();
            dispose?.();
            commandEditorRef.current = null;
        };
    }, [
        commandScope,
        commandConnectionGeneration,
        hydrated,
        sessionHydrated,
        projectId,
        loadAttempt,
        resetHistory,
        setProjectLoaded,
        setNodes,
        setConnections,
        setChatSessions,
        setActiveChatId,
        setCanvasAppearance,
        setBackgroundMode,
        setShowImageInfo,
        setViewport,
    ]);

    useEffect(() => {
        if (commandScope || commandBootstrapState !== "inactive" || !hydrated || !sessionHydrated) return;
        let cancelled = false;
        // Keep load intent on the refs until this attempt finishes. React Strict
        // Mode remounts the effect; consuming the flags here would turn "load
        // latest" into a normal open and immediately recreate the conflict.
        const latest = loadLatestRef.current;
        const historyRestore = historyRestoreRef.current;
        const pendingReload = pendingReloadRef.current;
        const keepEditor = editorReadyRef.current && (latest || Boolean(historyRestore));
        if (!keepEditor) {
            editorReadyRef.current = false;
            setProjectLoaded(false);
            setLoadError("");
            observedContentRef.current = null;
        }
        const applyRestoredProject = (targetProject: CanvasProject) => {
            if (cancelled) return;
            const fallbackTheme = useCanvasThemeStore.getState().theme;
            const restoredAppearance = targetProject.appearance ? normalizeCanvasAppearance(targetProject.appearance, fallbackTheme) : canvasAppearanceForTheme(fallbackTheme);
            const initialNodes = normalizeCanvasNodeTimestamps(resetInterruptedGeneration(targetProject.nodes), {
                createdAt: targetProject.createdAt,
                updatedAt: targetProject.updatedAt,
            });
            const snapshot: CanvasHistorySnapshot = {
                nodes: initialNodes,
                connections: targetProject.connections,
                chatSessions: targetProject.chatSessions || [],
                activeChatId: targetProject.activeChatId || null,
                canvasAppearance: restoredAppearance,
                backgroundMode: targetProject.backgroundMode || DEFAULT_CANVAS_BACKGROUND_MODE,
                showImageInfo: targetProject.showImageInfo || false,
            };
            observedContentRef.current = snapshot;
            chatSessionsRef.current = snapshot.chatSessions;
            activeChatIdRef.current = snapshot.activeChatId;
            nodesRef.current = snapshot.nodes;
            connectionsRef.current = snapshot.connections;
            // A refresh after the cached document must preserve the initialized local view.
            // The user may already have panned, or the editor may have centered an empty viewport.
            const restoredViewport = editorReadyRef.current && !latest && !historyRestore ? viewportRef.current : targetProject.viewport;
            viewportRef.current = restoredViewport;
            setNodes(snapshot.nodes);
            setConnections(snapshot.connections);
            setChatSessions(snapshot.chatSessions);
            setActiveChatId(snapshot.activeChatId);
            setCanvasAppearance(snapshot.canvasAppearance);
            useCanvasThemeStore.getState().setTheme(canvasAppearanceBaseTheme(snapshot.canvasAppearance, fallbackTheme));
            setBackgroundMode(snapshot.backgroundMode);
            setShowImageInfo(snapshot.showImageInfo);
            setViewport(restoredViewport);
            resetHistory(snapshot);
            editorReadyRef.current = true;
            setProjectLoaded(true);
        };

        const load = async () => {
            const cachedProject = useCanvasStore.getState().projects.find((p) => p.id === projectId);
            if (!latest && !historyRestore && cachedProject && cachedProject.nodes?.length) {
                // 本地已有该画布的持久化缓存：先以本地数据秒开渲染，彻底消除白屏与等待
                applyRestoredProject(cachedProject);
            }
            const loadedProject = await loadCanvasProjectForEditing(projectId, { latest, historyRestore: historyRestore || undefined, onLoad: applyRestoredProject });
            if (cancelled) return;
            if (historyRestoreRef.current === historyRestore) {
                historyRestoreRef.current = null;
                historyRestore?.resolve();
            }
            if (!loadedProject) {
                if (!cachedProject) navigate("/canvas", { replace: true });
                return;
            }
            const project = useCanvasStore.getState().projects.find((p) => p.id === projectId) || loadedProject;

            // 画布媒体由节点自己的视口观察器按需加载；打开时遍历并解析全部节点会让大画布形成 N+1 资源读取。
            void hydrateAssistantImages(project.chatSessions || [])
                .then((hydratedSessions) => {
                    if (!cancelled)
                        setChatSessions((current) => {
                            const merged = mergeHydratedSessions(current, hydratedSessions);
                            if (observedContentRef.current?.chatSessions === current) observedContentRef.current = { ...observedContentRef.current, chatSessions: merged };
                            return merged;
                        });
                })
                .catch(() => {
                    if (!cancelled) message.warning("部分助手会话素材恢复失败，已使用项目记录继续打开");
                });
        };
        void load()
            .then(() => {
                if (cancelled) return;
                loadLatestRef.current = false;
                if (pendingReloadRef.current === pendingReload) {
                    pendingReloadRef.current = null;
                    pendingReload?.resolve();
                }
            })
            .catch((error) => {
                if (cancelled) return;
                loadLatestRef.current = false;
                if (historyRestoreRef.current === historyRestore) {
                    historyRestoreRef.current = null;
                    historyRestore?.reject(error);
                }
                if (pendingReloadRef.current === pendingReload) {
                    pendingReloadRef.current = null;
                    pendingReload?.reject(error);
                }
                if (useSyncProgressStore.getState().syncingProjects[projectId]?.phase !== "conflict") useSyncProgressStore.getState().setProjectProgress(projectId, { phase: "error", message: error instanceof Error ? error.message : "读取云端版本失败" });
                const detail = error instanceof Error ? error.message : "读取画布失败，请重试";
                if (keepEditor) message.error(detail);
                else setLoadError(detail);
            });
        return () => {
            cancelled = true;
        };
    }, [
        commandScope,
        commandBootstrapState,
        hydrated,
        sessionHydrated,
        loadAttempt,
        message,
        navigate,
        openProject,
        projectId,
        resetHistory,
        setActiveChatId,
        setBackgroundMode,
        setCanvasAppearance,
        setChatSessions,
        setConnections,
        setNodes,
        setShowImageInfo,
        setViewport,
    ]);

    useEffect(() => {
        if (!projectLoaded) return;
        let cancelled = false;
        listAddedSkills()
            .then(({ skills }) => {
                if (!cancelled) setAddedSkills(skills);
            })
            .catch(() => {
                if (!cancelled) setAddedSkills([]);
            });
        return () => {
            cancelled = true;
        };
    }, [projectLoaded]);

    useEffect(() => {
        if (commandScope) return;
        return subscribeAgentCanvasRefresh((project, previous) => {
            if (!projectLoaded || project.id !== projectId) return;
            // Merge only server-changed fields so dragging/editing other nodes can
            // continue while Agent media tasks complete. Same-field conflicts fail.
            const merged = previous ? mergeAgentCanvasEditor(previous, project, nodesRef.current, connectionsRef.current) : project;
            if (observedContentRef.current) {
                const observed = observedContentRef.current;
                // Advance only the observed server fields; edits in live refs still
                // differ from this baseline and must be persisted by the effect below.
                const baseline = previous ? mergeAgentCanvasEditor(previous, project, observed.nodes, observed.connections) : project;
                observedContentRef.current = { ...observed, nodes: baseline.nodes, connections: baseline.connections };
            }
            nodesRef.current = merged.nodes;
            connectionsRef.current = merged.connections;
            setNodes(merged.nodes);
            setConnections(merged.connections);
            const previousIds = new Set(previous?.nodes.map((node) => node.id));
            const created = project.nodes.filter((node) => !previousIds.has(node.id));
            if (created.length) setAgentCreatedNodes({ projectId: project.id, nodes: created });
        });
    }, [commandScope, projectId, projectLoaded, nodesRef, connectionsRef, setNodes, setConnections]);

    useEffect(() => {
        if (!projectLoaded || historyPausedRef.current) return;
        if (!commandScope && commandBootstrapState !== "inactive") return;
        if (commandScope) {
            const timer = setTimeout(() => {
                void commitCanvasCommandEditor(projectId).catch((error) => message.error(error instanceof Error ? error.message : "画布编辑未提交"));
            }, 180);
            return () => clearTimeout(timer);
        }
        const snapshot = { nodes, connections, chatSessions, activeChatId, canvasAppearance, backgroundMode, showImageInfo };
        if (!observedContentRef.current || sameCanvasHistorySnapshot(observedContentRef.current, snapshot)) return;
        observedContentRef.current = snapshot;
        const patch = { nodes, connections, chatSessions, activeChatId, appearance: canvasAppearance, backgroundMode, showImageInfo };
        const stored = useCanvasStore.getState().projects.find((project) => project.id === projectId);
        // 远端结果投影到编辑器不是一次本地编辑，避免改写时间戳并触发反向保存。
        if (stored && sameCanvasPatch(stored, patch)) return;
        updateProject(projectId, patch);
    }, [
        commandScope,
        commandBootstrapState,
        activeChatId,
        backgroundMode,
        canvasAppearance,
        chatSessions,
        connections,
        historyPausedRef,
        nodes,
        projectId,
        projectLoaded,
        showImageInfo,
        updateProject,
        currentProject?.previsScenes,
        currentProject?.timeline,
        message,
    ]);

    useEffect(() => {
        if (!projectLoaded) return;
        if (viewportSaveTimerRef.current) clearTimeout(viewportSaveTimerRef.current);
        viewportSaveTimerRef.current = setTimeout(() => {
            if (commandScope) void commandEditorRef.current?.saveViewport().catch((error) => message.error(error instanceof Error ? error.message : "视角保存失败"));
            else updateProject(projectId, { viewport: viewportRef.current });
            viewportSaveTimerRef.current = null;
        }, 500);
        return () => {
            if (viewportSaveTimerRef.current) clearTimeout(viewportSaveTimerRef.current);
        };
    }, [commandScope, projectId, projectLoaded, updateProject, viewport, viewportRef, message]);

    useEffect(() => {
        const personalView = commandScope ? canvasPersonalViewStorage() : null;
        return () => {
            if (!projectLoaded) return;
            if (viewportSaveTimerRef.current) clearTimeout(viewportSaveTimerRef.current);
            if (personalView) void personalView.write(projectId, viewportRef.current).catch(() => {});
            else updateProject(projectId, { viewport: viewportRef.current });
        };
    }, [commandScope, projectId, projectLoaded, updateProject, viewportRef]);

    const createAndOpenProject = useCallback(() => {
        void createCanvasProjectWithRemoteSync(`自由画布 ${useCanvasStore.getState().projects.length + 1}`)
            .then(({ id, syncError }) => {
                if (syncError) message.warning(syncError instanceof Error ? `画布已在本地创建，云端同步失败：${syncError.message}` : "画布已在本地创建，云端同步失败");
                navigate(`/canvas/${id}`);
            })
            .catch((error) => message.error(error instanceof Error ? error.message : "画布创建失败"));
    }, [message, navigate]);

    const deleteCurrentProject = useCallback(async () => {
        if (commandScope) {
            try {
                await commitCanvasCommandEditor(projectId);
                const session = canvasCommandSession(projectId);
                if (!session) throw new Error("画布会话不可用");
                await session.submit([{ type: "delete_canvas", id: projectId }]);
                withCanvasStorePersistenceSuppressed(() => useCanvasStore.setState((state) => ({ projects: state.projects.filter((project) => project.id !== projectId) })));
                navigate("/canvas");
            } catch (error) {
                message.error(error instanceof Error ? error.message : "删除画布失败");
            }
            return;
        }
        let drawingIds = nodesRef.current.flatMap((node) => (node.type === "drawing" && node.metadata?.drawingId ? [node.metadata.drawingId] : []));
        try {
            const drafts = await readCanvasSyncDrafts(projectId);
            const preserved = new Set(drafts.flatMap((draft) => draft.project.nodes.flatMap((node) => (node.metadata?.drawingId ? [node.metadata.drawingId] : []))));
            drawingIds = drawingIds.filter((id) => !preserved.has(id));
            await deleteCanvasProjectsWithRemoteSync([projectId]);
        } catch (error) {
            message.error(error instanceof Error ? `删除画布失败：${error.message}` : "删除画布失败，请稍后重试");
            return;
        }
        if (drawingIds.length) {
            void Promise.all(drawingIds.map((drawingId) => removeCanvasDrawing(projectId, drawingId))).catch(() => message.warning("项目已删除，但部分本地绘图缓存清理失败"));
        }
        cleanupAssetImages();
        navigate("/canvas");
    }, [commandScope, cleanupAssetImages, message, navigate, nodesRef, projectId]);

    const renameCurrentProject = useCallback(
        (title: string) => {
            if (commandScope) {
                void (async () => {
                    await commitCanvasCommandEditor(projectId);
                    const session = canvasCommandSession(projectId);
                    if (!session) throw new Error("画布会话不可用");
                    await session.submit([{ type: "rename_canvas", id: projectId, title }]);
                })().catch((error) => message.error(error instanceof Error ? error.message : "名称未提交"));
                return;
            }
            renameProject(projectId, title);
            // 标题是画布列表和分享入口的元数据，重命名后立即提交，避免只停留在浏览器缓存。
            void saveRemoteUserDataNow(projectId).catch((error) => {
                message.warning(error instanceof Error ? `名称已更新到本地，云端同步将在后台重试：${error.message}` : "名称已更新到本地，云端同步将在后台重试");
            });
        },
        [commandScope, message, projectId, renameProject],
    );

    const persistLocalEdits = useCallback(async () => {
        if (commandScope) {
            if (!(await commandEditorRef.current?.flush())) throw new Error("画布仍有未确认编辑，草稿已保留");
            return;
        }
        if (commandBootstrapState !== "inactive") throw new Error("画布同步尚未连接，本地编辑已保留");
        const snapshot = { nodes: nodesRef.current, connections: connectionsRef.current, chatSessions, activeChatId, canvasAppearance, backgroundMode, showImageInfo };
        if (observedContentRef.current && !sameCanvasHistorySnapshot(observedContentRef.current, snapshot)) {
            updateProject(projectId, {
                nodes: nodesRef.current,
                connections: connectionsRef.current,
                chatSessions,
                activeChatId,
                appearance: canvasAppearance,
                backgroundMode,
                showImageInfo,
                viewport: viewportRef.current,
            });
            observedContentRef.current = snapshot;
        }
        updateProject(projectId, { viewport: viewportRef.current });
        await flushCanvasStorePersistence();
    }, [commandScope, commandBootstrapState, activeChatId, backgroundMode, canvasAppearance, chatSessions, connectionsRef, nodesRef, projectId, showImageInfo, updateProject, viewportRef]);

    const reloadLatestCanvasProject = useCallback(async () => {
        await persistLocalEdits();
        if (commandScope) {
            await canvasCommandSession(projectId)?.reconnect();
            return;
        }
        return new Promise<void>((resolve, reject) => {
            pendingReloadRef.current?.reject(new Error("已有新的加载请求"));
            loadLatestRef.current = true;
            pendingReloadRef.current = { resolve, reject };
            setLoadAttempt((value) => value + 1);
        });
    }, [commandScope, projectId, persistLocalEdits]);

    const restoreCanvasProjectVersion = useCallback(
        async (snapshotId: string, revision: number) => {
            await persistLocalEdits();
            if (commandScope) {
                const session = canvasCommandSession(projectId);
                if (!session) throw new Error("画布命令会话尚未就绪");
                const saved = await getCanvasHistoryEntry(projectId, snapshotId);
                if (session !== canvasCommandSession(projectId)) throw new Error("画布会话已切换");
                await session.restoreVersion(saved.project as unknown as CanvasDocument, revision);
                return;
            }
            return new Promise<void>((resolve, reject) => {
                historyRestoreRef.current?.reject(new Error("已有新的恢复请求"));
                historyRestoreRef.current = { snapshotId, revision, resolve, reject };
                setLoadAttempt((value) => value + 1);
            });
        },
        [commandScope, projectId, persistLocalEdits],
    );

    const saveCanvasProject = useCallback(
        async (options: { requireRemote?: boolean } = {}): Promise<boolean> => {
            try {
                await persistLocalEdits();
                if (commandScope) {
                    message.success("画布已保存到主机");
                    return true;
                }
            } catch {
                message.error("画布保存失败，请稍后重试");
                return false;
            }
            try {
                await saveRemoteUserDataNow(projectId);
                message.success("画布已保存到云端");
            } catch (error) {
                const detail = error instanceof Error ? error.message : "未知错误";
                message.warning(`本地画布布局已保存，云端同步失败：${detail}`);
                // Imports can retain their durable local result; sharing requires cloud success.
                return options.requireRemote === false;
            }
            return true;
        },
        [commandScope, message, persistLocalEdits, projectId],
    );

    const forceSaveCanvasProject = useCallback(async (): Promise<boolean> => {
        try {
            await persistLocalEdits();
        } catch {
            message.error("本地保存失败，请重试");
            return false;
        }
        if (commandScope) {
            message.success("已核对提交回执，画布已保存到主机");
            return true;
        }
        try {
            const result = await forceOverwriteRemoteCanvasSync();
            message.success(result.reboundNodes > 0 ? `已保存，并修复 ${result.reboundNodes} 处媒体与素材的绑定` : "素材关联已核对，画布已保存");
        } catch (error) {
            message.error(`修复并保存失败：${error instanceof Error ? error.message : "未知错误"}`);
            return false;
        }
        return true;
    }, [commandScope, message, persistLocalEdits]);

    const clearCanvasFiles = useCallback(() => {
        cleanupCanvasFiles({ projectId, nodes: [], chatSessions: [] });
    }, [cleanupCanvasFiles, projectId]);

    return {
        loadError,
        retryLoad: () => {
            if (commandBootstrapState === "failed") void ensureOrdinaryCanvasCommandWorkspace().catch((error) => setLoadError(error instanceof Error ? error.message : "画布同步暂不可用"));
            else setLoadAttempt((attempt) => attempt + 1);
        },
        addedSkills,
        agentCreatedNodes: agentCreatedNodes?.projectId === projectId ? agentCreatedNodes.nodes : null,
        clearCanvasFiles,
        createAndOpenProject,
        currentProject,
        deleteCurrentProject,
        renameCurrentProject,
        reloadLatestCanvasProject,
        restoreCanvasProjectVersion,
        saveCanvasProject,
        persistLocalEdits,
        forceSaveCanvasProject,
        updateProject,
    };
}

function sameCanvasHistorySnapshot(left: CanvasHistorySnapshot, right: CanvasHistorySnapshot) {
    return (
        left.nodes === right.nodes &&
        left.connections === right.connections &&
        left.chatSessions === right.chatSessions &&
        left.activeChatId === right.activeChatId &&
        left.canvasAppearance === right.canvasAppearance &&
        left.backgroundMode === right.backgroundMode &&
        left.showImageInfo === right.showImageInfo
    );
}

function sameCanvasPatch(project: CanvasProject, patch: Pick<CanvasProject, "nodes" | "connections" | "chatSessions" | "activeChatId" | "backgroundMode" | "showImageInfo"> & { appearance: CanvasProject["appearance"] }) {
    return (
        project.nodes === patch.nodes &&
        project.connections === patch.connections &&
        project.chatSessions === patch.chatSessions &&
        project.activeChatId === patch.activeChatId &&
        project.appearance === patch.appearance &&
        project.backgroundMode === patch.backgroundMode &&
        project.showImageInfo === patch.showImageInfo
    );
}

function mergeHydratedSessions(currentSessions: CanvasAssistantSession[], hydratedSessions: CanvasAssistantSession[]) {
    const hydratedById = new Map(hydratedSessions.map((session) => [session.id, session]));
    return currentSessions.map((session) => {
        const hydrated = hydratedById.get(session.id);
        if (!hydrated) return session;
        const hydratedMessages = new Map(hydrated.messages.map((message) => [message.id, message]));
        return {
            ...session,
            messages: session.messages.map((message) => {
                const hydratedMessage = hydratedMessages.get(message.id);
                if (!hydratedMessage || !message.references?.length) return message;
                const hydratedReferences = new Map((hydratedMessage.references || []).map((reference) => [reference.id, reference]));
                return {
                    ...message,
                    references: message.references.map((reference) => {
                        const hydratedReference = hydratedReferences.get(reference.id);
                        return hydratedReference ? { ...reference, dataUrl: hydratedReference.dataUrl, storageKey: hydratedReference.storageKey } : reference;
                    }),
                };
            }),
        };
    });
}
