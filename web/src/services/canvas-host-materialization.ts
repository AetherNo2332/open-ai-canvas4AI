import type { MaterializationStatus } from "../lib/canvas/command-contract.generated";
export async function waitCanvasHostMaterialization(taskId: string, deps: { read: () => Promise<MaterializationStatus>; synchronize: () => Promise<void>; signal?: AbortSignal; delay?: (ms: number) => Promise<void>; now?: () => number }) {
    const now = deps.now || Date.now,
        start = now();
    const delay =
        deps.delay ||
        ((ms: number) =>
            new Promise<void>((resolve, reject) => {
                const finish = () => {
                    deps.signal?.removeEventListener("abort", abort);
                    resolve();
                };
                const timer = setTimeout(finish, ms);
                const abort = () => {
                    clearTimeout(timer);
                    deps.signal?.removeEventListener("abort", abort);
                    reject(new DOMException("画布已关闭", "AbortError"));
                };
                deps.signal?.addEventListener("abort", abort, { once: true });
                if (deps.signal?.aborted) abort();
            }));
    for (;;) {
        deps.signal?.throwIfAborted();
        const status = await deps.read();
        deps.signal?.throwIfAborted();
        if (status.taskId !== taskId) throw new Error("canvas_materialization_scope_mismatch");
        if (status.state === "done") {
            await deps.synchronize();
            return status;
        }
        if (status.message || now() - start >= 120000) throw Object.assign(new Error(status.message || "生成已完成，主机仍在保存结果，可在任务详情中查看写回状态"), { reason: "canvas_materialization_pending" });
        await delay(500);
    }
}
