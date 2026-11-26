import { describe, expect, test } from "bun:test";

import { shortplayCanvasEntriesEnabled } from "@/lib/canvas/canvas-shortplay-asset";
import { addNodeMenuCommands } from "@/lib/canvas/tool-registry/definitions/add-node-menu-tools";
import type { AddNodeMenuContext } from "@/lib/canvas/tool-registry/tool-definition";

// 短剧工作台（shortplayEnabled）与 drama 制作台（shortDramaEnabled）是两个独立开关：
// 自由画布上的短剧局部卡/「收录到短剧」只跟 shortplayEnabled 走，关联画布一律让位给作品资产。

const SHORTPLAY_CARD_IDS = ["shortplay-asset-character", "shortplay-asset-prop", "shortplay-asset-scene"];

function addNodeContext(input: { linked: boolean; withShortplayHandler: boolean }): AddNodeMenuContext {
    return {
        workspaceMode: "simple",
        isProjectLinked: input.linked,
        handlers: { onAddShortplayAsset: input.withShortplayHandler ? () => {} : undefined },
    } as unknown as AddNodeMenuContext;
}

function visibleShortplayCards(input: { linked: boolean; withShortplayHandler: boolean }): string[] {
    const context = addNodeContext(input);
    return addNodeMenuCommands.filter((command) => SHORTPLAY_CARD_IDS.includes(command.id) && (command.applicable?.(context) ?? true)).map((command) => command.id);
}

describe("shortplay canvas entries follow their own switch", () => {
    test("entries need shortplayEnabled and an unlinked canvas", () => {
        expect(shortplayCanvasEntriesEnabled({ shortplayEnabled: true, isProjectLinked: false })).toBe(true);
        expect(shortplayCanvasEntriesEnabled({ shortplayEnabled: true, isProjectLinked: true })).toBe(false);
        expect(shortplayCanvasEntriesEnabled({ shortplayEnabled: false, isProjectLinked: false })).toBe(false);
        expect(shortplayCanvasEntriesEnabled({ shortplayEnabled: false, isProjectLinked: true })).toBe(false);
    });

    test("no shortplay cards are offered when the handler is not wired", () => {
        expect(visibleShortplayCards({ linked: false, withShortplayHandler: false })).toEqual([]);
        expect(visibleShortplayCards({ linked: false, withShortplayHandler: true })).toEqual(SHORTPLAY_CARD_IDS);
        expect(visibleShortplayCards({ linked: true, withShortplayHandler: true })).toEqual([]);
    });

    test("both switches combine without leaking shortplay entries into a disabled workbench", () => {
        for (const shortDramaEnabled of [false, true]) {
            for (const shortplayEnabled of [false, true]) {
                // 画布本身关联了作品，只有 drama 开关打开时才按关联画布处理。
                const linked = shortDramaEnabled;
                const entries = shortplayCanvasEntriesEnabled({ shortplayEnabled, isProjectLinked: linked });
                const cards = visibleShortplayCards({ linked, withShortplayHandler: entries });
                expect(cards).toEqual(entries ? SHORTPLAY_CARD_IDS : []);
            }
        }
    });
});
