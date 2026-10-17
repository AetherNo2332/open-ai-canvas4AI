import { expect, test } from "bun:test";
import { effectiveAgentDefaultSkills } from "../src/lib/canvas/agent-effective-skill-defaults";

test("new conversation uses every summary item independent of market pages", () => {
    expect(
        effectiveAgentDefaultSkills(null, {
            count: 2,
            skills: [
                { skillId: "outside-page", skillName: "分镜" },
                { skillId: "second", skillName: "编剧" },
            ],
        }),
    ).toEqual([
        { skillId: "outside-page", skillName: "分镜" },
        { skillId: "second", skillName: "编剧" },
    ]);
});
test("existing conversation shows its frozen defaults instead of current global configuration", () => {
    expect(
        effectiveAgentDefaultSkills(
            {
                skills: [
                    { id: "old-default", name: "旧分镜", source: "global" },
                    { id: "picked", name: "用户技能", source: "user" },
                ],
            },
            { count: 1, skills: [{ skillId: "new-default", skillName: "新分镜" }] },
        ),
    ).toEqual([{ skillId: "old-default", skillName: "旧分镜" }]);
    expect(effectiveAgentDefaultSkills({ skills: [] }, { count: 1, skills: [{ skillId: "new-default", skillName: "新分镜" }] })).toEqual([]);
});
