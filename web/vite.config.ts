import { dirname, resolve } from "node:path";
import { readFileSync, realpathSync } from "node:fs";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig, searchForWorkspaceRoot } from "vite";
import { formatBuildVersion } from "./src/lib/app-version";

const webDir = dirname(fileURLToPath(import.meta.url));
const sourceVersion = process.env.CANVAS_BUILD_VERSION?.trim() || readFileSync(resolve(webDir, "../VERSION"), "utf8").trim();
const buildCommit = process.env.CANVAS_BUILD_COMMIT?.trim() || process.env.VITE_BUILD_COMMIT?.trim() || "unknown";
const appVersion = formatBuildVersion(sourceVersion, buildCommit);
const buildTime = process.env.CANVAS_BUILD_TIME?.trim() || process.env.VITE_BUILD_TIME?.trim() || "unknown";
const appChangelog = readFileSync(resolve(webDir, "../CHANGELOG.md"), "utf8");
const apiProxyTarget = process.env.VITE_API_PROXY_TARGET?.trim() || "http://127.0.0.1:8080";

export default defineConfig({
    plugins: [react()],
    define: {
        __APP_VERSION__: JSON.stringify(appVersion),
        __APP_CHANGELOG__: JSON.stringify(appChangelog),
        "import.meta.env.VITE_APP_VERSION": JSON.stringify(appVersion),
        "import.meta.env.VITE_BUILD_COMMIT": JSON.stringify(buildCommit),
        "import.meta.env.VITE_BUILD_TIME": JSON.stringify(buildTime),
    },
    server: {
        // A worktree may share installed dependencies through a junction.
        // Keep Vite's workspace boundary and permit only that dependency tree.
        fs: { allow: [searchForWorkspaceRoot(webDir), realpathSync(resolve(webDir, "node_modules"))] },
        proxy: {
            "^/api/canvas-projects/[^/]+/realtime$": {
                target: apiProxyTarget,
                changeOrigin: true,
                xfwd: true,
                ws: true,
                configure(proxy) {
                    proxy.on("proxyReqWs", (proxyRequest, request) => {
                        // The WS proxy's xfwd path omits the original host.
                        // Keep its external port for the backend Origin check.
                        if (request.headers.host) proxyRequest.setHeader("X-Forwarded-Host", request.headers.host);
                        proxyRequest.setHeader("X-Forwarded-Proto", "encrypted" in request.socket && request.socket.encrypted ? "https" : "http");
                    });
                },
            },
            "/api": {
                target: apiProxyTarget,
                changeOrigin: true,
                xfwd: true,
            },
            "/oauth/linuxdo/callback": {
                target: apiProxyTarget,
                changeOrigin: true,
                xfwd: true,
            },
        },
    },
    resolve: {
        alias: {
            "@": resolve(webDir, "src"),
            // 导演台 host bridge 单一实现:web 直接引用 director-desk 源码(契约镜像由漂移守卫测试守护)。
            "@desk": resolve(webDir, "../director-desk/src"),
        },
    },
    build: {
        rolldownOptions: {
            output: {
                strictExecutionOrder: true,
                codeSplitting: {
                    // Keep route-level lazy imports isolated. Recursively merging dependencies
                    // pulls unrelated pages into the initial modulepreload graph.
                    includeDependenciesRecursively: false,
                    minSize: 40 * 1024,
                    groups: [
                        {
                            // Shared interop helpers must not be emitted into a route entry:
                            // AntD would import that entry back and execute its bootstrap early.
                            name: "vendor-babel-runtime",
                            minSize: 0,
                            test: /node_modules[\\/]@babel[\\/]runtime[\\/]/,
                            priority: 40,
                        },
                        {
                            name: "vendor-react",
                            test: /node_modules[\\/](?:react(?:-dom|-router|-router-dom)?|scheduler|zustand|use-sync-external-store|@tanstack[\\/](?:query-core|react-query))[\\/]/,
                            priority: 30,
                        },
                        {
                            name: "vendor-icons",
                            test: /node_modules[\\/](?:lucide-react|@ant-design[\\/]icons)[\\/]/,
                            priority: 20,
                            entriesAware: true,
                            entriesAwareMergeThreshold: 48 * 1024,
                        },
                        {
                            name: "vendor-antd",
                            test: /node_modules[\\/](?:antd|@ant-design|@rc-component|rc-[^\\/]+|dayjs)[\\/]/,
                            priority: 10,
                            entriesAware: true,
                            entriesAwareMergeThreshold: 80 * 1024,
                        },
                    ],
                },
            },
        },
    },
});
