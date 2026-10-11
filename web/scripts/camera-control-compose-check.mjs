/**
 * 摄像机控制面板 — 本地 compose 脚本轨。
 *
 * 目标：对 docker-compose.local.yml 部署的真实构建产物做资产与接线断言，
 * 不需要模型渠道，也不写任何数据。
 *   1. /camera-controls/ 下 28 个面板资产经 nginx 全部 200 且为 PNG；
 *   2. 构建产物（JS chunk）里能找到新增机身的资产引用，证明映射进入产物；
 *   3. /api/health/ready 经 nginx 代理可达（后端在网）。
 * 任何断言失败 → exit 1；BASE_URL 默认 http://localhost:3000。
 */
const BASE_URL = process.env.BASE_URL || "http://localhost:3000";

const results = [];
let failures = 0;

function pass(name, detail = "") {
    results.push({ ok: true, name, detail });
    console.log(`PASS  ${name}${detail ? " — " + detail : ""}`);
}

function fail(name, detail = "") {
    results.push({ ok: false, name, detail });
    failures += 1;
    console.log(`FAIL  ${name}${detail ? " — " + detail : ""}`);
}

function assert(condition, name, detail = "") {
    if (condition) pass(name, detail);
    else fail(name, detail);
    return Boolean(condition);
}

async function fetchWithTimeout(url, timeoutMs = 15000) {
    const response = await fetch(url, { signal: AbortSignal.timeout(timeoutMs), redirect: "follow" });
    return response;
}

/** 新增机身的资产映射（camera-control-assets.ts 本次接线的五条）。 */
const NEW_CAMERA_BODY_IMAGES = ["arri-alexa-mini-lf.png", "arri-amira.png", "sony-venice-2.png", "sony-fx6.png", "sony-fx9.png"];
/** 既有 23 资产按 manifest 取名（机身 9 + 镜头 10 + 光圈 3 + 背景 1）。 */
const EXISTING_ASSETS = [
    "arri-alexa-35.png",
    "sony-venice.png",
    "arri-alexa-65.png",
    "red-v-raptor.png",
    "panavision-dxl2.png",
    "arricam-lt.png",
    "arriflex-435.png",
    "imax-keighley.png",
    "imax-film-camera.png",
    "zeiss-ultra-prime.png",
    "arri-signature-prime.png",
    "canon-k35.png",
    "cooke-s4.png",
    "cooke-speed-panchro.png",
    "cooke-sf-18x.png",
    "helios.png",
    "panavision-c-series.png",
    "panavision-primo.png",
    "hawk-class-x.png",
    "camera-control-bg.png",
    "f1_4.png",
    "f4.png",
    "f11.png",
];

async function checkAssets() {
    let okCount = 0;
    for (const file of [...NEW_CAMERA_BODY_IMAGES, ...EXISTING_ASSETS]) {
        const response = await fetchWithTimeout(`${BASE_URL}/camera-controls/${file}`);
        const type = response.headers.get("content-type") || "";
        if (response.status === 200 && type === "image/png") {
            okCount += 1;
        } else {
            fail(`asset ${file}`, `status=${response.status} type=${type}`);
        }
    }
    assert(okCount === NEW_CAMERA_BODY_IMAGES.length + EXISTING_ASSETS.length, `all ${okCount} panel assets served as image/png`);
    // 新增图必须是有效 PNG（签名 + 非零宽高），防止占位文件混入。
    for (const file of NEW_CAMERA_BODY_IMAGES) {
        const response = await fetchWithTimeout(`${BASE_URL}/camera-controls/${file}`);
        const bytes = new Uint8Array(await response.arrayBuffer());
        const isPng = bytes[0] === 0x89 && String.fromCharCode(...bytes.subarray(1, 4)) === "PNG";
        const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
        const width = bytes.length > 24 ? view.getUint32(16) : 0;
        const height = bytes.length > 24 ? view.getUint32(20) : 0;
        assert(isPng && width > 0 && height > 0, `${file} is a real PNG ${width}x${height}`);
    }
}

async function checkBundleWiring() {
    const home = await (await fetchWithTimeout(`${BASE_URL}/`)).text();
    // rolldown-vite 的分包引用是相对路径（import("./Foo-x.js")），按所在 chunk 目录解析。
    const seen = new Set();
    const queue = [...home.matchAll(/(?:src|href)="(\/[a-zA-Z0-9._/-]+\.js)"/g)].map((match) => match[1]);
    const haystacks = [];
    while (queue.length > 0 && seen.size < 1700) {
        const chunk = queue.shift();
        if (!chunk || seen.has(chunk)) continue;
        seen.add(chunk);
        let body = "";
        try {
            body = await (await fetchWithTimeout(`${BASE_URL}${chunk}`)).text();
        } catch {
            continue;
        }
        haystacks.push(body);
        const directory = chunk.slice(0, chunk.lastIndexOf("/") + 1);
        // rolldown 路由表两种引用形态：模板字面量 `./x.js` 与裸 "assets/x.js"。
        for (const match of body.matchAll(/[("'`](\.{0,2}\/?(?:assets\/)?[A-Za-z0-9._/-]+\.js)/g)) {
            let specifier = match[1].replace(/^\.\//, "");
            let resolved;
            if (specifier.startsWith("/")) resolved = specifier;
            else if (specifier.startsWith("assets/")) resolved = `${directory}${specifier.slice("assets/".length)}`;
            else resolved = `${directory}${specifier}`;
            if (!seen.has(resolved)) queue.push(resolved);
        }
    }
    const bundle = haystacks.join("\n");
    assert(seen.size > 10, `bundle crawl covered ${seen.size} chunks`);
    for (const file of NEW_CAMERA_BODY_IMAGES) {
        assert(bundle.includes(`camera-controls/${file}`), `bundle wires camera-controls/${file}`);
    }
    assert(bundle.includes("cameraPrompt"), "bundle carries the cameraPrompt persistence key");
}

async function checkBackendReady() {
    const response = await fetchWithTimeout(`${BASE_URL}/api/health/ready`);
    assert(response.status === 200, "backend /api/health/ready via nginx", `status=${response.status}`);
}

async function main() {
    console.log(`camera control compose check against ${BASE_URL}`);
    await checkBackendReady();
    await checkAssets();
    await checkBundleWiring();
    console.log(`\n=== ${results.length - failures}/${results.length} passed ===`);
    if (failures > 0) process.exit(1);
}

main().catch((cause) => {
    console.error("script error:", cause);
    process.exit(1);
});
