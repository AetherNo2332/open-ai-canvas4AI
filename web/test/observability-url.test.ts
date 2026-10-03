import { describe, expect, test } from "bun:test";

import { normalizeGrafanaUrl } from "@/services/api/observability";

describe("normalizeGrafanaUrl", () => {
    test("accepts http(s) Grafana URLs and removes trailing slash", () => {
        expect(normalizeGrafanaUrl("https://grafana.example/d/agent/")).toBe("https://grafana.example/d/agent");
    });
    test("rejects unsafe URLs", () => {
        expect(normalizeGrafanaUrl("javascript:alert(1)")).toBe("");
        expect(normalizeGrafanaUrl("https://user:pass@grafana.example")).toBe("");
    });
});
