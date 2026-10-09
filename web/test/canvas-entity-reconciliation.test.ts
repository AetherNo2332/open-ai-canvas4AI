import { expect, test } from "bun:test";
import { canvasEntityReconciler } from "@/lib/canvas/canvas-entity-reconciliation";

test("remote graph removal clears stale selection, editor/modal IDs and connection focus", () => {
    const reconcile = canvasEntityReconciler([{ id: "kept" }], [{ id: "kept-edge" }]);
    expect(reconcile.nodeId("deleted")).toBeNull();
    expect(reconcile.nodeId("kept")).toBe("kept");
    expect(reconcile.nodeId(null)).toBeNull();
    expect(reconcile.connectionId("deleted-edge")).toBeNull();
    expect(reconcile.connectionId("kept-edge")).toBe("kept-edge");
    const selected = new Set(["deleted", "kept"]);
    expect(reconcile.nodeSelection(selected)).toEqual(new Set(["kept"]));
    expect(selected).toEqual(new Set(["deleted", "kept"]));
    const retained = new Set(["kept"]);
    expect(reconcile.nodeSelection(retained)).toBe(retained);
});
