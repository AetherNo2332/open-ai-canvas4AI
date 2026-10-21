import { afterEach, expect, test } from "bun:test";

import { getAdminOSSSetting, testAdminOSSConnection, updateAdminOSSSetting } from "../src/services/api/auth";
import { apiClient } from "../src/services/api/request";

// 存储服务页（/admin/settings/storage）只能调用云端 cmd/server 注册的 /admin/settings/oss*。
// 曾误用已删除的桌面端 /desktop/settings/storage*，在云端落入 NoRoute 系统代理兜底而 404。
const originalAdapter = apiClient.defaults.adapter;

afterEach(() => {
    apiClient.defaults.adapter = originalAdapter;
});

test("admin OSS settings API uses the cloud /admin/settings/oss routes", async () => {
    const requests: Array<{ method?: string; url?: string }> = [];
    apiClient.defaults.adapter = async (config) => {
        requests.push({ method: config.method, url: config.url });
        return { config, headers: {}, status: 200, statusText: "OK", data: { code: 0, data: { setting: {} }, msg: "ok" } };
    };

    await getAdminOSSSetting();
    await updateAdminOSSSetting({ enabled: false });
    await testAdminOSSConnection({ provider: "aliyun", bucket: "canvas-assets" } as Parameters<typeof testAdminOSSConnection>[0]);

    expect(requests).toEqual([
        { method: "get", url: "/admin/settings/oss" },
        { method: "patch", url: "/admin/settings/oss" },
        { method: "post", url: "/admin/settings/oss/test" },
    ]);
});
