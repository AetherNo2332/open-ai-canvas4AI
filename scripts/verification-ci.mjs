import { spawnSync } from "node:child_process";
import { readFileSync, writeFileSync, appendFileSync } from "node:fs";
import { collectPlan } from "./verification-plan.mjs";
import { partitionChecks } from "./verification-execution.mjs";

const root = process.cwd();
let base = process.env.BASE_SHA;
let mode =
  process.env.GITHUB_EVENT_NAME === "pull_request" ? "affected" : "full";
if (!base || /^0+$/.test(base)) {
  const r = spawnSync("git", ["rev-parse", "HEAD^"], {
    encoding: "utf8",
    windowsHide: true,
  });
  if (r.status !== 0) throw new Error("No trusted parent for full regression");
  base = r.stdout.trim();
  mode = "full";
  console.log("Missing/zero event base: explicit first-parent full regression");
}
const plan = collectPlan({
  root,
  base,
  head: process.env.GITHUB_SHA || "HEAD",
  mode,
});
writeFileSync("verification-plan.json", JSON.stringify(plan, null, 2));
const groups = [
  ["backend-rest", "Backend tests (rest)"],
  ...Array.from({ length: 4 }, (_, i) => [
    "backend-app-" + (i + 1),
    "Backend tests (app-" + (i + 1) + ")",
  ]),
  ["web", "Web checks"],
  ["agent", "Agent checks"],
  ["payment", "Payment plugin artifacts"],
  ["smoke", "Production Agent Compose smoke"],
  ["docs", "Docs build"],
  ["meta", "Changelog entry"],
];
const include = groups.map(([partition, label]) => ({
  partition,
  label,
  active: partitionChecks(plan, partition).length > 0,
  backend: partition.startsWith("backend"),
}));
if (process.env.GITHUB_OUTPUT)
  appendFileSync(
    process.env.GITHUB_OUTPUT,
    `matrix=${JSON.stringify({ include })}\nfullBackend=${plan.selectedChecks.some((c) => c.id === "backend:full")}\n`,
  );
console.log(
  "Plan: " +
    plan.selectedChecks.length +
    " checks; " +
    include.filter((g) => g.active).length +
    " active partitions",
);
