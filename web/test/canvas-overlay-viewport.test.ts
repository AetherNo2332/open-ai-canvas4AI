import { afterAll, describe, expect, test } from "bun:test";
import { Window } from "happy-dom";
import { canvasOverlayRightEdge } from "@/lib/canvas/overlay-viewport";

const dom = new Window({ url: "http://localhost/", width: 1440, height: 900 });
const doc = dom.document as unknown as Document;
afterAll(() => dom.happyDOM.abort());

function panel({ docked, open, left }: { docked: boolean; open: boolean; left: number }) {
    doc.body.innerHTML = "";
    const aside = doc.createElement("aside");
    aside.className = `canvas-agent-panel${docked ? " is-docked" : ""}`;
    aside.setAttribute("data-agent-open", String(open));
    aside.getBoundingClientRect = () => ({ left, right: 1440, top: 0, bottom: 900, width: 1440 - left, height: 900, x: left, y: 0, toJSON: () => ({}) }) as DOMRect;
    doc.body.append(aside);
}

describe("canvasOverlayRightEdge", () => {
    test("an open docked panel limits canvas overlays to the editor's right edge", () => {
        panel({ docked: true, open: true, left: 1000 });
        expect(canvasOverlayRightEdge(doc)).toBe(1000);
    });

    test("floating or closed panels leave the whole viewport to overlays", () => {
        panel({ docked: false, open: true, left: 900 });
        expect(canvasOverlayRightEdge(doc)).toBe(1440);
        panel({ docked: true, open: false, left: 1000 });
        expect(canvasOverlayRightEdge(doc)).toBe(1440);
        doc.body.innerHTML = "";
        expect(canvasOverlayRightEdge(doc)).toBe(1440);
    });
});
