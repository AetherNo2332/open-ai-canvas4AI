import { expect, test } from "bun:test";
import { withUserScopedPersistenceSuppressed } from "../src/lib/user-scope";
import { useCanvasStore } from "../src/stores/canvas/use-canvas-store";
import { useThemeStore } from "../src/stores/use-theme-store";

test("only newly created canvases use the dot default; legacy missing grid remains blank", () => {
    const previous = useCanvasStore.getState().projects;
    withUserScopedPersistenceSuppressed(() => {
        try {
            const created = useCanvasStore.getState().createProject("new");
            const legacy = useCanvasStore.getState().importProject({ title: "legacy" });
            const explicit = useCanvasStore.getState().importProject({ title: "explicit", backgroundMode: "lines" });
            const projects = useCanvasStore.getState().projects;
            expect(projects.find(project => project.id === created)).toMatchObject({ backgroundMode: "dots", appearance: { mode: "preset", preset: { id: useThemeStore.getState().theme } } });
            expect(projects.find(project => project.id === legacy)?.backgroundMode).toBe("blank");
            expect(projects.find(project => project.id === explicit)?.backgroundMode).toBe("lines");
        } finally {
            useCanvasStore.setState({ projects: previous });
        }
    });
});
