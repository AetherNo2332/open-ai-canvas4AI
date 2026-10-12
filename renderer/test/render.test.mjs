import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, writeFile, readFile, readdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve, sep } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { createRendererServer } from "../src/server.mjs";
import { probeVideo } from "../src/render.mjs";
import {JobStore} from "../src/jobs.mjs";

export function renderFixture() {
    const transform = (position) => ({ position, rotation: [0, 0, 0], scale: [1, 1, 1] });
    return { canvasId: "canvas", sceneId: "scene", shotId: "shot", sourceHash: "source", assets: {}, duration: 0.375, fps: 8, width: 640, height: 360, scene: {
        id: "scene", version: 1, title: "Headless actor and camera", background: "#d8dde3", gridVisible: false, environmentIntensity: 0.7,
        objects: [
            { id: "actor", kind: "actor", primitive: "character", url: "/models/Xbot.glb", color: "#8795a5", visible: true, pose: "wave", archetype: "adult", transform: transform([0, 0, 0]), castShadow: true, receiveShadow: true, keyframes: [], boneTracks: [], boneOverrides: {}, motionClips: [] },
            { id: "box", kind: "primitive", primitive: "box", color: "#c36b53", visible: true, transform: transform([1, 0.5, 0]), castShadow: true, receiveShadow: true, keyframes: [] },
        ],
        cameras: [{ id: "camera", name: "camera", transform: transform([3, 2, 4]), target: [0, 1, 0], fov: 50, focalLength: 35, near: 0.05, far: 500, keyframes: [{ id: "start", time: 0, transform: transform([3, 2, 4]) }, { id: "end", time: 0.375, transform: transform([1.7, 1.4, 2.8]) }] }],
        shots: [{ id: "shot", cameraId: "camera", duration: 0.375, fps: 8, aspectRatio: "16:9", cameraMove: "static", shotSize: "medium", prompt: "" }],
        activeShotId: "shot", lights: [{ id: "light", type: "directional", intensity: 2.4, color: "#ffffff", castShadow: true, transform: transform([4, 6, 4]) }, { id: "ambient", type: "ambient", intensity: 0.8, color: "#ffffff", transform: transform([0, 0, 0]) }],
    } };
}

test("HTTP control rejects unauthorized submissions and keeps cancellation durable", async () => {
    const root = await mkdtemp(join(tmpdir(), "previs-http-test-"));
    const token = "test-only-".padEnd(40, "x");
    const renderer = await createRendererServer({ root, token, render: async () => { throw new Error("asset_load_failed"); } });
    await new Promise((resolve) => renderer.server.listen(0, "127.0.0.1", resolve));
    const url = `http://127.0.0.1:${renderer.server.address().port}`;
    try {
        const body = { taskId: "http-task", attempt: 1, leaseOwner: "worker", leaseUntil: Date.now() + 45000, input: renderFixture() };
        assert.equal((await fetch(`${url}/jobs`, { method: "POST", body: JSON.stringify(body) })).status, 401);
        const headers = { authorization: `Bearer ${token}`, "x-previs-lease-owner": "worker" };
        assert.equal((await fetch(`${url}/jobs`, { method: "POST", headers, body: JSON.stringify(body) })).status, 200);
        assert.equal((await fetch(`${url}/jobs/http-task/cancel`, { method: "POST", headers })).status, 200);
        assert.equal((await fetch(`${url}/jobs/http-task/start`, { method: "POST", headers })).status, 409);
    } finally { await renderer.close(); assert.ok(resolve(root).startsWith(resolve(tmpdir()) + sep)); await rm(root, { recursive: true }); }
});

test("probe rejects an empty or undecodable file", async () => {
    const root = await mkdtemp(join(tmpdir(), "previs-probe-test-"));
    try {
        const path = join(root, "empty.mp4"); await writeFile(path, "");
        await assert.rejects(probeVideo(path, renderFixture(), AbortSignal.timeout(10000)));
    } finally { assert.ok(resolve(root).startsWith(resolve(tmpdir()) + sep)); await rm(root, { recursive: true }); }
});

test("renderer crash has a bounded retry and asset/WebGL failures stop immediately", async () => {
    for (const code of ["renderer_crashed", "asset_load_failed", "webgl_context_lost"]) {
        const root = await mkdtemp(join(tmpdir(), "previs-fault-test-"));
        let calls = 0;
        const renderer = await createRendererServer({ root, token: "test-token".padEnd(40, "x"), render: async () => { calls++; throw new Error(code); } });
        try {
            await renderer.store.submit({ taskId: "fault-task", attempt: 1, leaseOwner: "worker", leaseUntil: Date.now()+45000, input: renderFixture() });
            await renderer.store.start("fault-task", "worker");
            const deadline = Date.now()+10000;
            while (renderer.store.jobs.get("fault-task").status !== "failed" && Date.now()<deadline) await new Promise(resolve=>setTimeout(resolve,50));
            const job=renderer.store.jobs.get("fault-task");
            assert.equal(job.status, "failed"); assert.equal(job.code, code);
            assert.equal(calls, code === "renderer_crashed" ? 2 : 1);
            assert.equal(job.output, undefined);
        } finally { await renderer.close(); assert.ok(resolve(root).startsWith(resolve(tmpdir()) + sep)); await rm(root, { recursive: true }); }
    }
});

test("cancel during rendering fences a late successful completion", async () => {
    const root = await mkdtemp(join(tmpdir(), "previs-cancel-test-"));
    let started; const start=new Promise(resolve=>{started=resolve});
    let release; const finish=new Promise(resolve=>{release=resolve});
    const renderer=await createRendererServer({root,token:"test-token".padEnd(40,"x"),render:async()=>{started();await finish;return {width:640,height:360,frameCount:3};}});
    try {
        await renderer.store.submit({taskId:"cancel-task",attempt:1,leaseOwner:"worker",leaseUntil:Date.now()+45000,input:renderFixture()});
        await renderer.store.start("cancel-task","worker"); await start;
        await renderer.store.cancel("cancel-task","worker"); release();
        await new Promise(resolve=>setTimeout(resolve,100));
        const job=renderer.store.jobs.get("cancel-task");
        assert.equal(job.status,"cancelled");assert.equal(job.code,"user_cancelled");assert.equal(job.output,undefined);
    } finally { release(); await renderer.close(); assert.ok(resolve(root).startsWith(resolve(tmpdir()) + sep)); await rm(root,{recursive:true}); }
});

test("old executor failure cannot overwrite the new owner after takeover",async()=>{
    const root=await mkdtemp(join(tmpdir(),"previs-takeover-test-"));
    let started;const start=new Promise(resolve=>{started=resolve});
    let rejectOld;const stalled=new Promise((_,reject)=>{rejectOld=reject});
    const renderer=await createRendererServer({root,token:"test-token".padEnd(40,"x"),render:async()=>{started();await stalled;}});
    try {
        const request={taskId:"takeover",attempt:1,leaseOwner:"old",leaseUntil:Date.now()+45000,input:renderFixture()};
        await renderer.store.submit(request);await renderer.store.start("takeover","old");await start;
        await renderer.store.cancel("takeover","old","lease_expired");
        renderer.store.jobs.get("takeover").leaseUntil=Date.now()-1;
        await renderer.store.submit({...request,attempt:2,leaseOwner:"new",leaseUntil:Date.now()+45000});
        await renderer.store.start("takeover","new");
        rejectOld(new Error("asset_load_failed"));await new Promise(resolve=>setTimeout(resolve,25));
        const job=renderer.store.jobs.get("takeover");assert.equal(job.status,"queued");assert.equal(job.leaseOwner,"new");
        await renderer.store.cancel("takeover","new");
    } finally {rejectOld(new Error("cancelled"));await renderer.close();assert.ok(resolve(root).startsWith(resolve(tmpdir())+sep));await rm(root,{recursive:true});}
});

test("real Chromium renders an offline actor, blocks and moving camera into playable MP4", { skip: !process.env.PREVIS_REAL_RENDER, timeout: 180000 }, async () => {
    const root = await mkdtemp(join(tmpdir(), "previs-real-test-"));
    const token = "test-only-".padEnd(40, "x");
    const renderer = await createRendererServer({ root, token, publicDir: resolve("web/dist-previs"), modelDir: resolve("renderer/public/models") });
    await new Promise((resolve) => renderer.server.listen(0, "127.0.0.1", resolve));
    const url = `http://127.0.0.1:${renderer.server.address().port}`;
    const headers = { authorization: `Bearer ${token}`, "x-previs-lease-owner": "worker" };
    try {
        const body = { taskId: "real-task", attempt: 1, leaseOwner: "worker", leaseUntil: Date.now() + 45000, input: renderFixture() };
        assert.equal((await fetch(`${url}/jobs`, { method: "POST", headers, body: JSON.stringify(body) })).status, 200);
        assert.equal((await fetch(`${url}/jobs/real-task/start`, { method: "POST", headers })).status, 200);
        let result;
        const deadline = Date.now() + 140000;
        while (Date.now() < deadline) {
            result = await (await fetch(`${url}/jobs/real-task`, { headers })).json();
            if (["output_ready", "failed", "cancelled"].includes(result.status)) break;
            await fetch(`${url}/jobs/real-task/renew`, { method: "POST", headers, body: JSON.stringify({ leaseUntil: Date.now() + 45000 }) });
            await new Promise((resolve) => setTimeout(resolve, 1000));
        }
        assert.equal(result.status, "output_ready", JSON.stringify(result));
        assert.equal(result.output.frameCount, 3);
        const video = await fetch(`${url}/jobs/real-task/video`, { headers });
        assert.equal(video.status, 200);
        const path = join(root, "observed.mp4"); await writeFile(path, Buffer.from(await video.arrayBuffer()));
        const decoded = await promisify(execFile)(process.env.FFMPEG_PATH || "ffmpeg", ["-v", "error", "-i", path, "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1"], { encoding: "buffer", maxBuffer: 8 * 1024 * 1024, windowsHide: true });
        const frameSize = 640 * 360 * 3;
        let changed = 0;
        for (let i = 0; i < frameSize; i++) if (Math.abs(decoded.stdout[i] - decoded.stdout[frameSize * 2 + i]) > 10) changed++;
        assert.ok(changed > 1000, `camera motion should change visible pixels, changed=${changed}`);
        assert.ok((await readFile(join(root, "real-task", "preview.png"))).length > 1000);
        // Simulate a crash after FFmpeg completed but before manifest success committed.
        const completed=renderer.store.jobs.get("real-task");completed.status="running";await renderer.store.save(completed);
        const recovered=await JobStore.open(root);
        assert.equal(recovered.jobs.get("real-task").status,"output_ready");
        assert.equal(recovered.jobs.get("real-task").renderAttempts,1);
        if(process.platform==="linux") {
            const survivors=[];
            for(const pid of await readdir("/proc")) {
                if(!/^\d+$/.test(pid))continue;
                const args=(await readFile(`/proc/${pid}/cmdline`).catch(()=>Buffer.alloc(0))).toString();
                if(args.includes(join(root,"real-task","browser-")))survivors.push(pid);
            }
            assert.deepEqual(survivors,[],"browser helpers escaped job cleanup");
        }
    } finally { await renderer.close(); assert.ok(resolve(root).startsWith(resolve(tmpdir()) + sep)); await rm(root, { recursive: true }); }
});
