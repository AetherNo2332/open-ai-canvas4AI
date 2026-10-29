import { expect, mock, setSystemTime, test } from "bun:test";

import type { CanvasNodeData } from "../src/types/canvas";
import parity from "../../backend/internal/canvas/mediaversions/testdata/parity.json";

// 与 Go 的 TestMediaVersionParity 读同一份夹具；这里调用真实的上游函数，夹具的 expected 必须由它们得出。
// bun 的 mock.module 对同一进程里的其他测试文件也生效：夹具 id 用完后退回真实 nanoid，不影响别的测试。
const realNanoid = (await import("nanoid")).nanoid;
const ids: string[] = [];
mock.module("nanoid", () => ({
    nanoid: (size?: number) => ids.shift() ?? realNanoid(size),
}));

const { adoptMediaVersion, createNodeFromMediaVersion, currentMediaVersion, mediaVersionAdoptBlock, recordReplacedMediaVersion } = await import("../src/lib/canvas/canvas-media-versions");

const MEDIA_KEYS = ["content", "storageKey", "previewContent", "mimeType", "bytes", "durationMs", "hasAudio", "waveform", "waveformVersion", "naturalWidth", "naturalHeight", "videoPreview", "imageRotation", "assetId"];

type ParityCase = {
    name: string;
    op: string;
    now: string;
    ids: string[];
    source?: "generate" | "upload";
    versionId?: string;
    newId?: string;
    node: CanvasNodeData;
    previous?: CanvasNodeData;
    args?: { media: Record<string, unknown>; generation: Record<string, unknown> };
    expected: unknown;
};

const plain = (value: unknown) => (value === undefined ? null : JSON.parse(JSON.stringify(value)));

function run(c: ParityCase) {
    switch (c.op) {
        case "recordGenerated": {
            const metadata = { ...c.node.metadata };
            for (const key of MEDIA_KEYS) delete metadata[key];
            Object.assign(metadata, c.args!.media, c.args!.generation);
            return { node: recordReplacedMediaVersion(c.node, { ...c.node, metadata }, "generate") };
        }
        case "recordReplaced":
            return { node: recordReplacedMediaVersion(c.previous, c.node, c.source!) };
        case "adopt":
            return { node: adoptMediaVersion(c.node, c.versionId!) };
        case "adoptBlock":
            return { block: mediaVersionAdoptBlock(c.node) };
        case "extract":
            return { node: createNodeFromMediaVersion(c.node, c.versionId!, c.newId) };
        case "current":
            return { versionId: currentMediaVersion(c.node)?.id ?? null };
    }
    throw new Error(`unknown op ${c.op}`);
}

for (const c of (parity as { cases: ParityCase[] }).cases) {
    test(`媒体版本 Go/TS 一致：${c.name}`, () => {
        ids.splice(0, ids.length, ...c.ids);
        setSystemTime(new Date(c.now));
        try {
            expect(plain(run(c))).toEqual(c.expected);
            expect(ids).toEqual([]);
        } finally {
            setSystemTime();
        }
    });
}
