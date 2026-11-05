import { describe, expect, test } from "bun:test";

import { createCreationPlatformClient, createFetchTransport, type Capability, type GenerationRequest } from "../src/sdk/creation-platform";

// 只在 backend/internal/handler 的 TestPlatformSDKFourCapabilitiesAgainstRealBackend 启动真实后端时运行；
// 平时 `bun test` 跳过，保持离线可重复。
const baseUrl = process.env.CANVAS_PLATFORM_E2E_URL;
const cookie = process.env.CANVAS_PLATFORM_E2E_COOKIE;
const channels = JSON.parse(process.env.CANVAS_PLATFORM_E2E_CHANNELS || "{}") as Record<Capability, string>;

const describeE2E = baseUrl ? describe : describe.skip;

describeE2E("creation platform SDK against a real backend", () => {
    const transport = createFetchTransport({ baseUrl: baseUrl!, headers: () => ({ Cookie: cookie! }) });
    const client = createCreationPlatformClient({ transport, pollIntervalMs: 50 });

    const request = (capability: Capability, parameters: Record<string, unknown>): GenerationRequest => ({
        contractVersion: 1,
        capability,
        model: { channelId: channels[capability], modelKey: `${capability}-e2e` },
        prompt: `e2e ${capability}`,
        parameters,
        business: { application: "test", objectType: "e2e", objectId: capability },
        idempotencyKey: `e2e-${capability}`,
    });

    test.each([
        ["text", {}],
        ["image", { size: "1024x1024" }],
        ["video", { videoSeconds: 5, size: "16:9" }],
        ["audio", { audioVoice: "alloy", audioFormat: "mp3" }],
    ] as const)(
        "%s generation completes through submit, events and snapshot",
        async (capability, parameters) => {
            const catalog = await client.models(capability);
            expect(catalog.models.some((option) => "channelId" in option.selection && option.selection.channelId === channels[capability])).toBe(true);
            const receipt = await client.submit(request(capability, parameters));
            // 视频沿用生产轮询间隔（30 秒），给足等待时间。
            const done = await client.waitForTask(receipt.snapshot.taskId, { signal: AbortSignal.timeout(90_000) });
            expect(done.execution).toBe("succeeded");
            expect(done.billing.status).toBe("settled");
            if (capability === "text") {
                expect(done.result?.text).toBe("e2e text result");
                const replay = await transport.request({ method: "GET", path: `/tasks/${done.taskId}/text-deltas?after=0` });
                const data = replay.body.data as { finalText?: string; deltas?: Array<{ content: string }> };
                expect(data.finalText).toBe("e2e text result");
                expect((data.deltas ?? []).map((delta) => delta.content).join("")).toBe("e2e text result");
            } else {
                expect(done.resources).toBe("ready");
                const resource = done.result?.resources?.[0];
                expect(resource?.resourceId).toBeTruthy();
                expect(resource?.kind).toBe(capability);
                const access = await client.resourceAccess(resource!.resourceId);
                expect(access.url).toContain(resource!.resourceId);
            }
            // 重投同一请求取回原任务，不产生新任务。
            const replayed = await client.submit(request(capability, parameters));
            expect(replayed.replayed).toBe(true);
            expect(replayed.snapshot.taskId).toBe(done.taskId);
        },
        120_000,
    );

    test("uploaded reference resource can be used by a generation", async () => {
        const png = new Uint8Array([
            137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 2, 0, 0, 0, 3, 8, 6, 0, 0, 0, 185, 234, 222, 129, 0, 0, 0, 11, 73, 68, 65, 84, 120, 156, 99, 96, 192, 9, 0, 0, 27, 0, 1, 202, 60, 74, 13, 0, 0, 0, 0, 73, 69, 78, 68, 174,
            66, 96, 130,
        ]);
        const uploaded = await client.uploadResource(new Blob([png], { type: "image/png" }), { kind: "image", fileName: "ref.png", width: 2, height: 3, idempotencyKey: "e2e-ref" });
        expect(uploaded.resourceId).toBeTruthy();
        const withReference = { ...request("image", { size: "1024x1024" }), operation: "image_to_image", idempotencyKey: "e2e-image-ref", references: [{ resourceId: uploaded.resourceId, kind: "image" as const }] };
        const receipt = await client.submit(withReference);
        const done = await client.waitForTask(receipt.snapshot.taskId, { signal: AbortSignal.timeout(30_000) });
        expect(done.execution).toBe("succeeded");
    }, 60_000);
});
