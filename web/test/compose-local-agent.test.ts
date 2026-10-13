import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

/**
 * docker-compose.local.yml 本地构建运行时也要带上 Pi Agent worker:
 * 后端只有设置了 CANVAS_AGENT_INTERNAL_TOKEN 才挂载 /internal-agent(见 backend/internal/handler/internal_agent.go),
 * agent 必须使用同一个 token,并挂载与镜像同源的 harness(含 TOOL_SCHEMA.json)。
 */
const repoRoot = resolve(import.meta.dir, "../..");
const read = (path: string) => readFileSync(resolve(repoRoot, path), "utf8").replace(/\r\n/gu, "\n");

function serviceBlock(compose: string, name: string): string {
    const start = compose.indexOf(`\n  ${name}:\n`);
    if (start < 0) return "";
    const rest = compose.slice(start + 1);
    const next = rest.slice(1).search(/\n {2}[a-z][a-z0-9-]*:\n|\n[a-z]/u);
    return next < 0 ? rest : rest.slice(0, next + 1);
}

describe("docker-compose.local.yml 包含 agent 服务", () => {
    const compose = read("docker-compose.local.yml");

    test("01 agent 从仓库 agent/Dockerfile 构建,默认随 local 栈启动", () => {
        const agent = serviceBlock(compose, "agent");
        expect(agent).not.toBe("");
        expect(agent).toContain("image: open-ai-canvas-agent:local");
        expect(agent).toContain("dockerfile: agent/Dockerfile");
        expect(agent).not.toContain("profiles:");
    });

    test("02 agent 与 backend 共用同一个内部 token 变量,并经 compose 网络访问 backend", () => {
        const agent = serviceBlock(compose, "agent");
        const backend = serviceBlock(compose, "backend");
        expect(backend).toContain("CANVAS_AGENT_INTERNAL_TOKEN: ${CANVAS_AGENT_INTERNAL_TOKEN:-}");
        expect(agent).toContain("CANVAS_AGENT_INTERNAL_TOKEN: ${CANVAS_AGENT_INTERNAL_TOKEN:-}");
        expect(agent).toContain("CANVAS_BACKEND_INTERNAL_URL: http://backend:8080");
        expect(agent).toMatch(/depends_on:\n\s+backend:\n\s+condition: service_healthy/u);
    });

    test("03 agent 只读挂载 harness,且不对外暴露端口", () => {
        const agent = serviceBlock(compose, "agent");
        expect(agent).toContain("CANVAS_AGENT_HARNESS_DIR: /agent/harness");
        expect(agent).toContain("./agent/harness:/agent/harness:ro");
        expect(agent).not.toContain("ports:");
    });

    test("04 .env.example 说明需要设置内部 token", () => {
        expect(read(".env.example")).toMatch(/^CANVAS_AGENT_INTERNAL_TOKEN=/mu);
    });
});
