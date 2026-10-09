import { spawn, execFile } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { join, resolve, sep } from "node:path";

const delay = (ms, signal) => new Promise((resolve, reject) => {
    signal?.throwIfAborted();
    const timer = setTimeout(done, ms);
    const abort = () => { clearTimeout(timer); signal.removeEventListener("abort", abort); reject(signal.reason); };
    function done() { signal?.removeEventListener("abort", abort); resolve(); }
    signal?.addEventListener("abort", abort, { once: true });
});

export function chromeBinary() {
    const candidates = [process.env.CHROME_BIN, "/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/google-chrome", "C:/Program Files/Google/Chrome/Application/chrome.exe", "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"].filter(Boolean);
    const found = candidates.find((path) => existsSync(path));
    if (!found) throw new Error("chromium_not_installed");
    return found;
}

async function connect(url, signal) {
    const socket = new WebSocket(url);
    await new Promise((resolve, reject) => {
        const timer = setTimeout(() => { socket.close(); reject(new Error("renderer_crashed")); }, 15000);
        socket.addEventListener("open", () => { clearTimeout(timer); resolve(); }, { once: true });
        socket.addEventListener("error", () => { clearTimeout(timer); reject(new Error("renderer_crashed")); }, { once: true });
    });
    let sequence = 0;
    const pending = new Map();
    const listeners = new Map();
    const closed = () => { for (const item of pending.values()) { clearTimeout(item.timer); item.reject(new Error("renderer_crashed")); } pending.clear(); };
    socket.addEventListener("close", closed);
    const abort = () => { closed(); socket.close(); };
    signal.addEventListener("abort", abort, { once: true });
    socket.addEventListener("message", (event) => {
        const message = JSON.parse(String(event.data));
        if (message.id && pending.has(message.id)) {
            const item = pending.get(message.id);
            pending.delete(message.id); clearTimeout(item.timer);
            message.error ? item.reject(new Error("browser_protocol_error")) : item.resolve(message.result);
        } else {
            for (const listener of listeners.get(message.method) || []) Promise.resolve(listener(message.params)).catch(() => undefined);
        }
    });
    const send = (method, params = {}) => new Promise((resolve, reject) => {
        signal.throwIfAborted();
        if (socket.readyState !== WebSocket.OPEN) { reject(new Error("renderer_crashed")); return; }
        const id = ++sequence;
        const timer = setTimeout(() => { pending.delete(id); reject(new Error("browser_step_timeout")); }, 45000);
        pending.set(id, { resolve, reject, timer });
        socket.send(JSON.stringify({ id, method, params }));
    });
    return { send, on: (method, callback) => listeners.set(method, [...(listeners.get(method) || []), callback]), close: () => { signal.removeEventListener("abort", abort); closed(); socket.close(); } };
}

export async function openChromium(directory, baseURL, input, signal) {
    const profile = await mkdtemp(join(directory, "browser-"));
    const args = ["--headless=new", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--disable-background-networking", "--disable-component-update", "--disable-sync", "--no-first-run", "--no-default-browser-check", "--renderer-process-limit=1", "--num-raster-threads=2", "--enable-unsafe-swiftshader", "--use-angle=swiftshader", "--disable-dev-shm-usage", `--window-size=${input.width},${input.height}`];
    if (process.platform === "linux") args.push("--no-sandbox");
    args.push("about:blank");
    const child = spawn(chromeBinary(), args, { stdio: ["ignore","ignore","pipe"], windowsHide: true, detached:process.platform==="linux", env:process.platform==="linux"?{...process.env,XDG_CONFIG_HOME:join(profile,"config"),XDG_CACHE_HOME:join(profile,"cache")}:process.env });
    let chromeErrors="";
    child.stderr.on("data",chunk=>{chromeErrors=(chromeErrors+String(chunk)).slice(-2000)});
    let spawnError = false;
    child.on("error", () => { spawnError = true; });
    let cdp;
    const close = async () => {
        cdp?.close();
        if (child.pid && child.exitCode === null) {
            if (process.platform === "win32") await new Promise((resolve) => execFile("taskkill", ["/PID", String(child.pid), "/T", "/F"], { windowsHide: true }, () => resolve()));
            else { try { process.kill(-child.pid,"SIGTERM"); } catch { child.kill("SIGTERM"); } }
            await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(3000)]);
            if (child.exitCode === null) { if(process.platform==="linux") {try{process.kill(-child.pid,"SIGKILL")}catch{child.kill("SIGKILL")}}else child.kill("SIGKILL"); }
        }
        // Crashpad may detach from the process group. Only this profile's exact argv owns it.
        if(process.platform==="linux") {
            for(const pid of await readdir("/proc")) {
                if(!/^\d+$/.test(pid) || Number(pid)===process.pid)continue;
                const argv=(await readFile(`/proc/${pid}/cmdline`).catch(()=>Buffer.alloc(0))).toString().split("\0");
                if(argv.some(value=>value===`--user-data-dir=${profile}` || value.startsWith(`--database=${profile}/`))) {
                    try{process.kill(Number(pid),"SIGKILL")}catch{}
                }
            }
        }
        if (!resolve(profile).startsWith(resolve(directory) + sep)) throw new Error("invalid_profile_path");
        await rm(profile, { recursive: true, force: true, maxRetries:3,retryDelay:100 });
    };
    try {
        const deadline = Date.now() + 30000;
        let port;
        while (Date.now() < deadline) {
            signal.throwIfAborted();
            if (spawnError || child.exitCode !== null) throw new Error("renderer_crashed");
            const value = await readFile(join(profile, "DevToolsActivePort"), "utf8").catch(() => "");
            if (value) { port = Number(value.split("\n")[0]); break; }
            await delay(100, signal);
        }
        if (!port) throw new Error("renderer_crashed");
        const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`, { signal: AbortSignal.any([signal, AbortSignal.timeout(10000)]) })).json();
        const target = targets.find((item) => item.type === "page");
        if (!target) throw new Error("renderer_crashed");
        cdp = await connect(target.webSocketDebuggerUrl, signal);
        let browserError = "";
        cdp.on("Runtime.exceptionThrown", (event) => {
            browserError = "scene_script_error";
            if (process.env.PREVIS_RENDER_DIAGNOSTICS) console.error("viewport exception", String(event.exceptionDetails?.exception?.description || event.exceptionDetails?.text || "").split("\n")[0].slice(0, 300));
        });
        cdp.on("Fetch.requestPaused", async (event) => {
            const url = new URL(event.request.url);
            const allowed = url.origin === new URL(baseURL).origin || url.protocol === "data:" || url.protocol === "blob:";
            if (!allowed) {
                browserError = "uncontrolled_asset";
                if (process.env.PREVIS_RENDER_DIAGNOSTICS) console.error("blocked renderer origin", url.origin, event.resourceType);
            }
            await cdp.send(allowed ? "Fetch.continueRequest" : "Fetch.failRequest", allowed ? { requestId: event.requestId } : { requestId: event.requestId, errorReason: "BlockedByClient" });
        });
        await cdp.send("Runtime.enable");
        await cdp.send("Fetch.enable", { patterns: [{ urlPattern: "*", requestStage: "Request" }] });
        await cdp.send("Emulation.setDeviceMetricsOverride", { width: input.width, height: input.height, deviceScaleFactor: 1, mobile: false });
        const evaluate = async (expression) => {
            signal.throwIfAborted();
            if (browserError) throw new Error(browserError);
            const result = await cdp.send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
            if (result.exceptionDetails) {
                if (process.env.PREVIS_RENDER_DIAGNOSTICS) console.error("viewport evaluation", String(result.exceptionDetails.exception?.description || result.exceptionDetails.text || "").split("\n")[0].slice(0, 300));
                throw new Error("scene_script_error");
            }
            return result.result.value;
        };
        await cdp.send("Page.navigate", { url: `${baseURL}/previs-render.html` });
        const readyDeadline = Date.now() + 45000;
        while (!await evaluate("Boolean(window.previsRenderer)")) { if (Date.now() > readyDeadline) throw new Error("viewport_load_timeout"); await delay(100, signal); }
        await evaluate(`window.previsRenderer.load(${JSON.stringify(input.scene)},${JSON.stringify(input.shotId)},${input.duration})`);
        while (true) {
            const state = await evaluate("window.previsRenderer.state()");
            if (state.error) throw new Error(state.error);
            if (state.ready) break;
            if (Date.now() > readyDeadline) {
                if (process.env.PREVIS_RENDER_DIAGNOSTICS) console.error("viewport readiness timeout",state);
                throw new Error("asset_load_timeout");
            }
            await delay(100, signal);
        }
        return { frame: (time) => evaluate(`window.previsRenderer.frame(${time})`), close };
    } catch (error) {
        if(process.env.PREVIS_RENDER_DIAGNOSTICS) console.error("chromium diagnostic",chromeErrors);
        try { await close(); } catch { /* Cleanup must not hide the actual render failure. */ }
        throw error;
    }
}
