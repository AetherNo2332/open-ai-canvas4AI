import { describe, expect, test } from "bun:test";
import {
    clearAgentPageContext,
    getAgentPageContext,
    publishAgentPageContext,
} from "@/services/agent/business-context";

describe("Agent page declaration ownership", () => {
    test("a late old-page cleanup cannot hide the current page", () => {
        publishAgentPageContext({
            owner: "old-page",
            routeKey: "/canvas/a",
            declaration: { enabled: true, kind: "business", businessType: "canvas", agentDefinitionId: "canvas.default" },
            status: "ready",
            scope: { type: "canvas", id: "a" },
            agentDefinitionId: "canvas.default",
            displayName: "画布 A",
        });
        publishAgentPageContext({
            owner: "new-page",
            routeKey: "/projects/p",
            declaration: { enabled: true, kind: "business", businessType: "drama", agentDefinitionId: "drama.analysis" },
            status: "ready",
            scope: { type: "drama", id: "p" },
            agentDefinitionId: "drama.analysis",
            displayName: "短剧 P",
        });

        clearAgentPageContext("old-page");
        expect(getAgentPageContext().owner).toBe("new-page");
        expect(getAgentPageContext().scope).toEqual({ type: "drama", id: "p" });

        clearAgentPageContext("new-page");
        expect(getAgentPageContext().declaration.enabled).toBe(false);
    });
});
