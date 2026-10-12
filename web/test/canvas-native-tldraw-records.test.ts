import { expect, test } from "bun:test";

test("shared native records restore with actual tldraw 5.2.5 and malformed records fail", async () => {
    const result = await new Promise<{ restored: Record<string, { typeName: string }>; rejected: string[]; expectedRejections: number; corruptPathRejected: boolean }>((resolve, reject) => {
        const worker = new Worker(new URL("./helpers/native-tldraw-records.worker.ts", import.meta.url).href, { type: "module" });
        worker.onmessage = ({ data }) => { worker.terminate(); if (data.error) reject(new Error(data.error)); else resolve(data); };
        worker.onerror = (event) => { worker.terminate(); reject(event.error ?? new Error(event.message)); };
        worker.postMessage(null);
    });
    expect(Object.values(result.restored).filter((item) => item.typeName === "page")).toHaveLength(2);
    expect(Object.values(result.restored).filter((item) => item.typeName === "shape")).toHaveLength(13);
    expect(result.restored["binding:arrow"]).toBeDefined();
    expect(result.restored["asset:image"]).toBeDefined();
    expect(result.rejected).toHaveLength(result.expectedRejections);
    expect(result.rejected).toContain("incomplete native props");
    expect(result.corruptPathRejected).toBe(true);
}, 30_000);
