import { b64Vecs, createTLStore, loadSnapshot } from "tldraw";

self.onmessage = async () => {
    try {
        const fixture = await Bun.file(new URL("../fixtures/canvas-native-tldraw-records.json", import.meta.url)).json();
        const native = (records: Record<string, Record<string, unknown>>) => {
            const next = structuredClone(records);
            const asset = next["asset:image"]?.props as { src?: string } | undefined;
            if (asset?.src) asset.src = "data:image/png;base64,YQ==";
            return { document: { schema: fixture.schema, store: next } };
        };
        const store = createTLStore();
        loadSnapshot(store, native(fixture.records) as never);
        const restored = store.getStoreSnapshot().store;
        const rejected: string[] = [];
        for (const invalid of fixture.invalidCases.filter((item: { nativeRejects: boolean }) => item.nativeRejects)) {
            try {
                loadSnapshot(createTLStore(), native(invalid.records) as never);
            } catch {
                rejected.push(invalid.name);
            }
        }
        let corruptPathRejected = false;
        try {
            b64Vecs.decodePoints("a");
        } catch {
            corruptPathRejected = true;
        }
        self.postMessage({ restored, rejected, corruptPathRejected, expectedRejections: fixture.invalidCases.filter((item: { nativeRejects: boolean }) => item.nativeRejects).length });
    } catch (error) {
        self.postMessage({ error: String(error) });
    }
};
