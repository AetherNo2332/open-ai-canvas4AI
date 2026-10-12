import { expect, test } from "bun:test";

test("bundled DirectorDesk engine exposes the real browser editor core", async () => {
    const module = await import("@/integrations/director-desk/vendor/director-desk-engine");
    expect(typeof module.Engine).toBe("function");
    expect(typeof module.configureCamera).toBe("function");
});
