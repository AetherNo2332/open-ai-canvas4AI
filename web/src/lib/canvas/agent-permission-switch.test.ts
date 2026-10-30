import assert from "node:assert/strict";
import test from "node:test";

// @ts-expect-error -- Node 原生 TypeScript 测试运行器需要保留扩展名。
import { agentPermissionChangedText, agentPermissionTargetRun, switchAgentPermission } from "./agent-permission-switch.ts";

function deps(change: (runId: string, input: { permissionMode: string; requestId: string }) => Promise<{ permissionMode: string; changed: boolean }>) {
    const log = { modes: [] as string[], calls: [] as Array<[string, string]>, applied: 0, failed: [] as unknown[] };
    return {
        log,
        value: {
            change: async (runId: string, input: { permissionMode: string; requestId: string }) => {
                log.calls.push([runId, input.permissionMode]);
                return change(runId, input);
            },
            setMode: (mode: string) => log.modes.push(mode),
            requestId: () => "req-1",
            onApplied: () => log.applied++,
            onFailed: (cause: unknown) => log.failed.push(cause),
        },
    };
}

test("switching during an active run on the canvas calls the endpoint and reports it applies to this run", async () => {
    const { log, value } = deps(async (_id, input) => ({ permissionMode: input.permissionMode, changed: true }));
    await switchAgentPermission("auto", "read_only", { id: "run-1", status: "running" }, true, value as never);
    assert.deepEqual(log.calls, [["run-1", "auto"]]);
    assert.deepEqual(log.modes, ["auto", "auto"]);
    assert.equal(log.applied, 1);
});

test("a failed switch reverts the control and surfaces the error", async () => {
    const failure = Object.assign(new Error("本轮已经结束"), { reason: "agent_run_finished" });
    const { log, value } = deps(async () => {
        throw failure;
    });
    await switchAgentPermission("read_only", "auto", { id: "run-1", status: "waiting_approval" }, true, value as never);
    assert.deepEqual(log.modes, ["read_only", "auto"]);
    assert.deepEqual(log.failed, [failure]);
    assert.equal(log.applied, 0);
});

test("no active run, a finished run or the off-canvas panel never calls the endpoint", async () => {
    for (const [run, onCanvas] of [
        [null, true],
        [{ id: "run-1", status: "completed" }, true],
        [{ id: "run-1", status: "running" }, false],
    ] as const) {
        const { log, value } = deps(async () => ({ permissionMode: "auto", changed: true }));
        await switchAgentPermission("auto", "read_only", run as never, onCanvas, value as never);
        assert.deepEqual(log.calls, []);
        assert.deepEqual(log.modes, ["auto"]);
    }
    assert.equal(agentPermissionTargetRun({ id: "run-1", status: "queued" }, true), "run-1");
});

test("permission_changed timeline text names both modes", () => {
    assert.equal(agentPermissionChangedText("read_only", "auto"), "执行权限已从「只读」改为「自动执行」，从下一次工具调用起生效");
});
