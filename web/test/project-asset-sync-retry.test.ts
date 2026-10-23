import { afterEach, expect, test } from "bun:test";
import localforage from "localforage";

import { CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, createCanvasProjectAssetLinkQueue } from "@/lib/canvas/project-asset-link-queue";
import { localForageStorageForScope } from "@/lib/localforage-storage";
import { apiClient } from "@/services/api/request";
import { ensureCanvasNodeAsset, retryPendingCanvasProjectAssetLinks } from "@/services/project-asset-sync";
import { deleteAssetWithRemoteSync, initializeRemoteUserDataSession, resetRemoteUserDataSync } from "@/services/user-data-sync";
import { useAssetStore } from "@/stores/use-asset-store";
import { useCanvasStore } from "@/stores/canvas/use-canvas-store";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

const originalWindow = globalThis.window;
const originalDocument = globalThis.document;
const originalNavigator = globalThis.navigator;
const originalAdapter = apiClient.defaults.adapter;
const originalGet = localforage.getItem;
const originalSet = localforage.setItem;
const originalRemove = localforage.removeItem;
const indexed = new Map<string, string>();

afterEach(() => {
    resetRemoteUserDataSync();
    useAssetStore.getState().replaceAssets([]);
    useCanvasStore.getState().replaceProjects([]);
    Object.defineProperty(globalThis, "window", { configurable: true, value: originalWindow });
    Object.defineProperty(globalThis, "document", { configurable: true, value: originalDocument });
    Object.defineProperty(globalThis, "navigator", { configurable: true, value: originalNavigator });
    apiClient.defaults.adapter = originalAdapter;
    localforage.getItem = originalGet;
    localforage.setItem = originalSet;
    localforage.removeItem = originalRemove;
    indexed.clear();
});

function installStorage(scope: string, online: { value: boolean }) {
    Object.defineProperty(globalThis, "window", {
        configurable: true,
        value: { localStorage: { getItem: () => scope, setItem: () => undefined }, setTimeout, clearTimeout },
    });
    Object.defineProperty(globalThis, "navigator", { configurable: true, value: { get onLine() { return online.value; } } });
    localforage.getItem = (async (key: string) => indexed.get(key) ?? null) as typeof localforage.getItem;
    localforage.setItem = (async (key: string, value: string) => { indexed.set(key, value); return value; }) as typeof localforage.setItem;
    localforage.removeItem = (async (key: string) => { indexed.delete(key); }) as typeof localforage.removeItem;
}

const node: CanvasNodeData = {
    id: "node", type: CanvasNodeType.Text, title: "Scene notes", position: { x: 0, y: 0 },
    width: 200, height: 100, metadata: { content: "The scene opens at dawn", assetCategory: "material" },
};

test("offline save reports a local asset and resumes the project link when online", async () => {
    const online = { value: false };
    installStorage("owner", online);
    const calls: string[] = [];
    apiClient.defaults.adapter = async (config) => {
        calls.push(`${config.method} ${config.url}`);
        const assetId = useAssetStore.getState().assets[0]?.id || "";
        const projectAsset = { id: assetId, title: "Scene notes", mediaType: "text", category: "material", status: "confirmed", versionCount: 1, usages: [], position: 0, updatedAt: "2026-10-23T00:00:00Z" };
        const data = config.url?.endsWith("/assets") ? { asset: projectAsset } : { asset: { id: assetId } };
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    await initializeRemoteUserDataSession("owner");
    useCanvasStore.getState().createProject("unsynced canvas");

    const saved = await ensureCanvasNodeAsset({ canvasId: "canvas", domainProjectId: "project", node, source: "canvas-manual" });
    expect(saved.created).toBe(true);
    expect(saved.linkedToProject).toBe(false);
    expect(saved.syncPending).toBe(true);
    expect(calls).toEqual([]);
    const queue = createCanvasProjectAssetLinkQueue(localForageStorageForScope("owner"), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, "owner");
    expect((await queue.list()).map((entry) => entry.assetId)).toEqual([saved.assetId]);

    online.value = true;
    await retryPendingCanvasProjectAssetLinks("owner");
    expect(calls.some((call) => call === "post /projects/project/assets")).toBe(true);
    expect(calls.some((call) => call.includes("/canvas-projects/"))).toBe(false);
    expect(await queue.list()).toEqual([]);
    expect(useAssetStore.getState().assets[0]?.metadata?.projectIds).toEqual(["project"]);
});

test("a missing project is blocked instead of retried on every reconnect", async () => {
    installStorage("owner-missing", { value: true });
    let linkAttempts = 0;
    apiClient.defaults.adapter = async (config) => {
        const missing = config.method === "post" && config.url === "/projects/missing/assets";
        if (missing) linkAttempts += 1;
        return {
            data: missing
                ? { code: 404, data: null, msg: "项目不存在", reason: "project_not_found" }
                : { code: 0, data: { asset: { id: useAssetStore.getState().assets[0]?.id } }, msg: "ok" },
            status: missing ? 404 : 200, statusText: missing ? "Not Found" : "OK", headers: {}, config,
        };
    };
    await initializeRemoteUserDataSession("owner-missing");

    await expect(ensureCanvasNodeAsset({ canvasId: "canvas", domainProjectId: "missing", node, source: "canvas-manual" })).rejects.toThrow("项目不存在");
    const queue = createCanvasProjectAssetLinkQueue(localForageStorageForScope("owner-missing"), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, "owner-missing");
    expect((await queue.list())[0]?.nextAttemptAt).toBe(Number.MAX_SAFE_INTEGER);
    await retryPendingCanvasProjectAssetLinks("owner-missing");
    expect(linkAttempts).toBe(1);
});

test("unavailable durable queue storage leaves the local asset and reports the failed handoff", async () => {
    installStorage("owner-no-idb", { value: false });
    await initializeRemoteUserDataSession("owner-no-idb");
    await Promise.resolve();
    Object.defineProperty(globalThis, "document", { configurable: true, value: {} });

    await expect(ensureCanvasNodeAsset({ canvasId: "canvas", domainProjectId: "project", node, source: "canvas-manual" }))
        .rejects.toThrow("项目关联待办未能持久化");
    expect(useAssetStore.getState().assets).toHaveLength(1);
});

test("cancellation keeps the persisted intent without scheduling a failed retry", async () => {
    installStorage("owner-abort", { value: true });
    const controller = new AbortController();
    apiClient.defaults.adapter = async (config) => {
        if (config.method === "post" && config.url === "/projects/project/assets") {
            controller.abort();
            throw new DOMException("Request canceled", "AbortError");
        }
        return { data: { code: 0, data: { asset: { id: useAssetStore.getState().assets[0]?.id } }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    await initializeRemoteUserDataSession("owner-abort");

    await expect(ensureCanvasNodeAsset({ canvasId: "canvas", domainProjectId: "project", node, source: "canvas-manual", signal: controller.signal }))
        .rejects.toMatchObject({ name: "AbortError" });
    const queue = createCanvasProjectAssetLinkQueue(localForageStorageForScope("owner-abort"), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, "owner-abort");
    expect((await queue.list())[0]).toMatchObject({ attempts: 0, nextAttemptAt: 0 });
});

test("deleting the saved asset also removes its pending project link", async () => {
    installStorage("owner-delete", { value: false });
    apiClient.defaults.adapter = async (config) => ({
        data: { code: 0, data: { ids: [useAssetStore.getState().assets[0]?.id] }, msg: "ok" },
        status: 200, statusText: "OK", headers: {}, config,
    });
    await initializeRemoteUserDataSession("owner-delete");
    const saved = await ensureCanvasNodeAsset({ canvasId: "canvas", domainProjectId: "project", node, source: "canvas-manual" });
    const queue = createCanvasProjectAssetLinkQueue(localForageStorageForScope("owner-delete"), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, "owner-delete");
    expect(await queue.list()).toHaveLength(1);

    await deleteAssetWithRemoteSync(saved.assetId);

    expect(await queue.list()).toEqual([]);
    expect(useAssetStore.getState().assets).toEqual([]);
});

test("a 401 waits for login and then uploads the asset before linking", async () => {
    installStorage("owner-login", { value: true });
    let loggedIn = false;
    const calls: string[] = [];
    apiClient.defaults.adapter = async (config) => {
        calls.push(`${config.method} ${config.url}`);
        if (!loggedIn) return { data: { code: 401, data: null, msg: "请登录" }, status: 401, statusText: "Unauthorized", headers: {}, config };
        const assetId = useAssetStore.getState().assets[0]?.id || "";
        const asset = { id: assetId, title: "Scene notes", mediaType: "text", category: "material", status: "confirmed", versionCount: 1, usages: [], position: 0, updatedAt: "2026-10-23T00:00:00Z" };
        return { data: { code: 0, data: { asset }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    await initializeRemoteUserDataSession("owner-login");
    const saved = await ensureCanvasNodeAsset({ canvasId: "canvas", domainProjectId: "project", node, source: "canvas-manual" });
    const queue = createCanvasProjectAssetLinkQueue(localForageStorageForScope("owner-login"), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, "owner-login");
    expect(saved.syncPending).toBe(true);
    expect((await queue.list())[0]?.blockedReason).toBe("auth");
    expect(calls.filter((call) => call === "post /projects/project/assets")).toHaveLength(0);

    loggedIn = true;
    await initializeRemoteUserDataSession("owner-login");
    for (let attempt = 0; attempt < 20 && (await queue.list()).length; attempt += 1) await new Promise((resolve) => setTimeout(resolve, 5));
    expect(await queue.list()).toEqual([]);
    expect(calls.indexOf(`put /assets/${saved.assetId}`)).toBeLessThan(calls.indexOf("post /projects/project/assets"));
});
