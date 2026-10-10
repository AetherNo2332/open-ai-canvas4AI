import { createServer } from "node:http";
import { createReadStream } from "node:fs";
import { mkdir, writeFile, stat, rm } from "node:fs/promises";
import { join, resolve, sep, extname } from "node:path";
import { spawn } from "node:child_process";
import { pipeline } from "node:stream/promises";
import { openChromium } from "./chromium.mjs";

function run(binary, args, signal) {
    return new Promise((resolve, reject) => {
        signal.throwIfAborted();
        const child = spawn(binary, args, { stdio: ["ignore", "pipe", "pipe"], windowsHide: true, signal });
        let stdout = "";
        let stderr = "";
        child.stdout.on("data", (data) => { if (stdout.length < 65536) stdout += data.toString(); });
        child.stderr.on("data", (data) => { stderr = (stderr + data.toString()).slice(-2000); });
        child.on("error", () => reject(new Error("encoding_unavailable")));
        child.on("exit", (code) => {
            if (code === 0) resolve(stdout);
            else {
                if (process.env.PREVIS_RENDER_DIAGNOSTICS) console.error("encoding diagnostic", stderr);
                reject(new Error("encoding_failed"));
            }
        });
    });
}

export async function probeVideo(path, input, signal) {
    const raw = await run(process.env.FFPROBE_PATH || "ffprobe", ["-v", "error", "-select_streams", "v:0", "-count_frames", "-show_entries", "stream=codec_name,width,height,nb_read_frames:format=duration", "-of", "json", path], signal);
    let probe;
    try { probe = JSON.parse(raw); } catch { throw new Error("invalid_video"); }
    const stream = probe.streams?.[0];
    const count = Math.ceil(input.duration * input.fps);
    const duration = Number(probe.format?.duration);
    const bytes = (await stat(path)).size;
    if (stream?.codec_name !== "h264" || stream.width !== input.width || stream.height !== input.height || Number(stream.nb_read_frames) !== count || !Number.isFinite(duration) || Math.abs(duration - count / input.fps) > 0.15 || bytes < 100 || bytes > 128 * 1024 * 1024) {
        if (process.env.PREVIS_RENDER_DIAGNOSTICS) console.error("probe diagnostic", { stream, duration, bytes, expected: { width: input.width, height: input.height, count } });
        throw new Error("invalid_video");
    }
    return { width: stream.width, height: stream.height, durationMs: Math.round(duration * 1000), frameCount: count, size: bytes };
}

async function staticServer(directory, publicDir, modelDir, input) {
    const server = createServer(async (request, response) => {
        try {
            if (request.method !== "GET") { response.writeHead(405).end(); return; }
            const pathname = decodeURIComponent(new URL(request.url, "http://localhost").pathname);
            if (pathname === "/favicon.ico") { response.writeHead(204).end(); return; }
            let path;
            let root;
            if (pathname.startsWith("/job-assets/")) {
                const name = pathname.slice(12);
                if (!Object.hasOwn(input.assets, name)) { response.writeHead(404).end(); return; }
                root = join(directory, "assets"); path = resolve(root, name);
            } else if (pathname === "/models/Xbot.glb") {
                root = resolve(modelDir); path = join(root, "Xbot.glb");
            } else {
                root = resolve(publicDir); path = resolve(root, "." + pathname);
            }
            if (!path.startsWith(resolve(root) + sep) || !(await stat(path)).isFile()) { response.writeHead(404).end(); return; }
            const types = { ".html": "text/html", ".js": "application/javascript", ".css": "text/css", ".png": "image/png", ".glb": "model/gltf-binary" };
            response.writeHead(200, { "Content-Type": types[extname(path)] || "application/octet-stream", "Cache-Control": "no-store" });
            await pipeline(createReadStream(path), response);
        } catch { if (!response.headersSent) response.writeHead(404); response.end(); }
    });
    await new Promise((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
    return { url: `http://127.0.0.1:${server.address().port}`, close: () => new Promise((resolve) => { server.closeAllConnections(); server.close(resolve); }) };
}

export async function renderJob(job, directory, options, signal, progress) {
    const input = job.input;
    const framesDir = join(directory, "frames");
    await mkdir(framesDir, { recursive: true });
    const files = await staticServer(directory, options.publicDir, options.modelDir, input);
    let browser;
    try {
        await progress("loading", 0);
        browser = await openChromium(directory, files.url, input, signal);
        const count = Math.ceil(input.duration * input.fps);
        let frameBytes = 0;
        for (let index = 0; index < count; index++) {
            signal.throwIfAborted();
            const value = await browser.frame(index / input.fps);
            if (typeof value !== "string" || value.length > 16 * 1024 * 1024) throw new Error("invalid_frame");
            const frame = Buffer.from(value, "base64");
            if (!frame.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))) throw new Error("invalid_frame");
            frameBytes += frame.length;
            if (frameBytes > 1024 * 1024 * 1024) throw new Error("frame_quota_exceeded");
            await writeFile(join(framesDir, `frame-${String(index).padStart(6, "0")}.png`), frame);
            if (index === 0) await writeFile(join(directory, "preview.png"), frame);
            if (index % 10 === 0 || index + 1 === count) await progress("rendering", index + 1);
        }
        await browser.close(); browser = null;
        await progress("encoding", count);
        const output = join(directory, "video.mp4");
        await run(process.env.FFMPEG_PATH || "ffmpeg", ["-hide_banner", "-loglevel", "error", "-y", "-framerate", String(input.fps), "-i", join(framesDir, "frame-%06d.png"), "-frames:v", String(count), "-an", "-c:v", "libx264", "-preset", "fast", "-crf", "23", "-pix_fmt", "yuv420p", "-movflags", "+faststart", output], signal);
        const metadata = await probeVideo(output, input, signal);
        metadata.previewSize = (await stat(join(directory, "preview.png"))).size;
        return metadata;
    } finally {
        await browser?.close();
        await files.close();
        if (!resolve(framesDir).startsWith(resolve(directory) + sep)) throw new Error("invalid_frames_path");
        await rm(framesDir, { recursive: true, force: true });
    }
}
