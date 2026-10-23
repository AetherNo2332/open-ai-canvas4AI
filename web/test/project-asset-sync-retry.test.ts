import { afterEach, expect, test } from "bun:test";
import localforage from "localforage";
import type { InternalAxiosRequestConfig } from "axios";

import { CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, createCanvasProjectAssetLinkQueue } from "@/lib/canvas/project-asset-link-queue";
import { localForageStorageForScope } from "@/lib/localforage-storage";
import { apiClient } from "@/services/api/request";
import { createCanvasProjectAssetLinkRetryRunner, ensureCanvasNodeAsset, retryPendingCanvasProjectAssetLinks } from "@/services/project-asset-sync";
import { deleteAssetWithRemoteSync, initializeRemoteUserDataSession, resetRemoteUserDataSync } from "@/services/user-data-sync";
import { useAssetStore, type Asset } from "@/stores/use-asset-store";
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
const cloudAssets = new Map<string, Asset>();

function assetAPIResponse(config: InternalAxiosRequestConfig) {
    const match = config.url?.match(/^\/assets\/([^/]+)$/);
    if (!match) return;
    const id = decodeURIComponent(match[1]);
    if (config.method === "put") cloudAssets.set(id, JSON.parse(config.data).asset);
    const asset = cloudAssets.get(id);
    return { data: asset ? { code: 0, data: { asset }, msg: "ok" } : { code: 404, data: null, msg: "素材不存在" }, status: asset ? 200 : 404, statusText: asset ? "OK" : "Not Found", headers: {}, config };
}

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
    cloudAssets.clear();
});

function installStorage(scope: string, online: { value: boolean }) {
    Object.defineProperty(globalThis, "window", {
        configurable: true,
        value: { localStorage: { getItem: () => scope, setItem: () => undefined }, setTimeout, clearTimeout, addEventListener: () => undefined },
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
        const entityResponse = assetAPIResponse(config);
        if (entityResponse) return entityResponse;
        const assetId = useAssetStore.getState().assets[0]?.id || "";
        const projectAsset = { id: assetId, title: "Scene notes", mediaType: "text", category: "material", status: "confirmed", versionCount: 1, usages: [], position: 0, updatedAt: "2026-10-23T00:00:00Z" };
        const data = { projectRevision: config.method === "get" ? 1 : 2, asset: projectAsset };
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
    expect(calls.some((call) => call === `put /projects/project/assets/${saved.assetId}/sync`)).toBe(true);
    expect(calls.some((call) => call.includes("/canvas-projects/"))).toBe(false);
    expect(await queue.list()).toEqual([]);
    expect(useAssetStore.getState().assets[0]?.metadata?.projectIds).toEqual(["project"]);
});

test("a missing project is blocked instead of retried on every reconnect", async () => {
    installStorage("owner-missing", { value: true });
    let linkAttempts = 0;
    apiClient.defaults.adapter = async (config) => {
        const entityResponse = assetAPIResponse(config);
        if (entityResponse) return entityResponse;
        const missing = config.url?.startsWith("/projects/missing/assets/");
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
    expect((await queue.list())[0]?.blockedReason).toBe("permanent");
    expect((await queue.list())[0]?.blockedMessage).toBe("项目不存在");
    expect((await queue.list())[0]?.createAssetIfMissing).toBe(false);
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
        const entityResponse = assetAPIResponse(config);
        if (entityResponse) return entityResponse;
        if (config.method === "put" && config.url?.endsWith("/sync")) {
            controller.abort();
            throw new DOMException("Request canceled", "AbortError");
        }
        return { data: { code: 0, data: { projectRevision: 1, asset: null }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
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
        const entityResponse = assetAPIResponse(config);
        if (entityResponse) return entityResponse;
        const assetId = useAssetStore.getState().assets[0]?.id || "";
        const asset = { id: assetId, title: "Scene notes", mediaType: "text", category: "material", status: "confirmed", versionCount: 1, usages: [], position: 0, updatedAt: "2026-10-23T00:00:00Z" };
        return { data: { code: 0, data: { projectRevision: config.method === "get" ? 1 : 2, asset }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    await initializeRemoteUserDataSession("owner-login");
    const saved = await ensureCanvasNodeAsset({ canvasId: "canvas", domainProjectId: "project", node, source: "canvas-manual" });
    const queue = createCanvasProjectAssetLinkQueue(localForageStorageForScope("owner-login"), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, "owner-login");
    expect(saved.syncPending).toBe(true);
    expect((await queue.list())[0]?.blockedReason).toBe("auth");
    expect(calls.filter((call) => call.endsWith("/sync"))).toHaveLength(0);

    loggedIn = true;
    await initializeRemoteUserDataSession("owner-login");
    for (let attempt = 0; attempt < 20 && (await queue.list()).length; attempt += 1) await new Promise((resolve) => setTimeout(resolve, 5));
    expect(await queue.list()).toEqual([]);
    expect(calls.indexOf(`put /assets/${saved.assetId}`)).toBeLessThan(calls.indexOf(`put /projects/project/assets/${saved.assetId}/sync`));
});

test("saving the same blocked intent explicitly retries it after the target is repaired", async () => {
    installStorage("owner-manual-retry", { value: true });
    let repaired = false;
    let attempts = 0;
    apiClient.defaults.adapter = async (config) => {
        const entityResponse = assetAPIResponse(config);
        if (entityResponse) return entityResponse;
        if (!repaired) {
            attempts += 1;
            return { data: { code: 404, data: null, msg: "目标暂不可用" }, status: 404, statusText: "Not Found", headers: {}, config };
        }
        return { data: { code: 0, data: { projectRevision: config.method === "get" ? 1 : 2, asset: null }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    await initializeRemoteUserDataSession("owner-manual-retry");
    const save = () => ensureCanvasNodeAsset({ canvasId: "canvas", domainProjectId: "project", node, source: "canvas-manual" });
    await expect(save()).rejects.toThrow("再次点击保存可重试");
    const queue = createCanvasProjectAssetLinkQueue(localForageStorageForScope("owner-manual-retry"), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, "owner-manual-retry");
    const blocked = (await queue.list())[0];
    await retryPendingCanvasProjectAssetLinks("owner-manual-retry");
    expect(attempts).toBe(1);
    repaired = true;

    const saved = await save();

    expect(saved.linkedToProject).toBe(true);
    expect(saved.syncPending).toBe(false);
    expect(await queue.list()).toEqual([]);
    expect(blocked.blockedReason).toBe("permanent");
});

test("an existing pending asset deleted in the cloud is not recreated by recovery", async () => {
    const online = { value: false };
    installStorage("owner-cloud-deleted", online);
    const asset: Asset = { id: "deleted", kind: "text", title: "Cached text", coverUrl: "", tags: [], category: "material", data: { content: "Cached content" }, createdAt: "2026-10-23T00:00:00Z", updatedAt: "2026-10-23T00:00:00Z" };
    useAssetStore.getState().replaceAssets([asset]);
    const queue = createCanvasProjectAssetLinkQueue(localForageStorageForScope("owner-cloud-deleted"), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, "owner-cloud-deleted");
    await queue.upsert({ assetId: asset.id, domainProjectId: "project", folderId: "new" });
    const calls: string[] = [];
    apiClient.defaults.adapter = async (config) => {
        calls.push(`${config.method} ${config.url}`);
        return assetAPIResponse(config)!;
    };
    await initializeRemoteUserDataSession("owner-cloud-deleted");
    online.value = true;

    await retryPendingCanvasProjectAssetLinks("owner-cloud-deleted");

    expect(calls).toEqual(["get /assets/deleted"]);
    expect(cloudAssets.has(asset.id)).toBe(false);
    expect((await queue.list())[0]?.blockedReason).toBe("permanent");
    expect(useAssetStore.getState().assets[0]?.data).toEqual(asset.data);
});

test("two executors cannot let an older in-flight relation overwrite a newer completed intent", async () => {
    const online = { value: false };
    installStorage("owner-two-tabs", online);
    const asset: Asset = { id: "shared", kind: "text", title: "Cloud text", coverUrl: "", tags: [], category: "material", data: { content: "Cloud content" }, metadata: { projectIds: ["project"] }, createdAt: "2026-10-23T00:00:00Z", updatedAt: "2026-10-23T00:00:00Z" };
    useAssetStore.getState().replaceAssets([asset]);
    cloudAssets.set(asset.id, asset);
    await initializeRemoteUserDataSession("owner-two-tabs");
    const queue = createCanvasProjectAssetLinkQueue(localForageStorageForScope("owner-two-tabs"), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, "owner-two-tabs");
    await queue.upsert({ assetId: asset.id, domainProjectId: "project", folderId: "old" });
    let releaseOld!: () => void;
    let startedOld!: () => void;
    const oldStarted = new Promise<void>((resolve) => { startedOld = resolve; });
    const oldGate = new Promise<void>((resolve) => { releaseOld = resolve; });
    let serverFolder = "initial";
    let revision = 1;
    let staleRequestsRejected = 0;
    apiClient.defaults.adapter = async (config) => {
        const entityResponse = assetAPIResponse(config);
        if (entityResponse) return entityResponse;
        const input = config.data ? JSON.parse(config.data) : {};
        const summary = { id: asset.id, title: asset.title, mediaType: "text", category: "material", status: "confirmed", folderId: serverFolder, versionCount: 1, usages: [], position: 0, updatedAt: asset.updatedAt };
        if (config.method === "get") return { data: { code: 0, data: { projectRevision: revision, asset: summary }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
        if (input.folderId === "old") { startedOld(); await oldGate; }
        if (config.url?.endsWith("/sync") && input.expectedProjectRevision !== revision) {
            staleRequestsRejected += 1;
            return { data: { code: 409, data: null, reason: "project_asset_sync_conflict", retryable: true, msg: "关系版本已变化" }, status: 409, statusText: "Conflict", headers: {}, config };
        }
        serverFolder = input.folderId;
        revision += 1;
        return { data: { code: 0, data: { projectRevision: revision, asset: { ...summary, folderId: serverFolder } }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    // The real executor factory gives this page a separate flight map, as another tab has.
    const secondTabRetry = createCanvasProjectAssetLinkRetryRunner();
    online.value = true;
    const firstFlight = retryPendingCanvasProjectAssetLinks("owner-two-tabs");
    await oldStarted;
    await queue.upsert({ assetId: asset.id, domainProjectId: "project", folderId: "new" });
    await secondTabRetry("owner-two-tabs");
    expect(serverFolder).toBe("new");
    releaseOld();
    await firstFlight;

    expect(serverFolder).toBe("new");
    expect(staleRequestsRejected).toBe(1);
    expect(await queue.list()).toEqual([]);
});
