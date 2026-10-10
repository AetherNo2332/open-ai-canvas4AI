import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

const promptPanel = readFileSync(new URL("../src/components/canvas/canvas-node-prompt-panel.tsx", import.meta.url), "utf8");
const switchMarkup = promptPanel.match(/<button\s+type="button"\s+role="switch"[\s\S]*?<\/button>/)?.[0];

describe("AutoLink switch layout regression", () => {
    test("isolates the fixed rail from global button padding and flex shrinking", () => {
        expect(switchMarkup).toBeDefined();
        expect(switchMarkup).toContain("shrink-0");
        expect(switchMarkup).toContain("h-5 w-9");
        expect(switchMarkup).toContain("padding: 0");
    });

    test("keeps the 14px thumb inside the 36px rail at both animated endpoints", () => {
        expect(switchMarkup).toContain("absolute left-0.5 top-1/2 size-3.5 -translate-y-1/2");
        expect(switchMarkup).toContain('autoLinkEnabled ? "translate-x-4" : "translate-x-0"');
        expect(switchMarkup).toContain("transition-transform");
        expect(switchMarkup).toContain("motion-reduce:transition-none");
        expect(switchMarkup).toContain("onAutoLinkEnabledChange(!autoLinkEnabled)");
    });
});
