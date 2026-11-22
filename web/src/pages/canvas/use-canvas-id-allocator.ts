import { useCallback } from "react";

import { allocateSequentialId, createSequentialIdAllocator, prepareSequentialIdBatch } from "@/lib/canvas/canvas-node-id";
import { useCanvasStore } from "@/stores/canvas/use-canvas-store";

/** 画布定位 ID 的分配入口：分配一个顺序 ID，并立刻把推进后的高水位写回项目文档。 */
export type CanvasIdAllocator = (prefix: string, taken?: Iterable<string>) => string;

export function useCanvasIdAllocator(projectId: string): CanvasIdAllocator {
    const updateProject = useCanvasStore((state) => state.updateProject);
    return useCallback(
        (prefix: string, taken?: Iterable<string>) => {
            const project = useCanvasStore.getState().projects.find((item) => item.id === projectId);
            const { id, counters } = allocateSequentialId(project ?? { nodes: [], connections: [] }, prefix, taken);
            if (project) updateProject(projectId, { idCounters: counters });
            return id;
        },
        [projectId, updateProject],
    );
}

export type CanvasIdBatch = {
    allocate: (prefix: string) => string;
    /** 把本事务推进后的高水位写回项目文档；重复调用只生效一次。 */
    commit: () => void;
};

/**
 * 一次业务动作内连续分配多个 ID 的事务：allocate 只读内存，commit 统一写回一次。
 * resetScopeId 用于 submap 子树整体重建，让该 scope 的局部编号从 1 重新开始。
 */
export function useCanvasIdBatcher(projectId: string): (taken?: Iterable<string>, resetScopeId?: string) => CanvasIdBatch {
    const updateProject = useCanvasStore((state) => state.updateProject);
    return useCallback(
        (taken?: Iterable<string>, resetScopeId?: string) => {
            const project = useCanvasStore.getState().projects.find((item) => item.id === projectId);
            const { taken: liveIds, counters } = prepareSequentialIdBatch(project?.nodes ?? [], project?.connections ?? [], taken ?? [], project?.idCounters, resetScopeId);
            const allocate = createSequentialIdAllocator(liveIds, counters);
            let committed = false;
            return {
                allocate,
                commit: () => {
                    if (committed || !project) return;
                    committed = true;
                    updateProject(projectId, { idCounters: { ...counters } });
                },
            };
        },
        [projectId, updateProject],
    );
}
