import { spawnSync } from "node:child_process";
const shard = process.argv[2];
function capture(args) {
  const r = spawnSync("go", args, {
    encoding: "utf8",
    windowsHide: true,
    maxBuffer: 32 * 1024 * 1024,
  });
  if (r.status !== 0) throw new Error("Go package/test discovery failed");
  return r.stdout.trim().split(/\r?\n/);
}
try {
  let args;
  if (shard === "rest") {
    const packages = capture(["list", "./..."]).filter(
      (p) => p !== "yingce/backend/internal/app",
    );
    if (!packages.length) throw new Error("No backend packages");
    args = ["test", "-json", "-count=1", "-timeout=20m", ...packages];
  } else {
    const index = Number(shard);
    if (![1, 2, 3, 4].includes(index)) throw new Error("invalid app shard");
    const names = capture(["test", "-list", ".", "./internal/app"])
      .filter((n) => /^(Test|Example|Fuzz)[A-Za-z0-9_]*$/.test(n))
      .sort()
      .filter((_, i) => i % 4 === index - 1);
    if (!names.length) throw new Error("Empty app test shard");
    args = [
      "test",
      "-json",
      "-count=1",
      "-timeout=20m",
      "-run",
      `^(${names.join("|")})$`,
      "./internal/app",
    ];
  }
  const r = spawnSync("go", args, { stdio: "inherit", windowsHide: true });
  process.exitCode = r.status ?? 1;
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
