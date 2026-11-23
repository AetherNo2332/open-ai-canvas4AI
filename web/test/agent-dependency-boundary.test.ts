import { describe, expect, test } from "bun:test";
import { Glob } from "bun";

const forbidden = [
    "@/components/canvas",
    "@/stores/canvas",
    "@/stores/use-global-agent-store",
    "@/lib/canvas",
    "@/types/canvas",
    "@/services/user-data-sync",
    "previs",
];

describe("unified Agent dependency boundary", () => {
    test("shared shell, controller, storage and SDK do not import canvas-only modules", async () => {
        const roots = ["src/components/agent", "src/services/agent", "src/lib/agent", "src/sdk/agent"];
        const violations: string[] = [];
        for (const root of roots) {
            const glob = new Glob(`${root}/**/*.{ts,tsx}`);
            for await (const path of glob.scan({ cwd: ".", absolute: false })) {
                const text = await Bun.file(path).text();
                for (const token of forbidden) {
                    if (text.includes(token)) violations.push(`${path}: ${token}`);
                }
            }
        }
        expect(violations).toEqual([]);
    });

    test("the drama script keeps only the global Agent entry", async () => {
        const script = await Bun.file("src/pages/projects/studio/script-view.tsx").text();
        expect(script).not.toContain("DramaAgentAnalysis");
        expect(script).not.toContain("本集分析建议");
        expect(await Bun.file("src/pages/projects/studio/agent-analysis.tsx").exists()).toBe(false);
    });
});
