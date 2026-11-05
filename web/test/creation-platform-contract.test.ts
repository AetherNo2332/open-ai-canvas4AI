import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { allowsAction, CONTRACT_VERSION, type EventPage, type GenerationRequest, type PlatformError, type TaskSnapshot, validateGenerationRequest } from "../src/sdk/creation-platform/contract";

// 与 backend/internal/creationplatform/testdata 共用一组合同样例：Go 与 TS 对同一 JSON 必须得出同样结论。
const samplesDir = join(import.meta.dir, "../../backend/internal/creationplatform/testdata");
const sample = <T>(name: string): T => JSON.parse(readFileSync(join(samplesDir, name), "utf8")) as T;

describe("creation platform contract samples", () => {
    test("request samples are valid and cover four capabilities and both model routes", () => {
        const names = readdirSync(samplesDir).filter((name) => name.startsWith("request-"));
        const capabilities = new Set<string>();
        const routes = new Set<string>();
        for (const name of names) {
            const request = sample<GenerationRequest>(name);
            expect(validateGenerationRequest(request)).toBeNull();
            expect(request.contractVersion).toBe(CONTRACT_VERSION);
            capabilities.add(request.capability);
            routes.add("logicalModelId" in request.model ? "logical" : "system");
        }
        expect([...capabilities].sort()).toEqual(["audio", "image", "text", "video"]);
        expect([...routes].sort()).toEqual(["logical", "system"]);
    });

    test("validation rejects the same malformed requests the Go contract rejects", () => {
        const base = sample<GenerationRequest>("request-image-system.json");
        const mutations: Array<[string, (request: Record<string, unknown>) => void]> = [
            ["both model routes", (r) => ((r.model as Record<string, unknown>).logicalModelId = "lm-1")],
            ["no model route", (r) => (r.model = {})],
            ["half system route", (r) => ((r.model as Record<string, unknown>).modelKey = "")],
            ["unknown capability", (r) => (r.capability = "previs")],
            ["wrong version", (r) => (r.contractVersion = 2)],
            ["missing idempotency", (r) => (r.idempotencyKey = " ")],
            ["missing application", (r) => ((r.business as Record<string, unknown>).application = "")],
            ["missing prompt", (r) => (r.prompt = "")],
            ["negative cost cap", (r) => (r.cost = { maxMicrocredits: -1 })],
            ["reference without id", (r) => (r.references = [{ kind: "image" }])],
            ["reference kind", (r) => (r.references = [{ resourceId: "res-1", kind: "file" }])],
            ["oversized idempotency", (r) => (r.idempotencyKey = "k".repeat(161))],
        ];
        for (const [name, mutate] of mutations) {
            const request = structuredClone(base) as unknown as Record<string, unknown>;
            mutate(request);
            const error = validateGenerationRequest(request as unknown as GenerationRequest);
            expect({ name, code: error?.code }).toEqual({ name, code: "invalid_request" });
        }
    });

    test("snapshots keep execution, resource delivery and billing separate", () => {
        const recovering = sample<TaskSnapshot>("snapshot-media-recovering.json");
        expect(recovering.execution).toBe("succeeded");
        expect(recovering.resources).toBe("failed");
        expect(allowsAction(recovering, "recover_media")).toBe(true);
        expect(allowsAction(recovering, "retry")).toBe(false);

        const pending = sample<TaskSnapshot>("snapshot-billing-pending.json");
        expect(pending.billing.status).toBe("pending_review");
        expect(pending.billing.chargedMicrocredits).toBeUndefined();
        expect(allowsAction(pending, "retry")).toBe(false);

        const succeeded = sample<TaskSnapshot>("snapshot-image-succeeded.json");
        expect(succeeded.result?.resources?.[0]?.resourceId).toBe("res-out-1");
        expect(succeeded.billing.chargedMicrocredits).toBe(100000);
    });

    test("event page ids increase and errors carry machine-readable codes", () => {
        const page = sample<EventPage>("events-page.json");
        expect(page.nextCursor).toBe("109");
        const ids = page.events.map((event) => event.id);
        expect(ids).toEqual([...ids].sort((a, b) => a - b));
        const conflict = sample<PlatformError>("error-idempotency-conflict.json");
        expect(conflict.code).toBe("idempotency_conflict");
        expect(conflict.retryable).toBe(false);
    });
});
