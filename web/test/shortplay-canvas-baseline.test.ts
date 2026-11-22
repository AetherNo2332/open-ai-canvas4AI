import { describe, expect, test } from "bun:test";
import { createZip, readZip } from "../src/lib/zip";

const fixtureRoot = new URL("../../testdata/shortplay-canvas-port/zip/", import.meta.url);

async function fixture(name: string) {
    return Bun.file(new URL(name, fixtureRoot)).json() as Promise<Record<string, unknown>>;
}

function manifestProject(manifest: Record<string, unknown>) {
    const projects = manifest.projects as Array<Record<string, unknown>>;
    return projects[0] as Record<string, unknown>;
}

function manifestDrawing(item: Record<string, unknown>) {
    const documents = item.drawingDocuments as Array<Record<string, unknown>>;
    return documents[0] as Record<string, unknown>;
}

async function buildArchive(manifest: Record<string, unknown>) {
    const entries = await fixture("archive-entries.json");
    const files = entries.entries as Array<Record<string, string>>;
    return createZip([
        { name: "projects.json", data: JSON.stringify(manifest) },
        ...files.map((entry) => ({
            name: entry.path,
            data: new Blob([Buffer.from(entry.base64, "base64")]),
        })),
    ]);
}

describe("shortplay ZIP baseline", () => {
    test("v3 and v4 manifests keep their historical drawing contract", async () => {
        const v3 = await fixture("projects-v3.json");
        const v4 = await fixture("projects-v4.json");
        const v3Drawing = manifestDrawing(manifestProject(v3));
        const v4Drawing = manifestDrawing(manifestProject(v4));

        expect(v3.app).toBe("infinite-canvas");
        expect(v3.version).toBe(3);
        expect(v3Drawing.version).toBe(1);
        expect(v3Drawing.engine).toBeUndefined();

        expect(v4.app).toBe("yingce");
        expect(v4.version).toBe(4);
        expect(v4Drawing.version).toBe(2);
        expect(v4Drawing.engine).toBe("tldraw");
        expect((v4Drawing.generationRender as Record<string, unknown>).path).toContain("drawing%3A1.generation.png");
    });

    test("the importer's file-existence check can read both reconstructed archives", async () => {
        for (const name of ["projects-v3.json", "projects-v4.json"]) {
            const manifest = await fixture(name);
            const archive = await readZip(await buildArchive(manifest));
            expect(archive.get("projects.json")?.size).toBeGreaterThan(0);
            const item = manifestProject(manifest);
            const files = item.files as Array<{ path: string }>;
            expect(files.find((file) => !archive.get(file.path))).toBeUndefined();
            const drawing = manifestDrawing(item);
            const previewPath = drawing.previewPath as string;
            expect(archive.get(previewPath)).toBeDefined();
            const render = drawing.generationRender as Record<string, string> | undefined;
            if (render) expect(archive.get(render.path)).toBeDefined();
        }
    });

    test("a missing media entry is still a hard import error, not a silent partial import", async () => {
        const manifest = await fixture("projects-v4.json");
        const item = manifestProject(manifest);
        const files = item.files as Array<{ path: string }>;
        const archive = await readZip(await createZip([{ name: "projects.json", data: JSON.stringify(manifest) }]));
        expect(files.some((file) => !archive.get(file.path))).toBe(true);
    });
});
