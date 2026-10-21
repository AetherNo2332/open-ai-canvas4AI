import { describe, expect, test } from "bun:test";
import { USER_FEEDBACK_PLUGIN_ID, userFeedbackPlugin } from "../src/lib/plugins/builtin/user-feedback";
import { isOfficialApplicationPluginId } from "../src/lib/plugins/official-applications";

describe("User Feedback Plugin", () => {
    test("插件元数据完整且正确注册", () => {
        expect(userFeedbackPlugin.manifest.id).toBe(USER_FEEDBACK_PLUGIN_ID);
        expect(userFeedbackPlugin.manifest.name).toContain("反馈");
        expect(isOfficialApplicationPluginId(USER_FEEDBACK_PLUGIN_ID)).toBe(true);
    });

    test("反馈类型与状态枚举完整", () => {
        const types = ["bug", "feature", "improvement", "other"];
        const statuses = ["pending", "processing", "resolved", "closed", "archived"];
        expect(types).toHaveLength(4);
        expect(statuses).toHaveLength(5);
    });
});
