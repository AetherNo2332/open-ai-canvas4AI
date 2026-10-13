import "./setup-build-globals";
import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { resolveAppRuntimeMode, currentAppRuntimeMode } from "../src/lib/app-runtime-mode";
import { CloudSessionHydrator, runCloudSessionBoot } from "../src/components/auth/cloud-session-hydrator";
import { RequireAuth } from "../src/components/auth/require-auth";
import { router } from "../src/router";

// 双运行时构建:云镜像(Dockerfile 传 VITE_APP_RUNTIME=cloud)必须恢复浏览器可用形态
// (登录页 + 会话引导),桌面构建(无标志)保持本地 sidecar 引导原行为。

describe("app runtime mode(build 标志)", () => {
    test("cloud 标志返回 cloud,其余值保持 desktop(默认不改变桌面行为)", () => {
        expect(resolveAppRuntimeMode("cloud")).toBe("cloud");
        expect(resolveAppRuntimeMode("desktop")).toBe("desktop");
        expect(resolveAppRuntimeMode(undefined)).toBe("desktop");
        expect(resolveAppRuntimeMode("")).toBe("desktop");
        expect(resolveAppRuntimeMode("CLOUD")).toBe("desktop");
        expect(resolveAppRuntimeMode("cloud ")).toBe("desktop");
    });

    test("无构建标志的测试环境读取为 desktop", () => {
        expect(currentAppRuntimeMode()).toBe("desktop");
    });
});

describe("cloud 路由合同", () => {
    test("登录/注册/忘记密码为公共路由(位于工作区守卫之外)", () => {
        const paths = router.routes.map((route) => route.path).filter(Boolean);
        for (const path of ["/login", "/register", "/forgot-password"]) {
            expect(paths).toContain(path);
        }
    });

    test("工作区路由(画布等)必须被认证守卫包裹", () => {
        const workspace = router.routes.find(
            (route) => Array.isArray(route.children) && route.children.some((child) => child.path === "/canvas"),
        );
        expect(workspace).toBeTruthy();
        expect(workspace?.element?.type).toBe(RequireAuth);
    });
});

describe("RequireAuth(守卫)", () => {
    test("会话未 hydrate 前不暴露工作区(加载态)", () => {
        const html = renderToStaticMarkup(
            <MemoryRouter>
                <RequireAuth>
                    <div>workspace-content</div>
                </RequireAuth>
            </MemoryRouter>,
        );
        expect(html).not.toContain("workspace-content");
        expect(html).toContain("data-full-screen-loader");
    });
});

describe("CloudSessionHydrator(云会话引导)", () => {
    test("初始渲染为引导加载(不渲染子树、不渲染错误)", () => {
        const html = renderToStaticMarkup(
            <CloudSessionHydrator>
                <div id="cloud-child">child-content</div>
            </CloudSessionHydrator>,
        );
        expect(html).toContain("正在连接服务");
        expect(html).not.toContain("child-content");
        expect(html).not.toContain("重新连接");
    });

    test("boot:成功时把会话负载交给 applyUserSession", async () => {
        const applied: unknown[] = [];
        const signal = new AbortController().signal;
        await runCloudSessionBoot(
            {
                fetchSession: async () => ({ user: null }),
                applySession: async (payload) => {
                    applied.push(payload);
                },
            },
            signal,
        );
        expect(applied).toEqual([{ user: null }]);
    });

    test("boot:请求失败时向上抛出(由组件转为错误屏)", async () => {
        await expect(
            runCloudSessionBoot(
                {
                    fetchSession: async () => {
                        throw new Error("请先登录");
                    },
                    applySession: async () => undefined,
                },
                new AbortController().signal,
            ),
        ).rejects.toThrow("请先登录");
    });

    test("boot:已中止时不再应用会话", async () => {
        const controller = new AbortController();
        controller.abort();
        let applied = false;
        await expect(
            runCloudSessionBoot(
                {
                    fetchSession: async (signal) =>
                        new Promise((_resolve, reject) => {
                            if (signal.aborted) {
                                reject(new DOMException("aborted", "AbortError"));
                                return;
                            }
                            signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
                            // 模拟请求就绪后仍未收到响应:中止时只应以错误退出,绝不应用会话。
                        }),
                    applySession: async () => {
                        applied = true;
                    },
                },
                controller.signal,
            ),
        ).rejects.toThrow();
        expect(applied).toBe(false);
    });
});