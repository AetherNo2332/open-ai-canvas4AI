import { useEffect, useState, type ReactNode } from "react";
import type { AuthSessionPayload } from "@/services/api/auth";
import { FullScreenLoader } from "@/components/ui/aceternity/full-screen-loader";

// 云运行时的会话引导:云后端没有桌面 sidecar 的 /api/desktop/bootstrap 本地实例合同,
// 直接读 /api/auth/session(匿名返回 user:null 的 guest 会话,登录页随后由路由守卫挂载)。
export type CloudSessionBootDeps = {
    fetchSession: (signal: AbortSignal) => Promise<unknown>;
    applySession: (payload: unknown) => Promise<void>;
};

export async function runCloudSessionBoot(deps: CloudSessionBootDeps, signal: AbortSignal): Promise<void> {
    const payload = await deps.fetchSession(signal);
    signal.throwIfAborted();
    await deps.applySession(payload);
    signal.throwIfAborted();
}

export function CloudSessionHydrator({ children }: { children: ReactNode }) {
    const [ready, setReady] = useState(false);
    const [error, setError] = useState("");
    const [retry, setRetry] = useState(0);

    useEffect(() => {
        const abort = new AbortController();
        setReady(false);
        setError("");
        void (async () => {
            try {
                const [{ getAuthSession }, { applyUserSession }] = await Promise.all([
                    import("@/services/api/auth"),
                    import("@/lib/user-session"),
                ]);
                await runCloudSessionBoot(
                    {
                        fetchSession: (signal) =>
                            new Promise<AuthSessionPayload>((resolve, reject) => {
                                if (signal.aborted) {
                                    reject(new DOMException("aborted", "AbortError"));
                                    return;
                                }
                                signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
                                getAuthSession().then(
                                    (payload) => {
                                        if (!signal.aborted) resolve(payload);
                                    },
                                    (err) => reject(err),
                                );
                            }),
                        applySession: (payload) => applyUserSession(payload as AuthSessionPayload, { awaitHydration: true }),
                    },
                    abort.signal,
                );
                if (!abort.signal.aborted) setReady(true);
            } catch (err) {
                if (abort.signal.aborted || (err instanceof DOMException && err.name === "AbortError")) return;
                setError(err instanceof Error ? err.message : "无法连接服务");
            }
        })();
        return () => abort.abort();
    }, [retry]);

    if (error) {
        return (
            <div className="flex h-dvh items-center justify-center">
                <div className="space-y-4">
                    <p>获取登录状态失败：{error}</p>
                    <button className="rounded border px-4 py-2" onClick={() => setRetry((value) => value + 1)}>
                        重新连接
                    </button>
                </div>
            </div>
        );
    }
    return ready ? children : <FullScreenLoader label="正在连接服务" detail="获取登录状态与功能配置" />;
}