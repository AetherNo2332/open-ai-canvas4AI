import { execFileSync } from "node:child_process";
import { readFileSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// Small real decodable files exercise resource saving/probing and browser players.
let directory;
let initialized = false;
function ensureFiles() {
  if (initialized) return;
  directory = mkdtempSync(join(tmpdir(), "canvas-media-fixture-"));
  // Fixed 320x180 RGB PNG; independent of optional FFmpeg image encoders.
  writeFileSync(
    `${directory}/image.png`,
    Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAUAAAAC0CAIAAABqhmJGAAABk0lEQVR42u3TAQkAAAzDsBm9gdu+kesYBKKg0NwsUCoSgIEBAwMGBgMDBgYMDBgYDAwYGDAwGBgwMGBgwMBgYMDAgIEBA4OBAQMDBgYDAwYGDAwYGAwMGBgwMGBgMDBgYMDAYGDAwICBAQODgQEDAwYGA6sABgYMDBgYDAwYGDAwYGAwMGBgwMBgYMDAgIEBA4OBAQMDBgYMDAYGDAwYGAwMGBgwMGBgMDBgYMDAgIHBwICBAQODgQEDAwYGDAwGBgwMGBgMDBgYMDBgYDAwYGDAwICBwcCAgQEDg4EBAwMGBgwMBgYMDBgYMDAYGDAwYGAwMGBgwMCAgcHAgIEBAwMGBgMDBgYMDAYGDAwYGDAwGBgwMGBgMDBgYMDAgIHBwICBAQMDBgYDAwYGDAwGBgwMGBgwMBgYMDBgYMDAYGDAwICBwcCAgQEDAwYGAwMGBgwMBlYBDAwYGDAwGBgwMGBgwMBgYMDAgIHBwICBAQMDBgYDAwYGDAwYGAwMGBgwMBgYMDBgYMDAYGDAwICBAQNDtwffEn/rxP1E3wAAAABJRU5ErkJggg==",
      "base64",
    ),
  );
  for (const [name, args] of [
    [
      "video.mp4",
      [
        "-f",
        "lavfi",
        "-i",
        "testsrc2=size=320x180:rate=12",
        "-t",
        "1",
        "-c:v",
        "libx264",
        "-pix_fmt",
        "yuv420p",
      ],
    ],
    [
      "audio.wav",
      [
        "-f",
        "lavfi",
        "-i",
        "sine=frequency=440:duration=1",
        "-c:a",
        "pcm_s16le",
      ],
    ],
  ])
    execFileSync("ffmpeg", [
      "-loglevel",
      "error",
      "-y",
      ...args,
      `${directory}/${name}`,
    ]);
  initialized = true;
}

let hold = false;
let fail = false;
let count = 0;
const pending = new Set();
const tasks = new Map();
const records = [];
const json = (res, status, body) => {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
};
const url = (name) => `http://model-stub:8080/media/${name}`;

export async function handleMedia(req, res, raw) {
  if (req.url === "/media-control") {
    const body = JSON.parse(raw || "{}");
    hold = body.hold === true;
    fail = body.fail === true;
    if (!hold) {
      for (const resolve of pending) resolve();
      pending.clear();
    }
    json(res, 200, { hold, fail });
    return true;
  }
  if (req.url === "/media-stats") {
    json(res, 200, { requests: records, pending: pending.size });
    return true;
  }
  const name = req.url?.slice("/media/".length);
  if (
    req.method === "GET" &&
    req.url?.startsWith("/media/") &&
    ["image.png", "video.mp4", "audio.wav"].includes(name)
  ) {
    ensureFiles();
    const data = readFileSync(`${directory}/${name}`);
    res.writeHead(200, {
      "Content-Type": {
        "image.png": "image/png",
        "video.mp4": "video/mp4",
        "audio.wav": "audio/wav",
      }[name],
      "Content-Length": data.length,
    });
    res.end(data);
    return true;
  }
  const image = /\/images\/(generations|edits)$/.test(req.url ?? "");
  const audio = req.url?.endsWith("/audio/speech");
  const video = /\/contents\/generations\/tasks(?:\/[^/]+)?$/.test(
    req.url ?? "",
  );
  if (!image && !audio && !video) return false;
  ensureFiles();
  if (req.method === "POST") {
    records.push({ id: ++count, path: req.url });
    if (video) {
      const id = `media-${count}`;
      tasks.set(id, true);
      json(res, 200, { id });
      return true;
    }
    if (hold)
      await new Promise((resolve) => {
        pending.add(resolve);
        res.once("close", () => {
          pending.delete(resolve);
          resolve();
        });
      });
    if (res.destroyed) return true;
    if (fail)
      json(res, 502, { error: { message: "controlled media failure" } });
    else if (image) json(res, 200, { data: [{ url: url("image.png") }] });
    else {
      const data = readFileSync(`${directory}/audio.wav`);
      res.writeHead(200, {
        "Content-Type": "audio/wav",
        "Content-Length": data.length,
      });
      res.end(data);
    }
    return true;
  }
  const id = req.url.split("/").at(-1);
  if (!tasks.has(id)) json(res, 404, { error: "unknown media task" });
  else
    json(res, 200, {
      id,
      status: hold ? "running" : fail ? "failed" : "succeeded",
      ...(fail
        ? { error: { message: "controlled media failure" } }
        : { content: { video_url: url("video.mp4") } }),
    });
  return true;
}
