import { createServer } from "node:http";
import { createReadStream, createWriteStream } from "node:fs";
import { mkdir, rename, stat, rm } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { Transform } from "node:stream";
import { pipeline } from "node:stream/promises";
import { randomUUID, timingSafeEqual } from "node:crypto";
import { JobStore, maxAssetBytes } from "./jobs.mjs";
import { renderJob } from "./render.mjs";

const here = fileURLToPath(new URL("..", import.meta.url));
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const errorCode = (error) => /^[a-z][a-z0-9_]{1,70}$/.test(error?.message || "") ? error.message : "renderer_internal_error";

async function jsonBody(request) {
    let size = 0; const chunks = [];
    for await (const chunk of request) { size += chunk.length; if (size > 1048576) throw new Error("input_too_large"); chunks.push(chunk); }
    try { return JSON.parse(Buffer.concat(chunks).toString("utf8")); } catch { throw new Error("invalid_json"); }
}

export async function createRendererServer(options) {
    if (!options.token || options.token.length < 32) throw new Error("renderer_token_required");
    const store = await JobStore.open(options.root);
    const render = options.render || renderJob;
    let closing = false; let active = null; let ticking = false;
    async function execute(job) {
        const executionOwner=job.leaseOwner, executionAttempt=job.attempt;
        const currentExecution=()=>job.leaseOwner===executionOwner && job.attempt===executionAttempt;
        const controller = new AbortController();
        store.controllers.set(job.taskId, controller);
        const deadline = setTimeout(() => controller.abort(new Error("render_timeout")), 30 * 60000);
        const progress = (phase, frames) => store.locked(async () => {
            const current = store.owned(job.taskId, executionOwner);
            if (!currentExecution()) throw new Error("lease_lost");
            if (current.status !== "running" || controller.signal.aborted) throw new Error("cancelled");
            current.phase = phase; current.frames = frames; current.lastProgressAt = Date.now();
            await store.save(current);
        });
        try {
            let output;
            while (true) {
                await store.locked(async () => {
                    store.owned(job.taskId, executionOwner);
                    if (!currentExecution()) throw new Error("lease_lost");
                    if (job.status === "cancelled" || controller.signal.aborted) throw new Error("cancelled");
                    if (job.renderAttempts >= 2) throw new Error("render_retry_exhausted");
                    job.status = "running"; job.renderAttempts++; job.frames = 0; job.lastProgressAt = Date.now();
                    await store.save(job);
                });
                try { output = await render(job, store.directory(job.taskId), options, controller.signal, progress); break; }
                catch (error) {
                    if (errorCode(error) !== "renderer_crashed" || job.renderAttempts >= 2 || controller.signal.aborted) throw error;
                    await progress("retrying_renderer", 0);
                }
            }
            await store.locked(async () => {
                store.owned(job.taskId, executionOwner);
                if (!currentExecution()) throw new Error("lease_lost");
                if (job.status !== "running" || controller.signal.aborted) throw new Error("cancelled");
                job.output = output; job.status = "output_ready"; job.phase = "output_ready"; job.code = "";
                await store.save(job);
            });
        } catch (error) {
            if(process.env.PREVIS_RENDER_DIAGNOSTICS) console.error("renderer execution diagnostic",String(error?.message || error).slice(0,300));
            await store.locked(async () => {
                if (!currentExecution() || job.status === "cancelled") return;
                job.status = "failed"; job.phase = "failed"; job.code = errorCode(controller.signal.reason || error);
                await store.save(job);
            });
        } finally { clearTimeout(deadline); if(store.controllers.get(job.taskId)===controller) store.controllers.delete(job.taskId); }
    }
    const tick = async () => {
        if (closing || ticking) return;
        ticking = true;
        try {
            await store.expire();
            if (!active) {
                const next = [...store.jobs.values()].find((job) => job.status === "queued");
                if (next) active = execute(next).catch(() => { console.error("previs job checkpoint failed"); }).finally(() => { active = null; });
            }
        } finally { ticking = false; }
    };
    const timer = setInterval(() => tick().catch(() => undefined), 200);
    const json = (response, status, value) => { response.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store" }); response.end(JSON.stringify(value)); };
    const server = createServer(async (request, response) => {
        try {
            const path = new URL(request.url, "http://renderer").pathname;
            if (request.method === "GET" && path === "/health") { json(response, 200, { ready: !closing }); return; }
            const expected = Buffer.from(`Bearer ${options.token}`);
            const actual = Buffer.from(request.headers.authorization || "");
            if (actual.length !== expected.length || !timingSafeEqual(actual, expected)) { json(response, 401, { code: "unauthorized" }); return; }
            if (closing) { json(response, 503, { code: "renderer_draining" }); return; }
            if (request.method === "POST" && path === "/jobs") { json(response, 200, await store.submit(await jsonBody(request))); return; }
            const match = /^\/jobs\/([A-Za-z0-9_-]{1,80})(?:\/(renew|start|cancel|ack|video|preview|assets\/([A-Za-z0-9_-]{1,80})))?$/.exec(path);
            if (!match) { json(response, 404, { code: "job_not_found" }); return; }
            const [, id, action, asset] = match;
            const owner = String(request.headers["x-previs-lease-owner"] || "");
            if (request.method === "POST" && action === "ack") { json(response, 200, await store.acknowledge(id,owner)); return; }
            const job = store.jobs.get(id);
            if (!job) { json(response, 404, { code: "job_not_found" }); return; }
            if (request.method === "GET" && !action) { json(response, 200, store.view(job)); return; }
            if (request.method === "POST" && action === "renew") { json(response, 200, await store.renew(id, owner, (await jsonBody(request)).leaseUntil)); return; }
            if (request.method === "POST" && action === "cancel") { json(response, 200, await store.cancel(id, owner)); return; }
            if (request.method === "POST" && action === "start") { json(response, 200, await store.start(id, owner)); return; }
            if (request.method === "PUT" && asset) {
                store.owned(id, owner);
                if (job.status !== "preparing" || !Object.hasOwn(job.input.assets, asset)) throw new Error("invalid_asset");
                const directory = join(store.directory(id), "assets");
                await mkdir(directory, { recursive: true });
                const target = join(directory, asset); const temp = target + "." + randomUUID();
                let size = 0;
                try {
                    await pipeline(request, new Transform({ transform(chunk, _encoding, done) { size += chunk.length; done(size > maxAssetBytes ? new Error("assets_too_large") : null, chunk); } }), createWriteStream(temp, { mode: 0o600 }));
                    store.owned(id, owner);
                    if (!size) throw new Error("missing_asset");
                    await rename(temp, target);
                    json(response, 200, { stored: true, size });
                } finally { await rm(temp, { force: true }); }
                return;
            }
            if (request.method === "GET" && (action === "video" || action === "preview")) {
                store.owned(id, owner);
                if (job.status !== "output_ready") throw new Error("output_not_ready");
                const file = join(store.directory(id), action === "video" ? "video.mp4" : "preview.png");
                const info = await stat(file);
                response.writeHead(200, { "Content-Type": action === "video" ? "video/mp4" : "image/png", "Content-Length": info.size });
                await pipeline(createReadStream(file), response); return;
            }
            json(response, 405, { code: "method_not_allowed" });
        } catch (error) {
            const code = errorCode(error);
            const status = code === "renderer_busy" ? 429 : code === "job_not_found" ? 404 : ["lease_lost", "input_conflict", "cancelled", "user_cancelled", "output_not_ready"].includes(code) ? 409 : code === "renderer_internal_error" ? 500 : 422;
            if (!response.headersSent && !response.destroyed) json(response, status, { code }); else response.destroy();
        }
    });
    server.requestTimeout = 60000;
    server.headersTimeout = 15000;
    return { server, store, close: async () => {
        closing = true; clearInterval(timer);
        for (const controller of store.controllers.values()) controller.abort(new Error("renderer_restarted"));
        if (active) await Promise.race([active, sleep(10000)]);
        server.closeAllConnections(); await new Promise((resolve) => server.close(resolve));
    } };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
    const renderer = await createRendererServer({ root: process.env.PREVIS_DATA_DIR || join(here, ".local/jobs"), token: process.env.PREVIS_RENDERER_TOKEN, publicDir: process.env.PREVIS_PUBLIC_DIR || resolve(here, "../web/dist-previs"), modelDir: process.env.PREVIS_MODEL_DIR || join(here, "public/models") });
    renderer.server.listen(Number(process.env.PORT || 8081), "0.0.0.0", () => console.log("previs renderer ready"));
    for (const signal of ["SIGTERM", "SIGINT"]) process.once(signal, async () => { await renderer.close(); process.exit(0); });
}
