// Regenerate with Bun when the pinned native engine is upgraded.
import { b64Vecs, createTLStore, defaultShapeUtils, loadSnapshot } from "tldraw";

const store = createTLStore();
const schema = store.getStoreSnapshot().schema;
const records: Record<string, Record<string, unknown>> = {
    "document:document": { id: "document:document", typeName: "document", gridSize: 10, name: "Native parity", meta: {} },
    "page:one": { id: "page:one", typeName: "page", name: "First", index: "a1", meta: {} },
    "page:two": { id: "page:two", typeName: "page", name: "Second", index: "a2", meta: {} },
};
for (const Util of defaultShapeUtils) {
    const props = new Util({} as never).getDefaultProps();
    const id = `shape:${Util.type}`;
    records[id] = { id, typeName: "shape", type: Util.type, x: 10, y: 20, rotation: 0, index: "a1", parentId: "page:one", isLocked: false, opacity: 1, props, meta: {} };
}
records["shape:group"] = { ...structuredClone(records["shape:geo"]), id: "shape:group", type: "group", props: {}, parentId: "page:two" };
records["shape:geo"].parentId = "shape:group";
(records["shape:draw"].props as Record<string, unknown>).segments = [
    {
        type: "free",
        path: b64Vecs.encodePoints([
            { x: 0, y: 0, z: 0.5 },
            { x: 1, y: 2, z: 0.5 },
        ]),
    },
];
(records["shape:highlight"].props as Record<string, unknown>).segments = [
    {
        type: "straight",
        dim: 2,
        path: b64Vecs.encodePoints(
            [
                { x: 0, y: 0 },
                { x: 1, y: 2 },
            ],
            2,
        ),
    },
];
records["asset:image"] = { id: "asset:image", typeName: "asset", type: "image", props: { w: 100, h: 100, name: "Owned image", isAnimated: false, mimeType: "image/png", src: "resource:owned-image" }, meta: {} };
(records["shape:image"].props as Record<string, unknown>).assetId = "asset:image";
records["binding:arrow"] = { id: "binding:arrow", typeName: "binding", type: "arrow", fromId: "shape:arrow", toId: "shape:geo", props: { terminal: "end", normalizedAnchor: { x: 0.5, y: 0.5 }, isExact: false, isPrecise: false, snap: "none" }, meta: {} };
const invalidCases: Array<{ name: string; records: typeof records; nativeRejects: boolean }> = [];
function invalid(name: string, mutate: (next: typeof records) => void, nativeRejects = true) {
    const next = structuredClone(records);
    mutate(next);
    invalidCases.push({ name, records: next, nativeRejects });
}
invalid("missing native base field", (next) => {
    delete next["shape:geo"].rotation;
});
invalid("unknown native shape type", (next) => {
    next["shape:geo"].type = "alien";
});
invalid("incomplete native props", (next) => {
    delete (next["shape:geo"].props as Record<string, unknown>).richText;
});
invalid("invalid numeric prop", (next) => {
    (next["shape:geo"].props as Record<string, unknown>).w = 0;
});
invalid("invalid style enum", (next) => {
    (next["shape:geo"].props as Record<string, unknown>).fill = "unknown";
});
invalid("enum cannot contain two valid values", (next) => {
    (next["shape:geo"].props as Record<string, unknown>).color = "black grey";
});
invalid("invalid line point", (next) => {
    (next["shape:line"].props as Record<string, unknown>).points = { a1: { id: "a1", index: "a1", x: "bad", y: 0 } };
});
invalid("invalid segment", (next) => {
    (next["shape:draw"].props as Record<string, unknown>).segments = [{ type: "unknown", path: "" }];
});
invalid(
    "unrenderable segment encoding",
    (next) => {
        (next["shape:draw"].props as Record<string, unknown>).segments = [{ type: "free", path: "a" }];
    },
    false,
);
invalid("invalid index", (next) => {
    next["shape:geo"].index = "0";
});
invalid("invalid opacity", (next) => {
    next["shape:geo"].opacity = 2;
});
invalid("incomplete asset", (next) => {
    delete (next["asset:image"].props as Record<string, unknown>).mimeType;
});
invalid("incomplete binding", (next) => {
    delete (next["binding:arrow"].props as Record<string, unknown>).snap;
});
invalid(
    "missing referenced asset",
    (next) => {
        delete next["asset:image"];
    },
    false,
);
invalid(
    "wrong referenced asset type",
    (next) => {
        next["asset:image"].type = "video";
    },
    false,
);
invalid("shape parent is asset", (next) => {
    next["shape:geo"].parentId = "asset:image";
});
invalid(
    "missing parent page",
    (next) => {
        next["shape:geo"].parentId = "page:missing";
    },
    false,
);
invalid(
    "parent cycle",
    (next) => {
        next["shape:group"].parentId = "shape:geo";
    },
    false,
);
invalid("binding target is page", (next) => {
    next["binding:arrow"].toId = "page:one";
});
invalid(
    "binding source is not arrow",
    (next) => {
        next["binding:arrow"].fromId = "shape:image";
    },
    false,
);

function hydrate(input: typeof records) {
    const next = structuredClone(input);
    (next["asset:image"]?.props as Record<string, unknown> | undefined)?.src && ((next["asset:image"].props as Record<string, unknown>).src = "data:image/png;base64,YQ==");
    return next;
}
loadSnapshot(createTLStore(), { document: { store: hydrate(records), schema } } as never);
for (const fixture of invalidCases.filter((item) => item.nativeRejects)) {
    let rejected = false;
    try {
        loadSnapshot(createTLStore(), { document: { store: hydrate(fixture.records), schema } } as never);
    } catch {
        rejected = true;
    }
    if (!rejected) throw new Error(`Native engine accepted rejection fixture: ${fixture.name}`);
}
await Bun.write(new URL("../fixtures/canvas-native-tldraw-records.json", import.meta.url), JSON.stringify({ engineVersion: "5.2.5", schema, records, invalidCases }, null, 2) + "\n");
console.log(`Verified all built-in native shapes and ${invalidCases.filter((item) => item.nativeRejects).length} malformed records against loadSnapshot.`);
process.exit(0);
