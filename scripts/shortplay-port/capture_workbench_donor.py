#!/usr/bin/env python3
"""Capture the W01 shortplay workbench inventory from the pinned donor commit.

This collector only reads the donor tree. It never runs donor code, so every
inventory row it writes is `source-observed`. The one thing it validates is the
replayable two-episode sample in `testdata/shortplay-workbench-port/sample/`,
against rules quoted from the donor source at the pinned commit.

Usage:
  python scripts/shortplay-port/capture_workbench_donor.py \
    --donor-root .local/donor-13731aae6 \
    --out testdata/shortplay-workbench-port/donor
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path


DONOR_COMMIT = "13731aae6c4cefadc2a1719e3e7e59b22e249f94"
SAMPLE_REL = Path("testdata/shortplay-workbench-port/sample/two-episodes.json")

ROUTES_FILE = "backend/internal/handler/shortplay_routes.go"
MODEL_FILE = "backend/internal/shortplay/model.go"
EXECUTOR_FILE = "backend/internal/shortplay/executor.go"
ORCHESTRATOR_FILE = "backend/internal/shortplay/orchestrator.go"
POLICY_FILE = "backend/internal/agent/tool/policy.go"
MIGRATIONS_FILE = "backend/internal/database/migrations.go"
DERIVED_MIGRATIONS_FILE = "backend/internal/database/migrations_shortplay_derived.go"
PROJECT_FILE = "backend/internal/app/project.go"
SERVICE_FILE = "backend/internal/app/service.go"
PROMPTS_FILE = "backend/internal/shortplay/prompts.go"
ASSET_IMAGE_FILE = "backend/internal/app/shortplay_asset_image.go"
PRODUCTION_PROMPTS_FILE = "backend/internal/production/prompts.go"
FIELD_BRIDGE_FILE = "backend/internal/canvas/capability/builtin.go"
ROUTER_FILE = "web/src/router.tsx"
NAV_FILE = "web/src/components/layout/workspace-sidebar-nav.tsx"
WEB_API_FILE = "web/src/services/api/shortplay.ts"
WEB_SHORTPLAY_DIR = "web/src/shortplay"

# GORM default plural tables created by donor migrations 88/91/92/110/111.
TABLES = [
    "shortplays",
    "shortplay_episodes",
    "shortplay_assets",
    "shortplay_jobs",
    "shortplay_asset_images",
    "shortplay_asset_audios",
]

MIGRATIONS = [
    (88, "shortplay_tables", "migrateSchemaV88"),
    (91, "shortplay_asset_voice", "migrateShortplayAssetVoice"),
    (92, "shortplay_asset_int_id", "migrateShortplayAssetIntID"),
    (110, "shortplay_derived_asset", "migrateShortplayDerivedAsset"),
    (111, "shortplay_asset_audio_status", "migrateShortplayAssetAudioStatus"),
]

JOB_DISPATCH = {
    "extract": "backend/internal/app/shortplay_extract.go",
    "asset_image": "backend/internal/app/shortplay_asset_image.go",
    "asset_audio": "backend/internal/app/shortplay_asset_audio.go",
    "asset_sync": "backend/internal/app/shortplay_asset_sync.go",
}

WEB_ROUTES = [
    ("/shortplays", "list.tsx", "列作品"),
    ("/shortplays/new", "create.tsx", "建档（两集原文一次性提交）"),
    ("/shortplays/:projectId", "detail.tsx", "详情/分集/资产/设置/收件箱"),
]

AGENT_TOOLS = {
    "ShortplayListAssets": "shortplay_list_assets",
    "ShortplayAsset": "shortplay_asset",
    "ShortplayAssetImage": "shortplay_asset_image",
    "ShortplayAssetAudio": "shortplay_asset_audio",
}


def die(message: str) -> None:
    raise SystemExit(message)


def read_text(root: Path, rel: str) -> str:
    path = root / rel
    if not path.exists():
        die(f"donor file missing: {path}")
    return path.read_text(encoding="utf-8")


def git(root: Path, *args: str) -> str:
    result = subprocess.run(
        ("git", *args), cwd=root, text=True, capture_output=True, check=False
    )
    if result.returncode != 0:
        die(f"git {' '.join(args)} failed: {result.stderr.strip()}")
    return result.stdout.strip()


def parse_routes(text: str) -> list[dict]:
    pattern = re.compile(
        r'r\.(GET|POST|PATCH|PUT|DELETE)\("([^"]+)",\s*dramaRoute\(svc,\s*([0-9]+(?:<<[0-9]+)?|0)'
    )
    matches = list(pattern.finditer(text))
    routes = []
    for index, match in enumerate(matches):
        method, path, raw_limit = match.groups()
        if "<<" in raw_limit:
            base, shift = raw_limit.split("<<")
            limit = int(base) << int(shift)
        else:
            limit = int(raw_limit)
        block_start = matches[index - 1].end() if index else 0
        preceding = text[block_start:match.start()].splitlines()
        comments: list[str] = []
        for line in reversed(preceding):
            stripped = line.strip()
            if not stripped:
                if comments:
                    break
                continue
            if not stripped.startswith("//"):
                break
            comments.append(stripped.lstrip("/ ").strip())
        comments.reverse()
        block = text[match.end():]
        next_route = block.find("\n\tr.")
        if next_route != -1:
            block = block[:next_route]
        calls = re.findall(r"svc\.([A-Z][A-Za-z]+)\(", block)
        routes.append(
            {
                "method": method,
                "path": "/api" + path,
                "bodyLimitBytes": limit,
                "service": calls[0] if calls else None,
                "note": comments[0] if comments else "",
            }
        )
    return routes


def parse_agent_tools(text: str) -> list[dict]:
    tools = []
    for match in re.finditer(r"\{Name: (Shortplay\w+),([^}]*)\}", text):
        body = match.group(2)
        tools.append(
            {
                "const": match.group(1),
                "requiresDeclaration": "RequiresDeclaration: true" in body,
                "requiresShortplay": "RequiresShortplay: true" in body,
                "requiresCanvas": "RequiresCanvas: true" in body,
                "kind": (re.search(r"Kind: (Kind\w+)", body) or [None, None])[1],
            }
        )
    return tools


def parse_capability_fields(text: str) -> list[dict]:
    fields = []
    pattern = re.compile(
        r'fields\["(shortplayAsset\w+)"\] = Field\{\s*Path: "([^"]+)", Kind: (Kind\w+), Label: "([^"]+)"'
    )
    for name, path, kind, label in pattern.findall(text):
        fields.append({"name": name, "path": path, "kind": kind, "label": label})
    return fields


def read_settings_schema(root: Path) -> dict[str, str]:
    text = read_text(root, PRODUCTION_PROMPTS_FILE)
    start = text.find("type Settings struct")
    if start == -1:
        die("donor production.Settings struct not found")
    body = text[start:text.find("\n}", start)]
    schema = {}
    for go_type, tag in re.findall(r"(\w+)\s+\w+\s+`json:\"([^\"]+)\"`", body):
        schema[tag.split(",")[0]] = go_type
    return schema


def resolve_migration_version(text: str, version: int, name: str) -> int | None:
    """Resolve a migration entry whose version may be a named constant."""
    direct = re.search(rf'version: {version}, name: "{re.escape(name)}"', text)
    if direct:
        return version
    indirect = re.search(rf'version: (\w+), name: "{re.escape(name)}"', text)
    if not indirect:
        return None
    const_name = indirect.group(1)
    const = re.search(rf'{re.escape(const_name)}\s+int64\s*=\s*(\d+)', text)
    return int(const.group(1)) if const else None


def validate_sample(sample_path: Path) -> list[str]:
    """Check the sample against donor rules quoted at the pinned commit."""
    sample = json.loads(sample_path.read_text(encoding="utf-8"))
    problems: list[str] = []

    def fail(message: str) -> None:
        problems.append(message)

    if sample.get("schema") != "shortplay-workbench-sample/v1":
        fail("sample schema must be shortplay-workbench-sample/v1")
    provenance = sample.get("provenance") or {}
    if provenance.get("donorCommit") != DONOR_COMMIT:
        fail("sample provenance.donorCommit does not match the pinned donor commit")
    if provenance.get("kind") != "source-observed":
        fail("sample provenance.kind stays source-observed until W02 replay")

    project = sample.get("project") or {}
    if project.get("type") != "shortplay":
        fail("project.type must be shortplay")
    if project.get("aspectRatio") not in {"9:16", "16:9", "1:1", "3:4", "4:3"}:
        fail("project.aspectRatio is outside donor AspectRatios")

    episodes = sample.get("episodes") or []
    if [episode.get("number") for episode in episodes] != [1, 2]:
        fail("sample must hold exactly two episodes numbered 1 and 2")
    canvas_ids = [episode.get("canvasId") for episode in episodes]
    if len(set(canvas_ids)) != len(canvas_ids) or any(not value for value in canvas_ids):
        fail("each episode needs its own non-empty canvasId")

    assets = sample.get("assets") or []
    by_id: dict[int, dict] = {}
    for asset in assets:
        asset_id = asset.get("id")
        if not isinstance(asset_id, int) or asset_id <= 0:
            fail("asset ids are project-scoped positive integers starting at 1")
            continue
        if asset_id in by_id:
            fail(f"asset id {asset_id} is duplicated")
        by_id[asset_id] = asset
    if sorted(by_id) != list(range(1, len(by_id) + 1)):
        fail("asset ids must be contiguous from 1 inside one project")

    bases = [asset for asset in assets if not asset.get("derivedFrom")]
    derived = [asset for asset in assets if asset.get("derivedFrom")]
    if len(bases) != 1 or bases[0].get("kind") != "character":
        fail("sample must hold exactly one base character asset")
    if len(derived) != 1:
        fail("sample must hold exactly one derived look")
    if bases and derived:
        base, look = bases[0], derived[0]
        if look.get("derivedFrom") != base.get("id"):
            fail("derived look must point at the shared base character")
        variant = (look.get("variantName") or "").strip()
        if not variant:
            fail("derived look needs variantName")
        if "-" in variant:
            fail("variantName must not contain the display-name separator '-'")
        display = f"{base.get('name', '').strip()}-{variant}"
        if len(display) > 20:
            fail(f"derived display name {display!r} exceeds 20 runes")
        if look.get("name"):
            fail("derived rows do not own name: the read model synthesises it")
        if look.get("voice"):
            fail("derived rows fall back to the parent voice when empty")
        if not look.get("appearance"):
            fail("appearance is the one field a derived look must supply")

    expectations = sample.get("expectations") or {}
    look_expect = expectations.get("derivedLook") or {}
    if bases and derived:
        base, look = bases[0], derived[0]
        if look_expect.get("assetId") != look.get("id"):
            fail("expectations.derivedLook.assetId must be the derived row id")
        if look_expect.get("displayName") != f"{base.get('name')}-{look.get('variantName')}":
            fail("expectations.derivedLook.displayName must be parent-variant")
        if look_expect.get("resolvedVoice") != base.get("voice"):
            fail("expected resolved voice must fall back to the parent")
        if look_expect.get("resolvedDescription") != base.get("description"):
            fail("expected resolved description must fall back to the parent")
        if look_expect.get("baseImageStale") is not False:
            fail("sample baseline expects a fresh derived image")
        for entry in expectations.get("resolveLookNames") or []:
            if "assetId" not in entry or not isinstance(entry["assetId"], int):
                fail("every resolveLookNames entry needs an integer assetId (0 = base row)")
    episode_assets = expectations.get("episodeAssetIds") or {}
    if sorted(episode_assets.get("1") or []) != [1]:
        fail("episode 1 must reference only the shared base character")
    if sorted(episode_assets.get("2") or []) != [1, 2]:
        fail("episode 2 must reference the base character and its derived look")

    for job in sample.get("jobs") or []:
        kind = job.get("kind")
        if kind not in JOB_DISPATCH:
            fail(f"job kind {kind!r} is not one of the donor kinds")
            continue
        parts = (job.get("dedupeKey") or "").split("|")
        allowed = {"extract": 2, "asset_image": 3, "asset_audio": 3, "asset_sync": 2}[kind]
        if len(parts) != allowed or parts[0] != kind:
            fail(f"dedupeKey {job.get('dedupeKey')!r} does not follow the {kind} form")
        if kind in {"asset_image", "asset_audio"} and len(parts) == 3:
            if parts[1] != (sample.get("shortplay") or {}).get("id"):
                fail(f"{kind} dedupe key must carry the shortplay id")

    unfinished = [
        job for job in sample.get("jobs") or []
        if job.get("status") in {"queued", "running"}
    ]
    keys = [job.get("dedupeKey") for job in unfinished]
    if len(keys) != len(set(keys)):
        fail("unfinished jobs must not repeat a dedupe key")

    return problems


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--donor-root", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--sample", default=str(SAMPLE_REL))
    parser.add_argument("--allow-other-commit", action="store_true")
    args = parser.parse_args()

    donor = Path(args.donor_root).resolve()
    out = Path(args.out).resolve()
    sample_path = Path(args.sample)
    if not sample_path.is_absolute():
        sample_path = Path(__file__).resolve().parents[2] / sample_path

    if not (donor / "backend/go.mod").exists():
        die(f"donor backend not found: {donor}")
    head = git(donor, "rev-parse", "HEAD")
    if not args.allow_other_commit and head != DONOR_COMMIT:
        die(f"donor HEAD is {head}, expected {DONOR_COMMIT}")
    if git(donor, "status", "--porcelain"):
        die("donor worktree is dirty; refusing to capture")

    route_text = read_text(donor, ROUTES_FILE)
    model_text = read_text(donor, MODEL_FILE)
    executor_text = read_text(donor, EXECUTOR_FILE)
    orchestrator_text = read_text(donor, ORCHESTRATOR_FILE)
    policy_text = read_text(donor, POLICY_FILE)
    migrations_text = read_text(donor, MIGRATIONS_FILE)
    derived_text = read_text(donor, DERIVED_MIGRATIONS_FILE)
    project_text = read_text(donor, PROJECT_FILE)
    service_text = read_text(donor, SERVICE_FILE)
    asset_image_text = read_text(donor, ASSET_IMAGE_FILE)
    bridge_text = read_text(donor, FIELD_BRIDGE_FILE)
    router_text = read_text(donor, ROUTER_FILE)
    nav_text = read_text(donor, NAV_FILE)
    api_text = read_text(donor, WEB_API_FILE)

    routes = parse_routes(route_text)
    if len(routes) != 13:
        die(f"expected 13 shortplay routes, captured {len(routes)}")

    job_kinds = dict(
        re.findall(
            r"(ShortplayJob(?:Extract|AssetImage|AssetAudio|AssetSync))\s+=\s+\"([a-z_]+)\"",
            model_text,
        )
    )
    statuses = dict(
        re.findall(
            r"(ShortplayJob(?:Queued|Running|Succeeded|Failed|Canceled))\s+=\s+\"([a-z]+)\"",
            model_text,
        )
    )
    image_states = dict(
        re.findall(
            r"(ShortplayImage(?:Queued|Running|Ready|Failed))\s+=\s+\"([a-z]+)\"",
            model_text,
        )
    )
    if len(job_kinds) != 4 or len(statuses) != 5 or len(image_states) != 4:
        die("donor job/status constants changed shape; re-read model.go")

    priorities = {
        name: int(value)
        for name, value in re.findall(r"(Shortplay\w+Priority)\s+=\s+(\d+)", executor_text)
    }
    scheduler = {
        name: int(value)
        for name, value in re.findall(
            r"(shortplay(?:TickInterval|LeaseTTL|RunsPerTick|Concurrency))[^\n=]*=\s*([0-9]+)",
            orchestrator_text,
        )
    }

    tools = parse_agent_tools(policy_text)
    if {tool["const"] for tool in tools} != set(AGENT_TOOLS):
        die(f"donor shortplay agent tool set changed: {[t['const'] for t in tools]}")
    for tool in tools:
        tool["name"] = AGENT_TOOLS[tool["const"]]

    fields = parse_capability_fields(bridge_text)
    if not fields:
        die("donor shortplay-asset capability fields not found")

    migrations = []
    combined = migrations_text + derived_text
    for version, name, apply in MIGRATIONS:
        resolved = resolve_migration_version(combined, version, name)
        if resolved != version:
            die(f"migration {version} {name} not found as version {resolved} in donor database package")
        migrations.append({"version": version, "name": name, "apply": apply})

    web_routes = []
    for path, page, action in WEB_ROUTES:
        if f'path: "{path}"' not in router_text:
            die(f"web route {path} not found in donor router")
        if not (donor / WEB_SHORTPLAY_DIR / "pages" / page).exists():
            die(f"web page {page} not found")
        web_routes.append({"path": path, "page": page, "action": action})
    if "/shortplays" not in nav_text:
        die("shortplay entry missing from donor sidebar nav")

    web_api = re.findall(r"export function (\w+)\(", api_text)
    web_components = sorted(
        str(path.relative_to(donor / WEB_SHORTPLAY_DIR)).replace("\\", "/")
        for path in (donor / WEB_SHORTPLAY_DIR).rglob("*.ts*")
    )

    if 'project.Type == "shortplay"' not in project_text:
        die("donor project cascade for type shortplay not found")
    if "startShortplayScheduler" not in service_text:
        die("donor scheduler startup not found")

    render_rule = re.search(r'shortplayDefaultRenderRule = "([^"]+)"', asset_image_text)
    if not render_rule:
        die("donor default render rule not found")

    inventory = {
        "provenance": {
            "donorCommit": head,
            "capturedAt": datetime.now(timezone.utc).isoformat(timespec="seconds"),
            "kind": "source-observed",
            "collector": "scripts/shortplay-port/capture_workbench_donor.py",
            "note": "The collector only reads the pinned donor tree; no donor code was executed.",
        },
        "http": {"routeCount": len(routes), "routes": routes},
        "project": {
            "type": "shortplay",
            "cascadeDeleteOfShortplayTables": True,
            "webRoutes": web_routes,
            "webSidebarEntry": True,
            "webModules": web_components,
            "webApiFunctions": web_api,
        },
        "jobs": {
            "kinds": job_kinds,
            "statuses": statuses,
            "priorities": priorities,
            "scheduler": scheduler,
            "imageStates": image_states,
            "dedupeForms": [
                "extract|shortplayId",
                "asset_image|shortplayId|assetId",
                "asset_audio|shortplayId|assetId",
                "asset_sync|canvasId",
            ],
            "dispatch": JOB_DISPATCH,
            "uniqueIndexScope": "unfinished jobs only (queued, running)",
        },
        "agent": {
            "tools": tools,
            "extraction": "RequiresDeclaration + RequiresShortplay; not part of the base tool set",
        },
        "tables": TABLES,
        "migrations": migrations,
        "capability": {"nodeType": "shortplay-asset", "fields": fields},
        "prompts": {
            "file": PROMPTS_FILE,
            "extractSystemPrompt": "ExtractSystemPrompt",
            "extractPrompt": "ExtractPrompt",
            "defaultRenderRule": render_rule.group(1),
            "settingsSchema": read_settings_schema(donor),
        },
        "evidence": {
            "source-observed": "every row above; captured from the pinned donor tree",
            "runtime": [],
            "pending": [
                "real model-channel extract/image/audio runs and receipts",
                "PostgreSQL upgrade of the donor shortplay schema",
                "browser walkthrough of the donor pages",
            ],
        },
    }

    out.mkdir(parents=True, exist_ok=True)
    (out / "inventory.json").write_text(
        json.dumps(inventory, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )

    problems = validate_sample(sample_path)
    if problems:
        for problem in problems:
            print(f"SAMPLE ERROR: {problem}", file=sys.stderr)
        return 1

    print(f"donor HEAD: {head}")
    print(f"routes: {len(routes)} jobs: {len(job_kinds)} agent tools: {len(tools)}")
    print(f"tables: {len(TABLES)} migrations: {[m['version'] for m in migrations]}")
    print(f"inventory: {out / 'inventory.json'}")
    print(f"sample: {sample_path} OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
