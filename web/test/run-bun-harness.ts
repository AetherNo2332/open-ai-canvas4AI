import { resolve } from "node:path";

// bun 1.3 的 Bun.spawnSync({ timeout }) 计时器使用缓存的同步循环时钟，父进程空闲后会
// 立即误杀子进程并返回 null 退出码（oven-sh/bun#33932，bun 1.4.0 才修复）。
// 此包装改用异步 spawn 加手动计时器，保持同样的子进程隔离与超时上限。
export async function runBunHarness(args: string[], timeoutMs: number): Promise<{ exitCode: number | null; output: string }> {
    const child = Bun.spawn([process.execPath, ...args], { cwd: resolve(import.meta.dir, ".."), stdout: "pipe", stderr: "pipe" });
    let timedOut = false;
    const timer = setTimeout(() => {
        timedOut = true;
        child.kill();
    }, timeoutMs);
    const exitCode: number | null = await child.exited;
    clearTimeout(timer);
    const output = (await new Response(child.stdout).text()) + (await new Response(child.stderr).text());
    return { exitCode, output: timedOut ? `${output}\n[harness exceeded ${timeoutMs}ms]` : output };
}
