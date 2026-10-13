export type AppRuntimeMode = "cloud" | "desktop";

// 双运行时构建标志（vite.config.ts 的 define 注入 __APP_RUNTIME__，与 __APP_VERSION__ 同一机制，
// 构建期确定、无运行时歧义）：
// - 云部署（根 Dockerfile / compose 镜像）传 VITE_APP_RUNTIME=cloud，
//   浏览器打开时恢复登录页 + /api/auth/session 云会话引导；
// - 桌面端构建（desktop/build-windows.ps1 的 bun build，不传该标志）默认 desktop，
//   保持本地 sidecar 的 /api/desktop/bootstrap 引导原行为，零改动。
export function resolveAppRuntimeMode(raw: string | undefined): AppRuntimeMode {
    return raw === "cloud" ? "cloud" : "desktop";
}

export function currentAppRuntimeMode(): AppRuntimeMode {
    // 测试环境未注入该构建全局：typeof 守卫读作 undefined → desktop。
    const raw = typeof __APP_RUNTIME__ === "string" ? __APP_RUNTIME__ : undefined;
    return resolveAppRuntimeMode(raw);
}