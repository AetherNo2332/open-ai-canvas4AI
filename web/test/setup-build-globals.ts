// 测试环境补齐构建期注入的全局量与最小 DOM 桩（供路由/组件静态合同测试使用）。
// 必须作为被测文件的第一个 import 出现(ES 模块按顺序求值)，先于任何触达这些全局量的模块。
// bunt 单进程跑全部测试文件:仅在缺失时定义本文件使用的桩,并在本文件结束时清理,
// 避免 document/window/localStorage 泄漏污染其他测试文件（历史教训:skills 缓存用例与
// detect-gpu/React DOM 探测在假 window 下行为改变）。
import { afterAll } from "bun:test";

const g = globalThis as Record<string, unknown>;
const definedHere: string[] = [];

function defineIfMissing(name: string, value: unknown) {
    if (typeof g[name] === "undefined") {
        g[name] = value;
        definedHere.push(name);
    }
}

defineIfMissing("__APP_VERSION__", "v0.0.0-test");
defineIfMissing("__APP_CHANGELOG__", "");

if (typeof g.document === "undefined") {
    const location = {
        pathname: "/",
        search: "",
        hash: "",
        href: "http://localhost/",
        origin: "http://localhost",
        host: "localhost",
        protocol: "http:",
    };
    const store = new Map<string, string>();
    const storageStub = {
        getItem: (key: string) => (store.has(key) ? (store.get(key) as string) : null),
        setItem: (key: string, value: string) => void store.set(key, String(value)),
        removeItem: (key: string) => void store.delete(key),
        clear: () => store.clear(),
        key: (index: number) => [...store.keys()][index] ?? null,
        get length() {
            return store.size;
        },
    };
    defineIfMissing("localStorage", storageStub);
    defineIfMissing("sessionStorage", storageStub);
    defineIfMissing("window", {
        location,
        history: { state: null, length: 1, pushState: () => {}, replaceState: () => {}, go: () => {}, back: () => {} },
        addEventListener: () => {},
        removeEventListener: () => {},
        scroll: () => {},
        scrollTo: () => {},
        // 不回调:rAF 桩立即回调会触发模块级动画循环死循环。
        requestAnimationFrame: () => 0,
        cancelAnimationFrame: () => {},
        matchMedia: () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {} }),
        localStorage: g.localStorage,
        sessionStorage: g.sessionStorage,
    });
    defineIfMissing("document", {
        defaultView: g.window,
        baseURI: "http://localhost/",
        documentElement: { style: {} },
        createElement: () => ({ style: {}, setAttribute: () => {}, appendChild: () => {} }),
        addEventListener: () => {},
        removeEventListener: () => {},
    });
}

afterAll(() => {
    for (const name of definedHere) delete g[name];
});