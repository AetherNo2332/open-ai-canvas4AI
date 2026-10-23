import assert from "node:assert/strict";
import { createServer as createHTTPServer, request } from "node:http";
import { once } from "node:events";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { createServer } from "vite";

test("Vite proxies the real realtime upgrade with browser credentials and preserves normal API/SSE", { timeout: 20000 }, async () => {
    const seen = [];
    const sockets = new Set();
    const backend = createHTTPServer((req, res) => {
        seen.push(req);
        res.setHeader("Content-Type", req.url.endsWith("/events") ? "text/event-stream" : "application/json");
        res.end(req.url.endsWith("/events") ? "data: committed\n\n" : '{"ok":true}');
    });
    backend.on("upgrade", (req, socket) => {
        seen.push(req);
        socket.write("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n");
        socket.on("error", () => {});
    });
    backend.on("connection", (socket) => {
        sockets.add(socket);
        socket.on("close", () => sockets.delete(socket));
    });
    backend.listen(0, "127.0.0.1");
    await once(backend, "listening");
    const previous = process.env.VITE_API_PROXY_TARGET;
    process.env.VITE_API_PROXY_TARGET = `http://127.0.0.1:${backend.address().port}`;
    let vite;
    let upgraded;
    try {
        vite = await createServer({
            root: fileURLToPath(new URL("..", import.meta.url)),
            configFile: fileURLToPath(new URL("../vite.config.ts", import.meta.url)),
            logLevel: "silent",
            optimizeDeps: { noDiscovery: true, include: [] },
            server: { host: "127.0.0.1", port: 0 },
        });
        await vite.listen();
        const port = vite.httpServer.address().port;
        const origin = `http://127.0.0.1:${port}`;
        upgraded = await new Promise((resolve, reject) => {
            const req = request(`${origin}/api/canvas-projects/test/realtime`, { headers: {
                Connection: "Upgrade", Upgrade: "websocket", Origin: origin,
                Cookie: "canvas_session=test.cookie", "Sec-WebSocket-Version": "13",
                "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==",
            }});
            req.setTimeout(2000, () => { req.destroy(new Error("upgrade timeout")); });
            req.on("error", reject);
            req.on("response", (res) => { res.resume(); reject(new Error(`upgrade returned HTTP ${res.statusCode}`)); });
            req.on("upgrade", (res, socket) => { assert.equal(res.statusCode, 101); resolve(socket); });
            req.end();
        });
        const upstream = seen.find((req) => req.url.endsWith("/realtime"));
        assert.equal(upstream.headers.origin, origin);
        assert.equal(upstream.headers.cookie, "canvas_session=test.cookie");
        assert.equal(upstream.headers["x-forwarded-host"], `127.0.0.1:${port}`);
        assert.equal(upstream.headers["x-forwarded-proto"], "http");
        for (const [path, body] of [["/api/canvas-projects", '{"ok":true}'], ["/api/agent/runs/test/events", "data: committed\n\n"]]) {
            const response = await fetch(origin + path);
            assert.equal(response.status, 200);
            assert.equal(await response.text(), body);
            assert.equal(seen.find((req) => req.url === path).headers.upgrade, undefined);
        }
    } finally {
        upgraded?.destroy();
        for (const socket of sockets) socket.destroy();
        vite?.httpServer?.closeAllConnections();
        await vite?.close();
        backend.closeAllConnections();
        await new Promise((resolve) => backend.close(resolve));
        if (previous === undefined) delete process.env.VITE_API_PROXY_TARGET;
        else process.env.VITE_API_PROXY_TARGET = previous;
    }
});

test("Nginx has a narrowly scoped WebSocket location independent of SSE", () => {
    const nginx = readFileSync(fileURLToPath(new URL("../../nginx.conf", import.meta.url)), "utf8");
    const block = nginx.match(/location ~ \^\/api\/canvas-projects\/\[\^\/\]\+\/realtime\$\s*\{([^}]+)\}/)?.[1];
    assert.ok(block, "realtime location missing");
    for (const directive of ["proxy_http_version 1.1;", "proxy_set_header Upgrade $http_upgrade;", 'proxy_set_header Connection "upgrade";', "proxy_set_header X-Forwarded-Host $http_host;", "proxy_set_header X-Forwarded-Proto $canvas_forwarded_proto;", "proxy_buffering off;", "proxy_read_timeout 60s;"]) {
        assert.ok(block.includes(directive), directive);
    }
});
