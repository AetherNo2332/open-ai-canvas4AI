import { describe, expect, test } from "bun:test";
import { appendAgentPromptPrefill } from "@/lib/canvas/agent-prompt-prefill";

describe("appendAgentPromptPrefill", () => {
    test("empty and non-empty drafts receive the same trimmed text", () => {
        expect(appendAgentPromptPrefill("", "@[node:n1] ")).toBe("@[node:n1]");
        expect(appendAgentPromptPrefill("   ", "@[node:n1] ")).toBe("@[node:n1]");
        expect(appendAgentPromptPrefill("草稿", "@[node:n1] ")).toBe("草稿 @[node:n1]");
        expect(appendAgentPromptPrefill("草稿 ", "@[node:n1] ")).toBe("草稿 @[node:n1]");
    });

    test("repeating the same prefill into a draft that already ends with it is a no-op", () => {
        expect(appendAgentPromptPrefill("@[node:n1]", "@[node:n1] ")).toBe("@[node:n1]");
        expect(appendAgentPromptPrefill("草稿 @[node:n1]", "@[node:n1] ")).toBe("草稿 @[node:n1]");
        expect(appendAgentPromptPrefill("草稿 @[node:n1] ", "@[node:n1]")).toBe("草稿 @[node:n1] ");
    });

    test("a different or only partially matching prefill is still appended", () => {
        expect(appendAgentPromptPrefill("草稿 @[node:n1]", "@[node:n2]")).toBe("草稿 @[node:n1] @[node:n2]");
        expect(appendAgentPromptPrefill("x@[node:n1]", "@[node:n1]")).toBe("x@[node:n1] @[node:n1]");
        expect(appendAgentPromptPrefill("草稿", "   ")).toBe("草稿");
    });
});
