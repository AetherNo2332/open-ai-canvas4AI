import { expect, test } from "bun:test";
import { mergeCrewDraft, createCrewMutationGate, canReleaseCrewPending, crewCommitToRefresh, crewModelSelection } from "../src/lib/canvas/crew-edit-recovery";
import { createModelChannel, encodeChannelModel, type AiConfig } from "../src/stores/use-config-store";

test("CAS refresh preserves edited name but adopts remote budget and coordinator role", () => {
    const base = { name: "原名", role: "member", budget: { maxCredits: 10, maxSteps: 20 } };
    const draft = { ...base, name: "本地名称" };
    const remote = { ...base, role: "coordinator", budget: { maxCredits: 50, maxSteps: 30 } };
    expect(mergeCrewDraft(base, draft, remote)).toEqual({ ...remote, name: "本地名称" });
    expect(mergeCrewDraft(base, { ...draft, budget: { ...base.budget, maxCredits: 15 } }, remote)).toEqual({ ...remote, name: "本地名称", budget: { maxCredits: 15, maxSteps: 30 } });
});
test("configuration mutation gate admits one write until its deferred completion", async () => {
    const gate = createCrewMutationGate();
    let finish!: () => void;
    let writes = 0;
    const first = gate.run(async () => {
        writes++;
        await new Promise<void>((resolve) => {
            finish = resolve;
        });
    });
    await expect(
        gate.run(async () => {
            writes++;
        }),
    ).rejects.toThrow();
    expect(writes).toBe(1);
    finish();
    await first;
    await gate.run(async () => {
        writes++;
    });
    expect(writes).toBe(2);
});
test("an ambiguous submission retains identity across rejected retries", () => {
    for (const status of [400, 401, 403, 404, 422]) expect(canReleaseCrewPending(true, status)).toBe(false);
    expect(canReleaseCrewPending(false, 401)).toBe(true);
    expect(canReleaseCrewPending(false, 0)).toBe(false);
});
test("observed commit refresh is scope-bound and repeats only until acknowledged", () => {
    const run = { canvasId: "canvas", approval: { operationId: "op" } };
    expect(crewCommitToRefresh(run, "canvas", null)).toBe("op");
    expect(crewCommitToRefresh(run, "other", null)).toBe(null);
    expect(crewCommitToRefresh(run, "canvas", "op")).toBe(null);
});
test("managed model selection emits complete credential-free member configuration", () => {
    const channel = createModelChannel({ id: "managed", scope: "system", models: ["text-model"] });
    const config = { channels: [channel] } as AiConfig;
    expect(crewModelSelection(config, encodeChannelModel("managed", "text-model"))).toEqual({ model: "text-model", channelId: "managed", channelModelKey: "text-model" });
    expect(() => crewModelSelection(config, "missing-model")).toThrow();
    expect(() => crewModelSelection({ ...config, channels: [{ ...channel, scope: "custom" }] } as AiConfig, encodeChannelModel("managed", "text-model"))).toThrow();
});
