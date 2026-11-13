"""Run the actual docs image behind the repository gateway in a disposable network."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]

def run(*args):
    return subprocess.check_output(["docker", *args], text=True, encoding="utf-8", stderr=subprocess.STDOUT).strip()

def get(url):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(url, timeout=10) as response:
        return response.status, response.read().decode("utf-8")

def wait(url):
    for _ in range(120):
        try:
            return get(url)
        except (urllib.error.URLError, TimeoutError, OSError):
            time.sleep(.5)
    raise AssertionError("Docs gateway did not become ready")

def smoke(image, output):
    project = "canvas-docs-smoke-" + uuid.uuid4().hex[:12]
    names = [project + "-docs", project + "-backend", project + "-gateway"]
    created = []
    network = False
    report = {"project": project, "image": image, "checks": []}
    def check(name, condition):
        report["checks"].append({"name": name, "passed": bool(condition)})
        if not condition:
            raise AssertionError(name)
    try:
        run("network", "create", project)
        network = True
        run("run", "-d", "--name", names[0], "--network", project, "--network-alias", "docs", image)
        created.append(names[0])
        sentinel = "require('http').createServer((q,s)=>{s.setHeader('Content-Type','application/json');s.end(JSON.stringify({status:'sentinel'}))}).listen(8080,'0.0.0.0')"
        run("run", "-d", "--name", names[1], "--network", project, "--network-alias", "backend", "node:22.19.0-alpine", "node", "-e", sentinel)
        created.append(names[1])
        run("run", "-d", "--name", names[2], "--network", project, "-p", "127.0.0.1::3000",
            "-v", str(ROOT / "nginx.conf") + ":/etc/nginx/conf.d/default.conf:ro", "nginx:1.27-alpine")
        created.append(names[2])
        port = run("port", names[2], "3000/tcp").rsplit(":", 1)[1]
        base = "http://127.0.0.1:" + port
        _, body = wait(base + "/docs/backend/creation-platform-sdk")
        health = json.loads(get(base + "/docs/api/health")[1])
        check("compiled health identity", health["status"] == "ok" and health["deploymentContract"] == 1 and bool(health["buildId"]))
        check("real SDK body", "SDK" in body and "<html" in body.lower())
        check("single public prefix", "/docs/docs" not in body)
        entry = get(base + "/docs")[1]
        check("documentation entry", "<html" in entry.lower())
        results = json.loads(get(base + "/docs/api/search?query=SDK")[1])
        check("real full-text search", isinstance(results, list) and len(results) > 0)
        assets = list(dict.fromkeys(re.findall(r'(?:src|href)="(/docs/_next/[^"]+)', body)))
        check("compiled assets referenced", len(assets) > 0)
        for asset in assets[:8]:
            check("asset " + asset, get(base + asset)[0] == 200)
        try:
            get(base + "/docs/this-page-does-not-exist")
            check("unknown document is 404", False)
        except urllib.error.HTTPError as error:
            check("unknown document is 404", error.code == 404)
        backend_before = run("inspect", names[1], "--format", "{{.Id}}")
        run("stop", names[0])
        check("business API survives docs outage", json.loads(get(base + "/api/health")[1])["status"] == "sentinel")
        check("business container untouched", backend_before == run("inspect", names[1], "--format", "{{.Id}}"))
        # Recreate docs to exercise DNS changes without restarting the gateway.
        run("rm", names[0])
        run("run", "-d", "--name", names[0], "--network", project, "--network-alias", "docs", image)
        wait(base + "/docs/api/health")
        check("docs recovered without gateway restart", json.loads(get(base + "/docs/api/health")[1])["buildId"] == health["buildId"])
    finally:
        for name in reversed(created):
            subprocess.run(["docker", "rm", "-f", name], capture_output=True)
        if network:
            subprocess.run(["docker", "network", "rm", project], capture_output=True)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(report, ensure_ascii=False))

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--image", required=True)
    parser.add_argument("--output", type=Path, default=ROOT / ".local/cache/verify/docs-smoke.json")
    args = parser.parse_args()
    smoke(args.image, args.output)
