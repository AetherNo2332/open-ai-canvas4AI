import { expect, test } from "bun:test";
import { appearanceAssetURL } from "../src/services/api/appearance";
import { DEFAULT_CANVAS_APPEARANCE } from "../src/lib/canvas/agent-appearance";

test("PNG Agent 形象使用公开外观资源地址并保留资源槽位", () => {
    expect(appearanceAssetURL("agent-avatar", "revision 1")).toBe("/api/public/appearance/assets/agent-avatar?rev=revision%201");
    expect(DEFAULT_CANVAS_APPEARANCE.avatarResourceId).toBe("");
    expect(DEFAULT_CANVAS_APPEARANCE.avatarType).toBe("orb");
});
