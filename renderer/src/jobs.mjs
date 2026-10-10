import { mkdir, readFile, readdir, rename, writeFile, stat, rm } from "node:fs/promises";
import { join, resolve, sep } from "node:path";
import { probeVideo } from "./render.mjs";

const idPattern = /^[A-Za-z0-9_-]{1,80}$/;
const terminal = new Set(["output_ready", "failed", "cancelled"]);
export const maxAssetBytes = 64 * 1024 * 1024;
const defaultActor = "https://cdn.jsdelivr.net/gh/mrdoob/three.js@r185/examples/models/gltf/Xbot.glb";

export function validateJob(body) {
    if (!idPattern.test(body?.taskId || "") || typeof body.leaseOwner !== "string" || !body.leaseOwner || body.leaseOwner.length > 120 || !Number.isInteger(body.attempt) || body.attempt < 1) throw new Error("invalid_task");
    if (!Number.isFinite(body.leaseUntil) || body.leaseUntil <= Date.now() || body.leaseUntil > Date.now() + 65000) throw new Error("invalid_lease");
    const input = body.input;
    if (!input || !Number.isFinite(input.duration) || input.duration < 0.1 || input.duration > 60 || !Number.isInteger(input.fps) || input.fps < 1 || input.fps > 60) throw new Error("invalid_timing");
    if (![input.width, input.height].every((value) => Number.isInteger(value) && value >= 64 && value <= 1280 && value % 2 === 0) || input.width * input.height > 921600) throw new Error("invalid_dimensions");
    if (typeof input.sourceHash !== "string" || !input.sourceHash || !input.scene || !Array.isArray(input.scene.objects) || input.scene.objects.length > 128 || !Array.isArray(input.scene.cameras) || !Array.isArray(input.scene.shots) || !input.scene.shots.some((item) => item.id === input.shotId && input.scene.cameras.some((camera) => camera.id === item.cameraId))) throw new Error("invalid_scene");
    if (!input.assets || typeof input.assets !== "object" || Array.isArray(input.assets) || Object.keys(input.assets).length > 129 || Object.keys(input.assets).some((key) => !idPattern.test(key))) throw new Error("invalid_assets");
    const allowedURL = (url) => !url || url === "/models/Xbot.glb" || url === defaultActor || (typeof url === "string" && url.startsWith("/job-assets/") && Object.hasOwn(input.assets, url.slice(12)));
    for (const object of input.scene.objects) {
        if (!allowedURL(object.url) || object.storageKey) throw new Error("uncontrolled_asset");
    }
    if (!allowedURL(input.scene.environment?.url) || input.scene.environment?.storageKey) throw new Error("uncontrolled_asset");
    if (Buffer.byteLength(JSON.stringify(input)) > 1048576) throw new Error("input_too_large");
    return body;
}

export class JobStore {
    constructor(root) { this.root = resolve(root); this.jobs = new Map(); this.chain = Promise.resolve(); this.controllers = new Map(); }
    static async open(root) {
        const store = new JobStore(root);
        await mkdir(store.root, { recursive: true });
        for (const name of await readdir(store.root)) {
            if (!idPattern.test(name)) continue;
            try {
                const job = JSON.parse(await readFile(join(store.root, name, "manifest.json"), "utf8"));
                if (job.taskId !== name) continue;
                if (job.status === "running") {
                    try {
                        const output = await probeVideo(join(store.directory(name), "video.mp4"), job.input, AbortSignal.timeout(15000));
                        const preview = await readFile(join(store.directory(name), "preview.png"));
                        if (preview.length < 100 || preview.readUInt32BE(16) !== job.input.width || preview.readUInt32BE(20) !== job.input.height) throw new Error("invalid_preview");
                        job.output = {...output, previewSize: preview.length}; job.status = "output_ready"; job.phase = "output_ready"; job.code = "";
                    } catch { job.status = "failed"; job.code = "renderer_restarted"; }
                    await store.save(job);
                }
                store.jobs.set(name, job);
            } catch { /* An incomplete atomic write is not an admitted job. */ }
        }
        return store;
    }
    directory(id) {
        if (!idPattern.test(id)) throw new Error("invalid_task");
        const path = resolve(this.root, id);
        if (!path.startsWith(this.root + sep)) throw new Error("invalid_task");
        return path;
    }
    locked(action) {
        const next = this.chain.then(action);
        this.chain = next.catch(() => undefined);
        return next;
    }
    async save(job) {
        const directory = this.directory(job.taskId);
        await mkdir(directory, { recursive: true });
        job.updatedAt = Date.now();
        const path = join(directory, "manifest.json");
        await writeFile(path + ".tmp", JSON.stringify(job), { mode: 0o600 });
        await rename(path + ".tmp", path);
    }
    async submit(body) {
        validateJob(body);
        return this.locked(async () => {
            const prior = this.jobs.get(body.taskId);
            if (body.recoverOnly && (!prior || prior.status !== "output_ready")) throw new Error("artifact_expired");
            if (prior) {
                if (JSON.stringify(prior.input) !== JSON.stringify(body.input)) throw new Error("input_conflict");
                if (prior.status === "cancelled" && prior.code !== "lease_expired") throw new Error("cancelled");
                if (prior.leaseOwner !== body.leaseOwner && prior.leaseUntil > Date.now()) throw new Error("lease_lost");
                if (body.attempt < prior.attempt) throw new Error("lease_lost");
                if (prior.status === "running" && prior.leaseOwner !== body.leaseOwner) throw new Error("lease_lost");
                prior.leaseOwner = body.leaseOwner;
                prior.leaseUntil = body.leaseUntil;
                prior.attempt = body.attempt;
                if ((prior.code === "lease_expired" || prior.code === "renderer_restarted") && (prior.renderAttempts || 0) < 2) { prior.status = "preparing"; prior.code = ""; }
                await this.save(prior);
                return this.view(prior);
            }
            // Include retained artifacts: bounded count also bounds persistent disk use.
            if (this.jobs.size >= 16) throw new Error("renderer_busy");
            const job = { ...body, status: "preparing", code: "", phase: "preparing", frames: 0, renderAttempts: 0, createdAt: Date.now(), updatedAt: Date.now() };
            await this.save(job);
            this.jobs.set(body.taskId, job);
            return this.view(job);
        });
    }
    owned(id, owner) {
        const job = this.jobs.get(id);
        if (!job) throw new Error("job_not_found");
        if (owner !== job.leaseOwner || job.leaseUntil <= Date.now()) throw new Error("lease_lost");
        return job;
    }
    async renew(id, owner, until) {
        return this.locked(async () => {
            const job = this.owned(id, owner);
            if (!Number.isFinite(until) || until <= Date.now() || until > Date.now() + 65000) throw new Error("invalid_lease");
            job.leaseUntil = until;
            await this.save(job);
            return this.view(job);
        });
    }
    async start(id, owner) {
        return this.locked(async () => {
            const job = this.owned(id, owner);
            if (job.status === "cancelled" || job.status === "failed") throw new Error(job.code || job.status);
            if (job.status !== "preparing") return this.view(job);
            let size = 0;
            for (const name of Object.keys(job.input.assets)) {
                const asset = await stat(join(this.directory(id), "assets", name)).catch(() => null);
                if (!asset?.isFile() || asset.size <= 0) throw new Error("missing_asset");
                size += asset.size;
            }
            if (size > maxAssetBytes) throw new Error("assets_too_large");
            job.status = "queued"; job.phase = "queued";
            await this.save(job);
            return this.view(job);
        });
    }
    async cancel(id, owner, code = "user_cancelled") {
        return this.locked(async () => {
            const job = this.jobs.get(id);
            if (!job || owner !== job.leaseOwner) throw new Error("lease_lost");
            if (job.status === "output_ready") return this.view(job);
            this.controllers.get(id)?.abort();
            job.status = "cancelled"; job.code = code; job.phase = "cancelled";
            await this.save(job);
            return this.view(job);
        });
    }
    async acknowledge(id,owner) {
        return this.locked(async()=>{
            if(!this.jobs.has(id)) return {taskId:id,status:"released"};
            const job=this.owned(id,owner);
            if(job.status!=="output_ready") throw new Error("output_not_ready");
            await rm(this.directory(id),{recursive:true,force:true});
            this.jobs.delete(id);
            return {taskId:id,status:"released"};
        });
    }
    view(job) {
        return { taskId: job.taskId, attempt: job.attempt, status: job.status, phase: job.phase, code: job.code, frames: job.frames, frameCount: Math.ceil(job.input.duration * job.input.fps), renderAttempts: job.renderAttempts, updatedAt: job.updatedAt, lastProgressAt: job.lastProgressAt || job.createdAt, output: job.output || null };
    }
    async expire() {
        for (const job of this.jobs.values()) {
            if (!terminal.has(job.status) && job.leaseUntil <= Date.now()) await this.cancel(job.taskId, job.leaseOwner, "lease_expired");
            if (terminal.has(job.status) && Date.now() - job.updatedAt > 6 * 3600000) {
                await this.locked(async () => {
                    if (Date.now() - job.updatedAt <= 6 * 3600000 || this.controllers.has(job.taskId)) return;
                    const path = this.directory(job.taskId);
                    await rm(path, { recursive: true, force: true });
                    this.jobs.delete(job.taskId);
                });
            }
        }
    }
}
