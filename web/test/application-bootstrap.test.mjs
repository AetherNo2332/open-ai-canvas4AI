import { expect, test } from "bun:test";
import { runInNewContext } from "node:vm";

import { isIsolatedDirectorRepro } from "../src/lib/dev-repro";

// Execute the real entry point; replace only its font, network and UI side effects.
async function prepareEntry(dev, pathname) {
    const build = await Bun.build({
        entrypoints: [new URL("../src/main.tsx", import.meta.url).pathname],
        target: "browser",
        format: "iife",
        define: { "import.meta.env.DEV": JSON.stringify(dev) },
        plugins: [
            {
                name: "entry-side-effects",
                setup(builder) {
                    builder.onResolve({ filter: /^(@fontsource-variable\/|@\/services\/appearance-bootstrap$|\.\/(welcome-)?application$)/ }, ({ path }) => ({ path, namespace: "entry-test" }));
                    builder.onLoad({ filter: /.*/, namespace: "entry-test" }, ({ path }) => {
                        if (path.startsWith("@fontsource-variable/")) return { contents: "", loader: "js" };
                        if (path === "@/services/appearance-bootstrap") {
                            return { contents: 'export function bootstrapAppearance() { events.push("appearance"); return appearanceReady; }', loader: "js" };
                        }
                        return { contents: `events.push(${JSON.stringify(path)}); entryLoaded();`, loader: "js" };
                    });
                },
            },
        ],
    });
    expect(build.success).toBe(true);

    const events = [];
    let resolveAppearance;
    let entryLoaded;
    const appearanceReady = new Promise((resolve) => (resolveAppearance = resolve));
    const loaded = new Promise((resolve) => (entryLoaded = resolve));
    const listeners = new Map();
    runInNewContext(await build.outputs[0].text(), { window: { location: { pathname }, addEventListener: (name, listener) => listeners.set(name, listener) }, events, appearanceReady, entryLoaded });
    expect(typeof listeners.get("vite:preloadError")).toBe("function");
    return { events, resolveAppearance, loaded };
}

test("DEV director lab loads without calling the appearance backend", async () => {
    const entry = await prepareEntry(true, "/dev/director-repro");
    await entry.loaded;
    expect(entry.events).toEqual(["./application"]);
});

for (const [dev, pathname] of [
    [false, "/dev/director-repro"],
    [true, "/login"],
    [false, "/login"],
    [true, "/dev/director-repro/"],
    [true, "/dev/director-repro-other"],
]) {
    // 合并取舍：本 fork 有意保留启动闸门 —— main.tsx 先让外观解析落定，再
    // 载入 application（自上一轮同步 3f525569 起如此）。上游 331609e6 改写成
    // “外观与应用并行加载”（先 await loaded 再 resolveAppearance），在闸门下
    // 会永久超时，故恢复为先断言 appearance、放行外观后再等应用载入。
    test(`appearance still blocks normal startup: dev=${dev} path=${pathname}`, async () => {
        const entry = await prepareEntry(dev, pathname);
        expect(entry.events).toEqual(["appearance"]);
        entry.resolveAppearance();
        await entry.loaded;
        expect(entry.events).toEqual(["appearance", "./application"]);
    });
}

for (const pathname of ["/welcome", "/welcome/"]) {
    test(`public film entry remains independent: ${pathname}`, async () => {
        const entry = await prepareEntry(false, pathname);
        await entry.loaded;
        expect(entry.events).toEqual(["./welcome-application"]);
    });
}

test("provider isolation shares the exact DEV-only route boundary", () => {
    expect(isIsolatedDirectorRepro(true, "/dev/director-repro")).toBe(true);
    expect(isIsolatedDirectorRepro(false, "/dev/director-repro")).toBe(false);
    for (const path of ["/", "/login", "/dev/director-repro/", "/dev/director-repro-other"]) {
        expect(isIsolatedDirectorRepro(true, path)).toBe(false);
    }
});
