import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve, sep } from "node:path";
import { JobStore, validateJob } from "../src/jobs.mjs";

const request = (overrides = {}) => ({ taskId: "task-one", attempt: 1, leaseOwner: "worker-one", leaseUntil: Date.now() + 45000, input: { canvasId: "canvas", sceneId: "scene", shotId: "shot", duration: 0.25, fps: 8, width: 640, height: 360, sourceHash: "source", assets: {}, scene: { id: "scene", objects: [], cameras: [{ id: "camera" }], shots: [{ id: "shot", cameraId: "camera" }] } }, ...overrides });

test("submission is idempotent and never replaces an admitted input", async () => {
    const root = await mkdtemp(join(tmpdir(), "previs-job-test-"));
    try {
        const store = await JobStore.open(root);
        const first = await store.submit(request());
        const second = await store.submit(request());
        assert.equal(first.taskId, second.taskId);
        assert.equal(store.jobs.size, 1);
        await assert.rejects(store.submit(request({ input: { ...request().input, fps: 12 } })), /input_conflict/);
        await store.cancel("task-one", "worker-one");
        const restarted = await JobStore.open(root);
        assert.equal(restarted.jobs.get("task-one").status, "cancelled");
        await assert.rejects(restarted.start("task-one", "worker-one"), /cancelled/);
    } finally {
        assert.ok(resolve(root).startsWith(resolve(tmpdir()) + sep));
        await rm(root, { recursive: true });
    }
});

test("rejects oversized frames, path traversal and uncontrolled scene URLs", () => {
    assert.throws(() => validateJob(request({ taskId: "../outside" })), /invalid_task/);
    assert.throws(() => validateJob(request({ input: { ...request().input, fps: 61 } })), /invalid_timing/);
    assert.throws(() => validateJob(request({ input: { ...request().input, width: 99999 } })), /invalid_dimensions/);
    const unsafe = request();
    unsafe.input.scene.objects = [{ id: "unsafe", visible: true, kind: "model", url: "http://169.254.169.254/private" }];
    assert.throws(() => validateJob(unsafe), /uncontrolled_asset/);
});

test("lease takeover fences the previous owner and does not revive user cancellation", async () => {
    const root = await mkdtemp(join(tmpdir(), "previs-job-test-"));
    try {
        const store = await JobStore.open(root);
        await store.submit(request());
        await assert.rejects(store.renew("task-one", "other", Date.now() + 45000), /lease_lost/);
        await store.cancel("task-one", "worker-one");
        await assert.rejects(store.submit(request({ attempt: 2, leaseOwner: "new-owner" })), /cancelled/);
    } finally {
        assert.ok(resolve(root).startsWith(resolve(tmpdir()) + sep));
        await rm(root, { recursive: true });
    }
});

test("crashed in-flight jobs remain indexed as failed after restart", async () => {
    const root = await mkdtemp(join(tmpdir(), "previs-job-test-"));
    try {
        const store = await JobStore.open(root);
        await store.submit(request());
        const job = store.jobs.get("task-one");
        job.status = "running";
        await store.save(job);
        const restarted = await JobStore.open(root);
        assert.equal(restarted.jobs.get("task-one").code, "renderer_restarted");
        assert.equal(restarted.jobs.get("task-one").status, "failed");
    } finally {
        assert.ok(resolve(root).startsWith(resolve(tmpdir()) + sep));
        await rm(root, { recursive: true });
    }
});

test("save-only recovery never recreates a missing or incomplete renderer job",async()=>{
 const root=await mkdtemp(join(tmpdir(),"previs-repair-test-"));
 try{
  const store=await JobStore.open(root);
  await assert.rejects(store.submit(request({recoverOnly:true})),/artifact_expired/);
  await store.submit(request());
  await assert.rejects(store.submit(request({recoverOnly:true})),/artifact_expired/);
  assert.equal(store.jobs.size,1);
 }finally{assert.ok(resolve(root).startsWith(resolve(tmpdir())+sep));await rm(root,{recursive:true});}
});

test("acknowledged persisted outputs release capacity without evicting unsaved artifacts", async () => {
    const root=await mkdtemp(join(tmpdir(),"previs-ack-test-"));
    try {
        const store=await JobStore.open(root);
        for(let i=0;i<16;i++) {
            await store.submit(request({taskId:`completed-${i}`}));
            const job=store.jobs.get(`completed-${i}`);job.status="output_ready";await store.save(job);
        }
        await assert.rejects(store.submit(request({taskId:"blocked"})),/renderer_busy/);
        await assert.rejects(store.acknowledge("completed-0","wrong-owner"),/lease_lost/);
        await store.acknowledge("completed-0","worker-one");
        await store.submit(request({taskId:"next"}));
        assert.ok(store.jobs.has("completed-1"),"unsaved recoverable artifact was evicted");
        assert.equal(store.jobs.size,16);
        const reopened=await JobStore.open(root);assert.ok(!reopened.jobs.has("completed-0"));
    } finally {assert.ok(resolve(root).startsWith(resolve(tmpdir())+sep));await rm(root,{recursive:true});}
});
