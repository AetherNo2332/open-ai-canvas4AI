import { expect, test } from "bun:test";
import { DirectorDeskPrevisWorkbench } from "@/components/canvas/previs/director-desk-previs-workbench";
import { useDirectorDeskPrevisRuntime } from "@/components/canvas/previs/use-director-desk-previs-runtime";

test("DirectorDesk is the single workbench entry and owns a runtime hook", () => {
    expect(typeof DirectorDeskPrevisWorkbench).toBe("function");
    expect(typeof useDirectorDeskPrevisRuntime).toBe("function");
});
