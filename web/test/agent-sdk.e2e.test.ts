import { test, expect } from "bun:test";
import { createAgentClient, AgentRequestError } from "../src/sdk/agent/client";
import { createAgentFetchTransport } from "../src/sdk/agent/transport";
import type { AgentRunSnapshot } from "../src/sdk/agent/contract";

const baseUrl = process.env.AGENT_SDK_E2E_URL;
test.skipIf(!baseUrl)(
    "SDK serves drama and canvas through real Go and Node, including lost tool response and restart",
    async () => {
        const client = createAgentClient({
            transport: createAgentFetchTransport({
                baseUrl: baseUrl!,
                headers: () => ({ Cookie: process.env.AGENT_SDK_E2E_COOKIE! }),
            }),
            retryDelayMs: () => 20,
        });
        const common = {
            channelId: "sdk-text",
            channelModelKey: "text-e2e",
            permissionMode: "read_only" as const,
            budget: { maxCredits: 20, maxSteps: 12 },
        };
        for (const kind of ["drama", "canvas"] as const) {
            const request = {
                ...common,
                scope: { type: kind, id: kind === "drama" ? "sdk-drama" : "sdk-canvas" },
                canvasId: kind === "canvas" ? "sdk-canvas" : "",
                ...(kind === "drama" ? { dramaEpisodeId: "sdk-episode" } : {}),
                contextScope: kind === "canvas" ? ["canvas"] : [],
                prompt: kind === "drama" ? "SDK_DRAMA: 分析本集" : "SDK_CANVAS: 请查看 cat 图片",
                idempotencyKey: `sdk-real-${kind}-request`,
            };
            const first = await client.startRun(request);
            const replay = await client.startRun(request);
            expect(first.run.id).toBe(replay.run.id);
            expect(first.run.scope).toEqual(request.scope);
            let finalStatus = "";
            const seen: number[] = [];
            await client.readEvents(
                first.run.id,
                (event) => {
                    seen.push(event.seq);
                },
                {
                    signal: AbortSignal.timeout(70_000),
                    onSnapshot: (run) => {
                        finalStatus = run.status;
                    },
                },
            );
            const { run } = await client.getRun(first.run.id);
            expect(finalStatus).toBe("completed");
            expect(run.status).toBe("completed");
            expect(run.spentCredits).toBeGreaterThan(0);
            expect(new Set(seen).size).toBe(seen.length);
            expect(seen.length).toBeGreaterThan(0);
            // 业务读取走各自具名工具：短剧分集 drama_read_episode，画布看图 canvas_inspect_image。
            const expectedTool = kind === "drama" ? "drama_read_episode" : "canvas_inspect_image";
            const receipts = run.events?.filter((event) => event.type === "tool_completed" && event.payload.toolName === expectedTool);
            expect(receipts?.length).toBe(1);
            expect(run.events?.filter((event) => event.type === "tool_completed" && event.payload.toolName === "read")).toHaveLength(0);
            expect(run.events?.some((event) => event.type === "assistant_message" && String(event.payload.text).includes("SDK_RESULT"))).toBe(true);
            const resumed: number[] = [];
            await client.readEvents(first.run.id, (event) => resumed.push(event.seq), { after: seen[0], signal: AbortSignal.timeout(5000) });
            expect(resumed.every((seq) => seq > seen[0]!)).toBe(true);
            try {
                await client.startRun({ ...request, prompt: "changed prompt" });
                throw new Error("expected idempotency conflict");
            } catch (error) {
                expect(error).toBeInstanceOf(AgentRequestError);
                expect((error as AgentRequestError).reason).toBe("agent_idempotency_conflict");
            }
        }
    },
    90_000,
);

test.skipIf(!baseUrl)(
    "scoped directories, question answers and model inputs isolate two users and both businesses",
    async () => {
        const transportFor = (second: boolean) =>
            createAgentFetchTransport({
                baseUrl: baseUrl!,
                headers: () => ({ Cookie: (second ? process.env.AGENT_SDK_E2E_SECOND_COOKIE : process.env.AGENT_SDK_E2E_COOKIE)! }),
            });
        const common = {
            channelId: "sdk-text",
            channelModelKey: "text-e2e",
            permissionMode: "read_only" as const,
            budget: { maxCredits: 20, maxSteps: 12 },
        };
        const complete = async (client: ReturnType<typeof createAgentClient>, id: string) => {
            let snapshot: AgentRunSnapshot | undefined;
            await client.readEvents(id, () => {}, {
                signal: AbortSignal.timeout(90_000),
                onSnapshot: (value) => {
                    snapshot = value;
                },
            });
            expect(snapshot?.status).toBe("completed");
            return (await client.getRun(id)).run;
        };
        await Promise.all(
            [false, true].map(async (second) => {
                for (const type of ["canvas", "drama"] as const) {
                    for (const index of [0, 1]) {
                        const object = `isolation-${Number(second)}-${type}-${index}`;
                        const scope = { type, id: object };
                        const client = createAgentClient({ transport: transportFor(second), scope });
                        const starts = await Promise.all(
                            [0, 1].map(async (turn) => {
                                const prompt = `SDK_ISOLATION_${Number(second)}_${type}_${index}_${turn}`;
                                const request = {
                                    ...common,
                                    scope,
                                    canvasId: type === "canvas" ? object : "",
                                    ...(type === "drama" ? { dramaEpisodeId: `${object}-ep` } : {}),
                                    contextScope: type === "canvas" ? ["canvas"] : [],
                                    prompt,
                                    idempotencyKey: prompt,
                                };
                                return (await client.startRun(request)).run;
                            }),
                        );
                        const runs = await Promise.all(starts.map((start) => complete(client, start.id)));
                        const directory = await client.listConversations();
                        expect(new Set(directory.items.map((item) => item.latestRunId))).toEqual(new Set(runs.map((run) => run.id)));
                        for (const run of runs) {
                            const readTool = type === "canvas" ? "canvas_read_content" : "drama_read_episode";
                            expect(run.events?.filter((event) => event.type === "tool_completed" && event.payload.toolName === readTool)).toHaveLength(1);
                            expect(run.events?.some((event) => String(event.payload.text).includes("SDK_ISOLATED_RESULT"))).toBe(true);
                            const history = await client.listConversationRuns(run.conversationId!);
                            expect(history.items.map((item) => item.id)).toEqual([run.id]);
                        }
                        const otherObject = createAgentClient({ transport: transportFor(second), scope: { type, id: `isolation-${Number(second)}-${type}-${1 - index}` } });
                        await expect(otherObject.getRun(runs[0].id)).rejects.toMatchObject({ status: 404 });
                        await expect(otherObject.cancelRun(runs[0].id)).rejects.toMatchObject({ status: 404 });
                        await expect(otherObject.archiveConversation(runs[0].conversationId!)).rejects.toMatchObject({ status: 404 });
                    }
                }
            }),
        );

        const scope = { type: "drama", id: "sdk-drama" };
        const client = createAgentClient({ transport: transportFor(false), scope });
        const request = { ...common, scope, canvasId: "", dramaEpisodeId: "sdk-episode", contextScope: [], prompt: "SDK_QUESTION", idempotencyKey: "sdk-question-test" };
        const question = await complete(client, (await client.startRun(request)).run.id);
        const queue = await client.getConversationQueue(question.conversationId!);
        expect(queue.awaitingQuestionId).toBeTruthy();
        const answered = await client.sendMessage(question.conversationId!, {
            mode: "interject",
            expectedRunId: question.id,
            replyToQuestionId: queue.awaitingQuestionId,
            request: { ...request, prompt: "SDK_ANSWER", idempotencyKey: "sdk-answer-test" },
        });
        let nextId = answered.runId;
        for (let attempt = 0; !nextId && attempt < 50; attempt++) {
            await Bun.sleep(100);
            const latest = await client.getConversationQueue(question.conversationId!);
            if (latest.latestRunId !== question.id) nextId = latest.latestRunId;
        }
        expect(nextId).toBeTruthy();
        const final = await complete(client, nextId!);
        expect(final.conversationId).toBe(question.conversationId);
        expect(final.events?.some((event) => String(event.payload.text).includes("SDK_ANSWERED_RESULT"))).toBe(true);
        const history = await client.listConversationRuns(question.conversationId!);
        expect(history.items.map((item) => item.id)).toEqual([question.id, final.id]);
        await client.archiveConversation(question.conversationId!);
        expect((await client.listConversations()).items.find((item) => item.id === question.conversationId)?.archived).toBe(true);
    },
    360_000,
);
