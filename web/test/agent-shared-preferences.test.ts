import { expect, test } from "bun:test";
import { apiClient } from "../src/services/api/request";
import { getAgentSharedPreferences, saveAgentSharedPreferences } from "../src/services/api/agent-shared-preferences";

test("shared preferences round trip and expose stale revisions", async () => {
    const previousAdapter = apiClient.defaults.adapter;
    const requests: unknown[] = [];
    apiClient.defaults.adapter = async (request) => {
        const body = request.data && JSON.parse(request.data);
        requests.push([request.method, request.url, body]);
        const data = request.method === "get" ? { revision: 4, language: "zh-CN" } : { ...body, revision: 5 };
        const response = body?.revision === 3 ? { code: 409, reason: "agent_preferences_conflict", msg: "设置已经变化", data: null } : { code: 0, msg: "", data };
        return { status: response.code || 200, statusText: "", headers: {}, config: request, data: response };
    };
    try {
        expect(await getAgentSharedPreferences()).toEqual({ revision: 4, language: "zh-CN" });
        const draft = { revision: 4, language: "ja-JP", tone: "neutral" as const };
        expect(await saveAgentSharedPreferences(draft)).toEqual({ ...draft, revision: 5 });
        await expect(saveAgentSharedPreferences({ ...draft, revision: 3 })).rejects.toMatchObject({
            status: 409,
            reason: "agent_preferences_conflict",
        });
        expect(requests).toEqual([
            ["get", "/agent/settings/shared-preferences", undefined],
            ["put", "/agent/settings/shared-preferences", draft],
            ["put", "/agent/settings/shared-preferences", { ...draft, revision: 3 }],
        ]);
    } finally {
        apiClient.defaults.adapter = previousAdapter;
    }
});
