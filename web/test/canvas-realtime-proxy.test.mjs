import { expect, test } from "bun:test";
import { fileURLToPath } from "node:url";

// Vite's HTTP/WebSocket proxy runs under Node in development. Keep its actual
// upgrade/close lifecycle isolated from Bun's node:test compatibility layer.
test("real Node Vite proxy preserves realtime upgrade, credentials and SSE", () => {
    const result = Bun.spawnSync(["node", "--test", fileURLToPath(new URL("./canvas-realtime-proxy.node.mjs", import.meta.url))], { timeout: 30_000 });
    const output = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
    expect(result.exitCode, output).toBe(0);
}, 35_000);
