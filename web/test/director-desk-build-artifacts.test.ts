import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

// 导演台静态产物在云端镜像中的托管契约(T4):
// 独立构建(base=/desk/)→ 进镜像 → nginx 同源托管 /desk/,且不被工作台 SPA 兜底吞掉。
const repoRoot = resolve(import.meta.dir, "../..");
const read = (path: string) => readFileSync(resolve(repoRoot, path), "utf8");

describe("导演台静态托管契约", () => {
    test("应用构建以 /desk/ 为基准路径", () => {
        const config = read("director-desk/vite.config.mjs");
        expect(config).toContain('/desk/');
        expect(config).toMatch(/base\s*:/);
        expect(config).toMatch(/outDir\s*:\s*['"]?dist/);
    });

    test("镜像包含导演台构建阶段并发布到 /desk/", () => {
        const dockerfile = read("Dockerfile");
        expect(dockerfile).toMatch(/AS\s+desk-build/);
        expect(dockerfile).toMatch(/COPY --from=desk-build[^\n]*\/opt\/canvas-release\/desk/);
        // 发布目录必须仍在同一个 release 根下，交给既有发布脚本一起发布。
        expect(dockerfile).toContain("/opt/canvas-release");
    });

    test("nginx 明确托管 /desk/ 且不回退到工作台 SPA", () => {
        const nginx = read("nginx.conf");
        expect(nginx).toContain("location ^~ /desk/");
        expect(nginx).toContain("location = /desk");
        // /desk/ 段内必须是自己的 index.html 兜底，而不是工作台的 /index.html。
        const block = nginx.slice(nginx.indexOf("location ^~ /desk/"));
        const section = block.slice(0, block.indexOf("}"));
        expect(section).toContain("/desk/index.html");
        expect(section).not.toContain("/index.html ");
    });

    test("工作台构建阶段能解析 @desk 源码(bridge 契约单源)", () => {
        const dockerfile = read("Dockerfile");
        const webStage = dockerfile.slice(dockerfile.indexOf("AS web-build"), dockerfile.indexOf("AS desk-build"));
        // web 通过 @desk/* 引用 director-desk/src(tsconfig/vite alias),镜像内必须存在同一相对路径。
        expect(webStage).toMatch(/COPY director-desk\/src \/app\/director-desk\/src/);
        // 拷贝必须在 vite build 之前。
        expect(webStage.indexOf("COPY director-desk/src")).toBeLessThan(webStage.indexOf("vite.js build"));
    });

    test("构建上下文排除导演台的依赖与产物", () => {
        const dockerignore = read(".dockerignore");
        // 宿主安装的 node_modules 若进上下文,会覆盖镜像内安装的依赖(架构/版本不一致)。
        expect(dockerignore).toMatch(/director-desk\/node_modules/);
        expect(dockerignore).toMatch(/director-desk\/dist/);
    });

    test("3040 验收脚本覆盖 /desk/ 入口与产物指纹", () => {
        const script = read(".local/compose-accept/acceptance-3040.sh");
        expect(script).toContain("/desk/");
        expect(script).toMatch(/desk/i);
    });
});
