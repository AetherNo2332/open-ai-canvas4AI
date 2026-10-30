import { expect, test } from "bun:test";

// 设置页与画布 Agent 设置里的「长期记忆」使用上游记忆文件面板：模式切换、待确认修改、编辑与历史，
// 只走 /agent/memory 接口；旧经验库接口（/agent/memories）与导出入口已退役。
test("memory settings pane uses the memory document API only", async () => {
    const pane = await Bun.file(new URL("../src/pages/settings/agent-memory-pane.tsx", import.meta.url)).text();
    for (const call of ["getAgentMemory", "updateAgentMemoryMode", "decideAgentMemoryRevision", "undoAgentMemoryRevision", "saveAgentMemory", "listAgentMemoryRevisions"]) {
        expect(pane).toContain(call);
    }
    expect(pane).toContain('ariaLabel="长期记忆模式"');
    expect(pane).not.toContain("agent-memories");
    expect(pane).not.toContain("exportAgentMemories");
    const api = await Bun.file(new URL("../src/services/api/agent-memory.ts", import.meta.url)).text();
    expect(api).toContain('"/agent/memory"');
    expect(api).not.toContain("/agent/memories");
    for (const owner of ["../src/pages/settings/index.tsx", "../src/components/canvas/canvas-cloud-agent-settings.tsx"]) {
        expect(await Bun.file(new URL(owner, import.meta.url)).text()).toContain('import AgentMemoryPane from "@/pages/settings/agent-memory-pane"'.replace("@/pages/settings/", owner.includes("settings/index") ? "./" : "@/pages/settings/"));
    }
});
