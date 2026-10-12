import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentDefaultSkillCard } from "../src/components/canvas/canvas-agent-skill-library-modal";
import { canvasThemes } from "../src/lib/canvas-theme";

test("default reference renders its name without a marketplace or installed Skill object", () => {
    const html = renderToStaticMarkup(<AgentDefaultSkillCard skill={{ skillId: "outside-page", skillName: "冻结分镜" }} theme={canvasThemes.light} />);
    expect(html).toContain("冻结分镜");
    expect(html).toContain("全局默认");
    expect(html).toContain("无需安装");
    expect(html).not.toContain("<button");
});
