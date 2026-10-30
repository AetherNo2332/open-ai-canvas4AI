import { expect, mock, test } from "bun:test";

const data = new Map<string, string>();
mock.module("@/lib/localforage-storage", () => ({
    localForageStorageForScope: (scope: string) => ({
        getItem: async (key: string) => data.get(`${scope}:${key}`) ?? null,
        setItem: async (key: string, value: string) => void data.set(`${scope}:${key}`, value),
        removeItem: async (key: string) => void data.delete(`${scope}:${key}`),
    }),
}));
const conversations = await import("@/services/cloud-agent-conversations");
const base = { title: "对话", run: null, permissionMode: "auto" as const, createdAt: "2026-10-30T00:00:00Z", updatedAt: "2026-10-30T00:00:00Z" };

test("cloud sync error messages are not written into saved conversation history", async () => {
    await conversations.saveCloudAgentConversations(
        "canvas",
        "c1",
        [
            {
                ...base,
                id: "c1",
                messages: [
                    { id: "answer", role: "assistant", text: "结果" },
                    { id: conversations.CLOUD_AGENT_SYNC_ERROR_MESSAGE_ID, role: "error", text: "后端服务暂时不可用，请稍后重试" },
                ],
            },
        ],
        "scope-a",
    );
    const saved = JSON.parse([...data.values()][0]!);
    expect(saved.conversations[0].messages.map((message: { id: string }) => message.id)).toEqual(["answer"]);
});

test("cloud sync error messages saved by older versions are removed when loaded", async () => {
    data.clear();
    await conversations.saveCloudAgentConversations("canvas", "c1", [{ ...base, id: "c1", messages: [{ id: "answer", role: "assistant", text: "结果" }] }], "scope-b");
    const [key, raw] = [...data.entries()][0]!;
    const legacy = JSON.parse(raw);
    legacy.conversations[0].messages.push({ id: "cloud-history-error", role: "error", text: "旧提示" });
    data.set(key, JSON.stringify(legacy));
    const loaded = await conversations.loadCloudAgentConversations("canvas", "scope-b");
    expect(loaded.conversations[0]!.messages.map((message) => message.id)).toEqual(["answer"]);
});
