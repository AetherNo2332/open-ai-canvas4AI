import { expect, test } from "bun:test";
import { useCanvasStore, withCanvasStorePersistenceSuppressed } from "../src/stores/canvas/use-canvas-store";

test("new web canvases start with the reference field contract and imports retain their migration state", () => {
    const original = useCanvasStore.getState().projects;
    try {
        withCanvasStorePersistenceSuppressed(() => {
            const freshId = useCanvasStore.getState().createProject("新画布");
            expect(useCanvasStore.getState().openProject(freshId)).toMatchObject({ referenceFieldsVersion: 1 });
            const normalizedId = useCanvasStore.getState().importProject({ title: "已迁移", referenceFieldsVersion: 1 });
            expect(useCanvasStore.getState().openProject(normalizedId)).toMatchObject({ referenceFieldsVersion: 1 });
            const legacyId = useCanvasStore.getState().importProject({ title: "历史画布" });
            expect(useCanvasStore.getState().openProject(legacyId)?.referenceFieldsVersion).toBeUndefined();
        });
    } finally {
        withCanvasStorePersistenceSuppressed(() => useCanvasStore.setState({ projects: original }));
    }
});
