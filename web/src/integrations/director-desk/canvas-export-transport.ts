import type { MaterializationStatus, Operation } from "@/lib/canvas/command-contract.generated";
import type { CanvasCommandSession } from "@/services/canvas-command-session";
import { canvasCommandSession, connectCanvasCommandWorkspace, openCanvasCommandSession, useCanvasSessionStore } from "@/stores/canvas/use-canvas-session-store";
import type { CanvasNodeData } from "@/types/canvas";
import type { DirectorDeskExportTransport, PrevisExportTask } from "./export-to-canvas";

type Session = Pick<CanvasCommandSession, "state" | "submit" | "submitPrevis" | "reconnect">;
type Dependencies = {
    session: (canvasId: string) => Session | undefined;
    queryTask: (id: string) => Promise<PrevisExportTask>;
    readMaterialization: (id: string) => Promise<MaterializationStatus>;
};

type SessionBootstrapDependencies = {
    getScope: () => string | null;
    getSession: (canvasId: string) => Session | undefined;
    connect: (scope: string) => Promise<void>;
    open: (canvasId: string) => Promise<Session>;
};

export async function ensureDirectorDeskCanvasSession(
    input: { canvasId: string; scope: string },
    dependencies: SessionBootstrapDependencies = {
        getScope: () => useCanvasSessionStore.getState().scope,
        getSession: (canvasId) => canvasCommandSession(canvasId),
        connect: (scope) => connectCanvasCommandWorkspace(scope),
        open: (canvasId) => openCanvasCommandSession(canvasId),
    },
): Promise<Session> {
    if (dependencies.getScope() !== input.scope) await dependencies.connect(input.scope);
    const existing = dependencies.getSession(input.canvasId);
    return existing || dependencies.open(input.canvasId);
}

export function createDirectorDeskExportTransport(deps: Dependencies): DirectorDeskExportTransport {
    const sessionFor = (canvasId: string) => {
        const session = deps.session(canvasId);
        if (!session) throw new Error("画布命令会话未就绪，请重新打开画布");
        return session;
    };
    return {
        submitPrevis: async (input, operationId) => {
            const result = await sessionFor(input.canvasId).submitPrevis({ sceneId: input.sceneId, shotId: input.shotId, options: input.options }, { operationId });
            return { taskId: result.taskId };
        },
        queryTask: deps.queryTask,
        waitForMaterialization: async (taskId) => {
            const deadline = Date.now() + 30000;
            while (Date.now() < deadline) {
                const status = await deps.readMaterialization(taskId);
                if (status.state === "done") return status;
                if (status.state === "failed") throw new Error("预演资源写回失败，请在任务中心重试写回");
                await new Promise((resolve) => setTimeout(resolve, 500));
            }
            throw new Error("预演已完成，画布写回尚未完成，请在任务中心检查");
        },
        readCanvas: async (canvasId) => {
            const session = sessionFor(canvasId);
            await session.reconnect();
            if (session.state.saveState === "offline" || session.state.saveState === "closed") throw new Error("画布状态同步未完成，请稍后重试");
            return { nodes: (session.state.projected.nodes || []) as CanvasNodeData[] };
        },
        writeStoryboard: async (canvasId, rows) => {
            const sceneId = rows[0]?.previsSceneId;
            if (!sceneId || !rows.length || rows.length > 100) throw new Error("镜头表需要 1 至 100 个镜头");
            const session = sessionFor(canvasId);
            const id = `director-storyboard-${sceneId}`;
            const nodes = (session.state.projected.nodes || []) as CanvasNodeData[];
            const node = nodes.find((item) => item.id === id);
            if (node && node.type !== "script") throw new Error("镜头表节点标识已被其他类型占用");
            const currentRows = node?.metadata?.storyboard?.rows || [];
            if (!node) await session.submit([{ type: "add_node", id, nodeType: "script", title: "导演台镜头表", x: Math.max(0, ...nodes.map((item) => item.position?.x + item.width || 0)) + 100, y: 0 }]);
            const nextIds = new Set(rows.map((row) => row.id));
            const operations: Operation[] = currentRows.filter((row) => !nextIds.has(row.id)).map((row) => ({ type: "edit_storyboard", id, action: "remove", rowId: row.id }));
            for (const row of rows) {
                // 只传合同可编辑字段;资源关联和运行状态继续由后台维护。
                const patch = { durationSeconds: row.durationSeconds, videoMotionPrompt: row.videoMotionPrompt, camera: row.camera, motion: row.motion, shotSize: row.shotSize };
                operations.push({ type: "edit_storyboard", id, action: currentRows.some((old) => old.id === row.id) ? "update" : "append", rowId: row.id, patch });
            }
            for (let index = 0; index < operations.length; index += 100) await session.submit(operations.slice(index, index + 100));
        },
    };
}
