import { afterEach, expect, test } from "bun:test";
import localforage from "localforage";

import { CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, createCanvasProjectAssetLinkQueue } from "@/lib/canvas/project-asset-link-queue";
import { localForageStorageForScope } from "@/lib/localforage-storage";
import { apiClient } from "@/services/api/request";
import { resetRemoteUserDataSync, syncRemoteUserData } from "@/services/user-data-sync";
import { useAssetStore, type Asset } from "@/stores/use-asset-store";

const originalWindow = globalThis.window;
const originalAdapter = apiClient.defaults.adapter;
const originalGet = localforage.getItem;
const originalSet = localforage.setItem;
const originalRemove = localforage.removeItem;
const indexed = new Map<string, string>();

afterEach(() => {
    resetRemoteUserDataSync();
    useAssetStore.getState().replaceAssets([]);
    Object.defineProperty(globalThis, "window", { configurable: true, value: originalWindow });
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
