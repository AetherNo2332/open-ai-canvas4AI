import { afterEach, expect, test } from "bun:test";
import localforage from "localforage";

import { CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, createCanvasProjectAssetLinkQueue } from "@/lib/canvas/project-asset-link-queue";
import { localForageStorageForScope } from "@/lib/localforage-storage";
import { apiClient } from "@/services/api/request";
import { initializeRemoteUserDataSession, resetRemoteUserDataSync, saveRemoteUserDataNow, syncRemoteUserData } from "@/services/user-data-sync";
import { useAssetStore, type Asset } from "@/stores/use-asset-store";

const originalWindow = globalThis.window;
const originalNavigator = globalThis.navigator;
const originalAdapter = apiClient.defaults.adapter;
const originalGet = localforage.getItem;
const originalSet = localforage.setItem;
const originalRemove = localforage.removeItem;
const indexed = new Map<string, string>();

afterEach(() => {
    resetRemoteUserDataSync();
    useAssetStore.getState().replaceAssets([]);
    Object.defineProperty(globalThis, "window", { configurable: true, value: originalWindow });
    Object.defineProperty(globalThis, "navigator", { configurable: true, value: originalNavigator });
    apiClient.defaults.adapter = originalAdapter;
    localforage.getItem = originalGet;
    localforage.setItem = originalSet;
    localforage.removeItem = originalRemove;
    indexed.clear();
});

function installSnapshot(scope: string) {
    Object.defineProperty(globalThis, "window", {
        configurable: true,
        value: { localStorage: { getItem: () => scope, setItem: () => undefined }, setTimeout, clearTimeout },
    });
    localforage.getItem = (async (key: string) => indexed.get(key) ?? null) as typeof localforage.getItem;
    localforage.setItem = (async (key: string, value: string) => { indexed.set(key, value); return value; }) as typeof localforage.setItem;
    localforage.removeItem = (async (key: string) => { indexed.delete(key); }) as typeof localforage.removeItem;
    apiClient.defaults.adapter = async (config) => ({
        data: { code: 0, data: { projects: [], assets: [] }, msg: "ok" },
        status: 200, statusText: "OK", headers: {}, config,
    });
}

function localAsset(id: string): Asset {
    return {
        id, kind: "text", title: id, coverUrl: "", tags: [], category: "material",
        createdAt: "2026-10-23T00:00:00Z", updatedAt: "2026-10-23T00:00:00Z",
        data: { content: "local content" },
    };
}

test("login snapshot retains this account's asset while its project link is pending", async () => {
    installSnapshot("owner-a");
    useAssetStore.getState().replaceAssets([localAsset("pending-asset"), localAsset("unrelated")]);
    await createCanvasProjectAssetLinkQueue(localForageStorageForScope("owner-a"), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY)
        .upsert({ assetId: "pending-asset", domainProjectId: "project", category: "material" });

    await syncRemoteUserData("owner-a");

    expect(useAssetStore.getState().assets.map((asset) => asset.id)).toEqual(["pending-asset"]);
});

test("another account's queued asset cannot survive the current account snapshot", async () => {
    installSnapshot("owner-b");
    useAssetStore.getState().replaceAssets([localAsset("foreign-asset")]);
    await createCanvasProjectAssetLinkQueue(localForageStorageForScope("owner-a"), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY)
        .upsert({ assetId: "foreign-asset", domainProjectId: "project" });

    await syncRemoteUserData("owner-b");

    expect(useAssetStore.getState().assets).toEqual([]);
});

test("login recovery of an existing pending asset adopts cloud content instead of overwriting it", async () => {
    installSnapshot("owner-existing");
    const cached = { ...localAsset("asset"), title: "old cached title", updatedAt: "2026-10-21T00:00:00Z" };
    const cloud = { ...localAsset("asset"), title: "new cloud title", data: { content: "new cloud content" }, metadata: { note: "new cloud metadata" } } as Asset;
    useAssetStore.getState().replaceAssets([cached]);
    await createCanvasProjectAssetLinkQueue(localForageStorageForScope("owner-existing"), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY)
        .upsert({ assetId: "asset", domainProjectId: "project", folderId: "new-folder" });
    const calls: string[] = [];
    apiClient.defaults.adapter = async (config) => {
        calls.push(`${config.method} ${config.url}`);
        return { data: { code: 0, data: { asset: cloud }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };

    await initializeRemoteUserDataSession("owner-existing");
    await saveRemoteUserDataNow([]);

    expect(calls).toEqual(["get /assets/asset"]);
    expect(useAssetStore.getState().assets[0]).toEqual(cloud);
});

test("the ordinary save batch uses create-only recovery for an unacknowledged pending asset", async () => {
    const scope = "owner-batch-race";
    installSnapshot(scope);
    Object.defineProperty(globalThis, "navigator", { configurable: true, value: { onLine: false } });
    await initializeRemoteUserDataSession(scope);
    const local = { ...localAsset("batch-asset"), title: "stale local body" };
    const fresh = { ...localAsset("batch-asset"), title: "other device edited", data: { content: "new cloud body" }, metadata: { note: "new cloud metadata" } } as Asset;
    useAssetStore.getState().replaceAssets([local]);
    const queue = createCanvasProjectAssetLinkQueue(localForageStorageForScope(scope), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, scope);
    await queue.upsert({ assetId: local.id, domainProjectId: "project", createAssetIfMissing: true });
    let cloud: Asset | undefined = fresh;
    let firstGet = true;
    let conditionalCreates = 0;
    const calls: string[] = [];
    apiClient.defaults.adapter = async (config) => {
        calls.push(`${config.method} ${config.url}`);
        if (config.method === "get" && firstGet) {
            firstGet = false;
            return { data: { code: 404, data: null, msg: "素材不存在" }, status: 404, statusText: "Not Found", headers: {}, config };
        }
        if (config.method === "put") {
            if (config.headers.get("If-None-Match") === "*") {
                conditionalCreates += 1;
                return { data: { code: 412, data: null, reason: "asset_already_exists", msg: "素材已存在" }, status: 412, statusText: "Precondition Failed", headers: {}, config };
            }
            cloud = JSON.parse(config.data).asset;
        }
        return { data: { code: 0, data: { asset: cloud }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };

    await saveRemoteUserDataNow([]);

    expect(cloud).toEqual(fresh);
    expect(conditionalCreates).toBe(1);
    expect(calls).toEqual(["get /assets/batch-asset", "put /assets/batch-asset", "get /assets/batch-asset"]);
    expect(useAssetStore.getState().assets[0]).toEqual(fresh);
    expect((await queue.list())[0]?.createAssetIfMissing).toBe(false);
});
