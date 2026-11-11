import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import vm from "node:vm";
import { parseSync, traverse, types } from "@babel/core";

const source = (path) => readFileSync(new URL(path, import.meta.url), "utf8");

test("welcome entry loads configured appearance", async () => {
    let calls = 0;
    const imports = [];
    const main = source("../../src/main.tsx")
        .replace(/^import .*;\n/gm, "")
        .replace(/import\.meta\.env\.DEV/g, "false")
        .replace(/import\(/g, "loadModule(");
    vm.runInNewContext(main, {
        window: { location: { pathname: "/welcome" } },
        installChunkRecovery() {},
        isIsolatedDirectorRepro: () => false,
        bootstrapAppearance: async () => {
            calls++;
        },
        loadModule: async (name) => {
            imports.push(name);
        },
    });
    await Promise.resolve();
    assert.equal(calls, 1, "welcome entry must bootstrap appearance");
    assert.deepEqual(imports, ["./welcome-application"]);
});

test("welcome effects preserve configured SEO title", () => {
    const page = source("../../src/pages/welcome/index.tsx");
    const ast = parseSync(page, { configFile: false, babelrc: false, parserOpts: { plugins: ["typescript", "jsx"] } });
    const effects = [];
    traverse(ast, {
        CallExpression(path) {
            const { callee, arguments: args } = path.node;
            if (!types.isIdentifier(callee, { name: "useEffect" }) || !types.isArrayExpression(args[1]) || args[1].elements.length !== 0) return;
            const component = path.getFunctionParent()?.node.id?.name;
            assert.ok(component, "mount effect must have a component scope");
            assert.ok(types.isArrowFunctionExpression(args[0]) || types.isFunctionExpression(args[0]), `${component} must provide an effect callback`);
            effects.push({ component, callback: page.slice(args[0].start, args[0].end) });
        },
    });
    assert.ok(effects.length > 0);
    for (const title of ["Custom SEO Title", "Custom Site Name"]) {
        const document = { title };
        let dialogOpens = 0;
        let dialogCloses = 0;
        const componentScopes = {
            WelcomePage: { setLook() {}, getWelcomeLook() {} },
            WelcomeExperience: { storyRef: { current: null }, setReduced() {} },
            FilmDialog: {
                dialogRef: {
                    current: {
                        showModal() {
                            dialogOpens++;
                        },
                        close() {
                            dialogCloses++;
                        },
                    },
                },
            },
        };
        // Execute every mount effect in its component scope, including dialog setup and cleanup.
        for (const { component, callback } of effects) {
            assert.ok(Object.hasOwn(componentScopes, component), `missing effect scope for ${component}`);
            const cleanup = vm.runInNewContext(`(${callback})()`, {
                document,
                window: { matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }), addEventListener() {}, removeEventListener() {} },
                ...componentScopes[component],
            });
            assert.equal(document.title, title, `${component} must preserve appearance metadata title`);
            cleanup?.();
            assert.equal(document.title, title, `${component} cleanup must preserve appearance metadata title`);
        }
        assert.equal(dialogOpens, 1, "film dialog mount effect must run");
        assert.equal(dialogCloses, 1, "film dialog cleanup must run");
    }
});
