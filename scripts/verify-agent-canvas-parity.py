"""Run real Pi/Go canvas tools against an isolated deterministic model protocol.

    python scripts/verify-agent-canvas-parity.py --restart

Build/start docker-compose.agent-parity.yml first. Credentials and reports remain
under ignored .local; reports contain IDs, counts and tool receipts, never Cookies.
"""
import argparse
import base64
import http.cookiejar
import json
import secrets
import subprocess
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
COMPOSE = ["docker", "compose", "--env-file", str(ROOT / ".local/agent-parity.env"), "-p", "canvas-agent-parity", "-f", str(ROOT / "docker-compose.agent-parity.yml")]
BUILTINS = ["image", "text", "drawing", "script", "skill", "config", "video", "audio", "frame", "markdown", "svg", "html", "panorama", "compare", "chart", "colorgrade", "media-conversion", "batch-table"]
TERMINAL = {"completed", "failed", "cancelled", "rejected"}


def wait(predicate, seconds=120):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(.3)
    raise AssertionError("runtime acceptance timed out")


def wait_terminal(read, progress, no_progress_seconds=120, total_seconds=300,
                  clock=time.monotonic, sleeper=time.sleep):
    started = last_progress = clock()
    greatest_step = greatest_seq = -1
    while True:
        row = read()
        now = clock()
        step, seq = int(row.get("step", 0)), int(row.get("latestSeq", 0))
        if step > greatest_step or seq > greatest_seq:
            greatest_step, greatest_seq = max(greatest_step, step), max(greatest_seq, seq)
            last_progress = now
            progress(row, round(now - started, 3))
        if now - started >= total_seconds:
            raise AssertionError("runtime terminal wait exceeded 300 second total limit")
        if row["status"] in TERMINAL:
            return row
        if now - last_progress >= no_progress_seconds:
            raise AssertionError("runtime terminal wait had no new step or event for 120 seconds")
        sleeper(.3)


def tool(name, **args):
    return {"tool": name, "args": args}


def apply(*ops, snapshot="$snapshotHash"):
    return tool("canvas_apply_ops", snapshotHash=snapshot, ops=list(ops))


def add(identity, kind="text", content="", **extra):
    return {"type": "add_node", "id": identity, "nodeType": kind, "title": identity, "content": content, **extra}


class Acceptance:
    def __init__(self, args):
        self.args = args
        self.base = "http://127.0.0.1:3040/api"
        self.jar = http.cookiejar.CookieJar()
        self.http = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(self.jar))
        self.report = {"project": "canvas-agent-parity", "port": 3040, "startedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "checks": [], "runs": [], "transientApiRetries": []}
        self.output = ROOT / args.output
        self.output.parent.mkdir(parents=True, exist_ok=True)

    def save(self):
        self.output.write_text(json.dumps(self.report, ensure_ascii=False, indent=2), encoding="utf-8")

    def check(self, name, condition, **evidence):
        self.report["checks"].append({"name": name, "passed": bool(condition), **evidence})
        self.save()
        if not condition:
            raise AssertionError(name)
        print("PASS:", name, flush=True)

    def api(self, method, path, body=None, expected=200, content_type="application/json"):
        data = body if isinstance(body, bytes) else None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(self.base + path, data=data, method=method, headers={"Content-Type": content_type, "Origin": "http://127.0.0.1:3040"})
        try:
            response = self.http.open(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        payload = json.loads(response.read())
        if response.status != expected or (expected == 200 and payload.get("code") != 0):
            raise AssertionError(f"{method} {path}: HTTP {response.status}, code {payload.get('code')}, reason {payload.get('reason')}, message {payload.get('msg')}")
        return payload.get("data", payload)

    def docker(self, *args, input=None):
        result = subprocess.run(COMPOSE + list(args), cwd=ROOT, input=input, capture_output=True, text=True, timeout=180)
        if result.returncode:
            raise AssertionError("isolated Compose command failed: " + result.stderr[-2000:])
        return result.stdout

    def stub(self, path, body=None):
        encoded = base64.b64encode(json.dumps(body).encode()).decode() if body is not None else None
        script = "const r=await fetch('http://127.0.0.1:8080/" + path + "'," + (
            "{method:'POST',headers:{'Content-Type':'application/json'},body:Buffer.from('" + encoded + "','base64')}" if encoded else "{}") + ");if(!r.ok)process.exit(2);console.log(await r.text());"
        return json.loads(self.docker("exec", "-T", "model-stub", "node", "--input-type=module", "-", input=script))

    def init(self):
        # Only this Compose project may be changed by the restart acceptance.
        ids = self.docker("ps", "-q").split()
        containers = json.loads(subprocess.check_output(["docker", "inspect", *ids], text=True))
        self.check("isolated Compose labels and unique images", len(containers) == 4 and all(
            row["Config"]["Labels"].get("com.docker.compose.project") == "canvas-agent-parity" and row["Config"]["Image"].startswith("canvas-agent-parity-") for row in containers),
            services=[{"service": row["Config"]["Labels"]["com.docker.compose.service"], "image": row["Config"]["Image"], "imageId": row["Image"]} for row in containers])
        def ready():
            try:
                health = self.api("GET", "/health")
                return health if health.get("ready") is True else None
            except (OSError, json.JSONDecodeError, AssertionError):
                return None
        self.report["health"] = wait(ready)
        self.check("backend reports isolated rebuilt source marker", self.report["health"].get("ready") is True and self.report["health"].get("build", {}).get("commit", "").endswith("-canvas-agent-parity"))
        with self.http.open(self.base[:-4] + "/canvas", timeout=30) as response:
            html = response.read().decode()
        self.check("built frontend serves the actual canvas route", "/assets/" in html and 'id="root"' in html)
        self.existing_3030 = self.inspect_3030()
        self.report["retained3030"] = self.existing_3030
        account_path = ROOT / ".local/agent-parity-account.json"
        if account_path.exists():
            account = json.loads(account_path.read_text())
            self.api("POST", "/auth/login", account)
        else:
            account = {"username": "parity_" + uuid.uuid4().hex[:12], "password": secrets.token_urlsafe(24)}
            self.api("POST", "/auth/register", {**account, "acceptedTerms": True})
            account_path.write_text(json.dumps(account), encoding="utf-8")
        channels = self.api("GET", "/admin/channels")["channels"]
        channel = next((row for row in channels if row["name"] == "Isolated canvas parity stub"), None)
        if channel is None:
            channel = self.api("POST", "/admin/channels", {"name": "Isolated canvas parity stub", "baseUrl": "http://model-stub:8080/v1", "apiKey": "non-secret-test-key", "models": ["agent-parity-model"], "enabled": True})["channel"]
        self.channel = channel["id"]
        models = self.api("GET", f"/admin/channels/{self.channel}/models")
        rows = models.get("models", []) if isinstance(models, dict) else models
        existing = next((row for row in rows if row["modelKey"] == "agent-parity-model"), None)
        model_path = f"/admin/channels/{self.channel}/models" + ("/" + existing["id"] if existing else "")
        self.api("PATCH" if existing else "POST", model_path, {"modelKey": "agent-parity-model", "providerModelKey": "agent-parity-model", "displayName": "Canvas parity test", "capability": "text", "protocol": "chat-completion", "billingMode": "token", "priceConfigured": True, "enabled": True, "inputTokenPriceMicrocredits": 0, "outputTokenPriceMicrocredits": 0,
            "capabilityConfig": {"version": 1, "text": {"streaming": False, "thinking": False, "contextWindowTokens": 128000, "maxOutputTokens": 32000, "references": {"promptMaxChars": 32000}}}})
        self.save()

    def inspect_3030(self):
        names = ["canvas-canary-3030-" + service + "-1" for service in ("backend", "agent", "web")]
        result = subprocess.run(["docker", "inspect", *names], capture_output=True, text=True)
        if result.returncode:
            raise AssertionError("cannot establish existing 3030 container baseline")
        return [{"name": row["Name"], "id": row["Id"], "image": row["Config"]["Image"], "startedAt": row["State"]["StartedAt"], "restartCount": row["RestartCount"]} for row in json.loads(result.stdout)]

    def canvas(self):
        identity = str(uuid.uuid4())
        now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        self.api("PUT", f"/canvas-projects/{identity}", {"project": {"id": identity, "revision": 0, "title": "Isolated Agent parity acceptance", "nodes": [], "connections": [], "createdAt": now, "updatedAt": now, "viewport": {"x": 0, "y": 0, "zoom": 1}}})
        return identity

    def project(self, identity):
        return self.api("GET", f"/canvas-projects/{identity}")["project"]

    def view(self, run):
        return self.api("GET", f"/agent/runs/{run['id']}?eventLimit=500")["run"]

    def gate(self, run, scenario):
        def waiting():
            if scenario in self.stub("stats")["waiting"]:
                return True
            view = self.view(run)
            if view["status"] in TERMINAL:
                entry = next(row for row in self.report["runs"] if row["runId"] == run["id"])
                entry.update(status=view["status"], events=[{"event": row["type"], "tool": row.get("payload", {}).get("toolName"), "errorClass": row.get("payload", {}).get("errorClass")} for row in view.get("events", [])])
                self.save()
                raise AssertionError(f"{entry['scenario']} ended {view['status']} before protocol gate")
            return False
        wait(waiting)

    def start(self, identity, name, steps, permission="auto", gate=None):
        scenario = name + "_" + uuid.uuid4().hex[:8]
        self.stub("control", {"scenario": scenario, "steps": [*steps, tool("finish_run", summary="Acceptance scenario completed")], "gateStep": gate})
        body = {"canvasId": identity, "prompt": "AGENT_PARITY_SCENARIO:" + scenario, "channelId": self.channel,
                "channelModelKey": "agent-parity-model", "permissionMode": permission, "reasoningMode": "off", "contextScope": ["canvas"], "skillIds": [],
                "budget": {"maxCredits": 1, "maxSteps": 60}, "idempotencyKey": str(uuid.uuid4())}
        for attempt in range(3):
            try:
                run = self.api("POST", "/agent/runs", body)["run"]
                break
            except AssertionError as error:
                if "HTTP 500, code 500" not in str(error) or attempt == 2:
                    raise
                self.report.setdefault("transientApiRetries", []).append({"method": "POST", "path": "/agent/runs", "scenario": name, "attempt": attempt + 1, "httpStatus": 500})
                self.save()
                time.sleep(.5)
        self.report["runs"].append({"scenario": name, "runId": run["id"], "canvasId": identity})
        self.save()
        return run, body, scenario

    def finish(self, run, expected_failures=0):
        entry = next(row for row in self.report["runs"] if row["runId"] == run["id"])
        started = time.monotonic()
        timing = {"startedAt": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                  "noProgressLimitSeconds": 120, "totalLimitSeconds": 300, "progress": []}
        entry["terminalWait"] = timing
        def progressed(row, elapsed):
            timing["progress"].append({"elapsedSeconds": elapsed, "step": row.get("step"), "latestSeq": row.get("latestSeq")})
            self.save()
        try:
            view = wait_terminal(lambda: self.view(run), progressed)
        finally:
            timing.update(finishedAt=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), elapsedSeconds=round(time.monotonic() - started, 3))
            self.save()
        failures = [row for row in view.get("events", []) if row["type"] == "tool_failed"]
        entry.update(status=view["status"], createdAt=view.get("createdAt"), updatedAt=view.get("updatedAt"), tools=[{"event": row["type"], "tool": row.get("payload", {}).get("toolName"), "errorClass": row.get("payload", {}).get("errorClass")} for row in view.get("events", []) if row["type"] in ("tool_completed", "tool_failed")])
        if view["status"] == "failed":
            entry["runFailureReasons"] = [row.get("payload", {}).get("reason") for row in view.get("events", []) if row["type"] == "run_failed"]
        self.save()
        self.check(entry["scenario"] + " real worker tools completed", view["status"] == "completed" and (expected_failures is None or len(failures) == expected_failures),
                   status=view["status"], toolFailures=len(failures), failedToolDetails=[{"tool": row.get("payload", {}).get("toolName"), "message": row.get("payload", {}).get("text")} for row in failures])
        return view

    def scenario(self, identity, name, steps, **kwargs):
        run, _, _ = self.start(identity, name, steps, **kwargs)
        return self.finish(run)

    def results(self, view, tool_name):
        return [row["payload"].get("result", {}) for row in view.get("events", []) if row["type"] == "tool_completed" and row.get("payload", {}).get("toolName") == tool_name]

    def crud(self):
        identity = self.canvas()
        long_text = "prefix\n" + "long line 😀 文字\n" * 800 + "UNIQUE_FRAGMENT\n" + "suffix"
        steps = [tool("canvas_list_node_types"), tool("canvas_get_state"), apply(*(add("n_" + kind, kind, long_text if kind == "text" else "draft", x=index * 400, y=0) for index, kind in enumerate(BUILTINS))),
                 apply(*({"type": "update_node", "id": "n_" + kind, "patch": {"title": "edited " + kind, "width": 500, "height": 300, "x": index * 400, "y": 100}} for index, kind in enumerate(BUILTINS))),
                 apply({"type": "update_node", "id": "n_text", "patch": {"fontSize": 18, "listMode": True}},
                       {"type": "connect_nodes", "id": "edge_image", "fromNodeId": "n_text", "toNodeId": "n_image"},
                       {"type": "connect_nodes", "id": "edge_video", "fromNodeId": "n_image", "toNodeId": "n_video"},
                       {"type": "connect_nodes", "id": "edge_audio", "fromNodeId": "n_text", "toNodeId": "n_audio"}),
                 apply({"type": "update_connection", "id": "edge_video", "fromNodeId": "n_text", "toNodeId": "n_video"}, {"type": "delete_connection", "id": "edge_audio"}),
                 apply({"type": "duplicate_node", "id": "copy_text", "sourceNodeId": "n_text"}, {"type": "set_parent", "id": "copy_text", "parentId": "n_frame"}),
                 tool("canvas_read_content", nodeId="n_text", offset=0, limit=8000), tool("canvas_read_content", nodeId="n_text", offset=8000, limit=16000),
                 tool("canvas_search_nodes", query="edited text", type="text", offset=0),
                 apply({"type": "replace_text", "id": "n_text", "match": "UNIQUE_FRAGMENT", "replacement": "PRECISE_EDIT"}),
                 apply({"type": "reorder_nodes", "id": "ordering", "nodeIds": ["copy_text", *reversed(["n_" + kind for kind in BUILTINS])]}),
                 apply({"type": "delete_node", "id": "n_image"}, {"type": "set_parent", "id": "copy_text", "parentId": ""}, {"type": "delete_node", "id": "n_frame"})]
        run, _, scenario = self.start(identity, "crud_geometry_copy_edges_group_long_text", steps, gate=7)
        self.gate(run, scenario)
        try:
            grouped = self.project(identity)
            copied = next(node for node in grouped["nodes"] if node["id"] == "copy_text")
            self.check("group assignment and edited edge persisted mid run", copied.get("parentId") == "n_frame" and any(edge["id"] == "edge_video" and edge["fromNodeId"] == "n_text" for edge in grouped["connections"]) and all(edge["id"] != "edge_audio" for edge in grouped["connections"]))
        finally:
            self.stub("control", {"release": scenario})
        view = self.finish(run)
        self.sse_run = view["id"]
        project = self.project(identity)
        nodes = {node["id"]: node for node in project["nodes"]}
        self.check("all builtins created and edited through worker", len(nodes) == len(BUILTINS) - 1 and all(node["width"] == 500 and node["height"] == 300 for node in nodes.values()), nodeCount=len(nodes))
        self.check("long content exact fragment changed without truncation", nodes["n_text"]["metadata"]["content"] == long_text.replace("UNIQUE_FRAGMENT", "PRECISE_EDIT") and nodes["copy_text"]["metadata"]["content"] == long_text, contentCharacters=len(long_text))
        pages = self.results(view, "canvas_read_content")
        self.check("paged content is complete and code point counted", len(pages) == 2 and pages[0]["content"] + pages[1]["content"] == long_text and pages[0]["hasMore"] and not pages[1]["hasMore"] and pages[1]["totalCharacters"] == len(long_text))
        self.check("search finds edited text node", any(node["id"] == "n_text" for result in self.results(view, "canvas_search_nodes") for node in result.get("nodes", [])))
        self.check("deletion cleans edges and order persists", [edge["id"] for edge in project["connections"]] == ["edge_video"] and project["nodes"][0]["id"] == "copy_text" and not nodes["copy_text"].get("parentId") and not nodes["copy_text"].get("metadata", {}).get("parentId"))
        self.persisted_canvas = identity
        self.persisted_project = project

    def structured(self):
        identity = self.canvas()
        self.scenario(identity, "structured_create", [tool("canvas_get_state"), tool("canvas_create_storyboard", snapshotHash="$snapshotHash", nodeId="script", title="Rows", rows=[{"durationSeconds": 3, "plotDescription": "Scene A", "dialogue": "A"}, {"durationSeconds": 3, "plotDescription": "Scene B", "dialogue": "B"}])])
        before = self.project(identity)["nodes"][0]["metadata"]["storyboard"]["rows"]
        row_a, row_b = [row["id"] for row in before]
        self.scenario(identity, "structured_rows", [tool("canvas_get_state"), apply({"type": "reorder_rows", "id": "script", "rowIds": [row_b, row_a]}), tool("canvas_edit_storyboard", snapshotHash="$snapshotHash", nodeId="script", action="update", rowId=row_b, patch={"dialogue": "B edited"}), tool("canvas_read_storyboard", nodeId="script", offset=0, rows=10)])
        rows = self.project(identity)["nodes"][0]["metadata"]["storyboard"]["rows"]
        self.check("structured row order and partial update persisted", [row["id"] for row in rows] == [row_b, row_a] and rows[0]["dialogue"] == "B edited" and rows[1]["dialogue"] == "A")

    def group_copy(self):
        identity = self.canvas()
        self.scenario(identity, "group_copy_relationships", [tool("canvas_get_state"), apply(add("group", "frame"), add("child_text", content="source"), add("child_image", "image"), {"type": "set_parent", "id": "child_text", "parentId": "group"}, {"type": "set_parent", "id": "child_image", "parentId": "group"}, {"type": "connect_nodes", "id": "internal", "fromNodeId": "child_text", "toNodeId": "child_image"}), apply({"type": "duplicate_node", "id": "copied_group", "sourceNodeId": "group"})])
        project = self.project(identity)
        children = [node for node in project["nodes"] if node.get("parentId") == "copied_group"]
        ids = {node["id"] for node in children}
        self.check("container copy remaps children and internal connections", len(project["nodes"]) == 6 and len(children) == 2 and len(project["connections"]) == 2 and any(edge["fromNodeId"] in ids and edge["toNodeId"] in ids for edge in project["connections"]))
        self.scenario(identity, "group_delete_detaches_children", [tool("canvas_get_state"), apply({"type": "delete_node", "id": "group"})])
        nodes = {node["id"]: node for node in self.project(identity)["nodes"]}
        self.check("container deletion detaches retained children", "group" not in nodes and not nodes["child_text"].get("parentId") and not nodes["child_image"].get("parentId") and all(nodes[identity].get("parentId") == "copied_group" for identity in ids))

    def drawing(self):
        identity = self.canvas()
        def rect(width):
            return {"id": "rect", "type": "rectangle", "x": 1, "y": 2, "width": width, "height": 80}
        def edit(node, *operations):
            return tool("canvas_edit_drawing", snapshotHash="$snapshotHash", nodeId=node, engine="excalidraw", operations=list(operations))
        def upsert(record):
            return {"type": "upsert", "id": record["id"], "record": record}
        view = self.scenario(identity, "drawing_native_copy", [tool("canvas_get_state"), apply(add("drawing", "drawing")),
            edit("drawing", upsert(rect(100)), upsert({"id": "label", "type": "text", "x": 10, "y": 10, "width": 100, "height": 20, "text": "Original label"})),
            tool("canvas_read_drawing", nodeId="drawing", offset=0, limit=1), tool("canvas_read_drawing", nodeId="drawing", offset=1, limit=1),
            edit("drawing", upsert(rect(200))), apply({"type": "duplicate_node", "id": "drawing_copy", "sourceNodeId": "drawing"}),
            edit("drawing_copy", upsert(rect(300)), {"type": "remove", "id": "label"}), tool("canvas_read_drawing", nodeId="drawing", offset=0, limit=100)])
        def drawing_nodes():
            return {node["id"]: node for node in self.project(identity)["nodes"]}
        def records(node):
            return {record["id"]: record for record in node["metadata"]["drawingDocument"]["snapshot"]["elements"]}
        nodes = drawing_nodes()
        original, copied = records(nodes["drawing"]), records(nodes["drawing_copy"])
        self.check("native drawing copy owns an independent document", original["rect"]["width"] == 200 and copied["rect"]["width"] == 300 and "label" in original and "label" not in copied and nodes["drawing"]["metadata"]["drawingId"] != nodes["drawing_copy"]["metadata"]["drawingId"])
        pages = self.results(view, "canvas_read_drawing")[:2]
        self.check("native drawing paging returns every record", len(pages) == 2 and pages[0]["hasMore"] and not pages[1]["hasMore"] and {record["id"] for page in pages for record in page["records"]} == {"rect", "label"})
        run, _, _ = self.start(identity, "drawing_approval", [tool("canvas_get_state"), edit("drawing", upsert(rect(400)))], "request_approval")
        view = wait(lambda: (row if (row := self.view(run))["status"] in {"waiting_approval", *TERMINAL} else None))
        self.check("native drawing edit waits for approval", view["status"] == "waiting_approval" and records(drawing_nodes()["drawing"])["rect"]["width"] == 200)
        approval = next(row["payload"] for row in reversed(view["events"]) if row["type"] == "approval_requested")
        self.api("POST", f"/agent/runs/{run['id']}/approvals/{approval['approvalId']}/decision", {"decision": "approve"})
        self.finish(run)
        self.check("approved native drawing edit persisted", records(drawing_nodes()["drawing"])["rect"]["width"] == 400)
        run, _, scenario = self.start(identity, "drawing_read_only", [tool("canvas_get_state"), edit("drawing", upsert(rect(999)))], "read_only")
        self.finish(run, expected_failures=None)
        seen = [row for row in self.stub("stats")["requests"] if row["scenario"] == scenario]
        self.check("read only rejects native drawing edits", records(drawing_nodes()["drawing"])["rect"]["width"] == 400 and any(row["step"] == 2 for row in seen) and all("canvas_edit_drawing" not in row["availableTools"] for row in seen))

    def asset_binding(self):
        source = (ROOT / "web/public/lighting-presets/sunset.png").read_bytes()
        boundary = "parity_" + uuid.uuid4().hex
        multipart = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"kind\"\r\n\r\nimage\r\n--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"parity-upload.png\"\r\nContent-Type: image/png\r\n\r\n".encode() + source + f"\r\n--{boundary}--\r\n".encode())
        resource = self.api("POST", "/resources", multipart, content_type="multipart/form-data; boundary=" + boundary)["resource"]
        asset_id = "parity_asset_" + uuid.uuid4().hex
        now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        resource_key, resource_url = "resource:" + resource["id"], "/api/resources/" + resource["id"] + "/file"
        self.api("PUT", f"/assets/{asset_id}", {"asset": {"id": asset_id, "kind": "image", "title": "Canvas parity uploaded image", "coverUrl": resource_url, "tags": [], "createdAt": now, "updatedAt": now, "data": {"bytes": len(source), "storageKey": resource_key, "dataUrl": resource_url, "mimeType": resource.get("mimeType", "image/png"), "width": resource.get("width", 1), "height": resource.get("height", 1)}}})
        identity = self.canvas()
        view = self.scenario(identity, "uploaded_asset_binding", [tool("canvas_get_state"), apply(add("image", "image"), add("approval_image", "image"), add("read_only_image", "image"), add("text", content="unchanged")), tool("canvas_list_assets", query="Canvas parity uploaded image", kind="image", page=1), tool("canvas_bind_asset", snapshotHash="$snapshotHash", nodeId="image", assetId=asset_id), apply({"type": "duplicate_node", "id": "image_copy", "sourceNodeId": "image"}, {"type": "delete_node", "id": "image"})])
        listed = [asset for result in self.results(view, "canvas_list_assets") for asset in result.get("assets", [])]
        nodes = {node["id"]: node for node in self.project(identity)["nodes"]}
        self.check("real upload asset binds and shared copy survives deletion", any(asset["assetId"] == asset_id for asset in listed) and nodes["image_copy"]["metadata"].get("storageKey") == resource_key and "image" not in nodes, resourceId=resource["id"], uploadedBytes=len(source))
        with self.http.open(self.base[:-4] + resource_url, timeout=30) as response:
            received = response.read()
        self.check("deleting original node preserves shared uploaded resource", received == source, downloadedBytes=len(received))
        run, _, _ = self.start(identity, "asset_binding_approval", [tool("canvas_get_state"), tool("canvas_bind_asset", snapshotHash="$snapshotHash", nodeId="approval_image", assetId=asset_id)], "request_approval")
        view = wait(lambda: (row if (row := self.view(run))["status"] in {"waiting_approval", *TERMINAL} else None))
        self.check("asset binding waits for approval", view["status"] == "waiting_approval" and not next(node for node in self.project(identity)["nodes"] if node["id"] == "approval_image")["metadata"].get("storageKey"))
        approval = next(row["payload"] for row in reversed(view["events"]) if row["type"] == "approval_requested")
        self.api("POST", f"/agent/runs/{run['id']}/approvals/{approval['approvalId']}/decision", {"decision": "approve"})
        self.finish(run)
        self.check("approved asset binding persisted", next(node for node in self.project(identity)["nodes"] if node["id"] == "approval_image")["metadata"].get("storageKey") == resource_key)
        run, _, scenario = self.start(identity, "asset_binding_read_only", [tool("canvas_get_state"), tool("canvas_bind_asset", snapshotHash="$snapshotHash", nodeId="read_only_image", assetId=asset_id)], "read_only")
        self.finish(run, expected_failures=None)
        seen = [row for row in self.stub("stats")["requests"] if row["scenario"] == scenario]
        self.check("read only rejects asset binding", not next(node for node in self.project(identity)["nodes"] if node["id"] == "read_only_image")["metadata"].get("storageKey") and any(row["step"] == 2 for row in seen) and all("canvas_bind_asset" not in row["availableTools"] for row in seen))
        run, _, _ = self.start(identity, "asset_binding_type_guard", [tool("canvas_get_state"), tool("canvas_bind_asset", snapshotHash="$snapshotHash", nodeId="text", assetId=asset_id)])
        self.finish(run, expected_failures=1)
        self.check("asset type mismatch preserves existing content", next(node for node in self.project(identity)["nodes"] if node["id"] == "text")["metadata"]["content"] == "unchanged")

    def approvals(self):
        identity = self.canvas()
        self.scenario(identity, "approval_seed", [tool("canvas_get_state"), apply(add("seed", content="one durable write"))])
        run, body, _ = self.start(identity, "approval_restart_replay", [tool("canvas_get_state"), apply({"type": "duplicate_node", "id": "approved", "sourceNodeId": "seed"}, {"type": "delete_node", "id": "seed"})], "request_approval")
        view = wait(lambda: (row if (row := self.view(run))["status"] in {"waiting_approval", *TERMINAL} else None))
        self.check("approval blocks duplicate and deletion", view["status"] == "waiting_approval" and [node["id"] for node in self.project(identity)["nodes"]] == ["seed"])
        approval = next(row["payload"] for row in reversed(view["events"]) if row["type"] == "approval_requested")
        if self.args.restart:
            print("Risk review: restart only canvas-agent-parity agent; approval and data remain on its isolated backend volume.", flush=True)
            self.docker("restart", "agent")
        self.api("POST", f"/agent/runs/{run['id']}/approvals/{approval['approvalId']}/decision", {"decision": "approve"})
        self.finish(run)
        replay = self.api("POST", "/agent/runs", body)["run"]
        self.check("approved run replay is idempotent", replay["id"] == run["id"] and [node["id"] for node in self.project(identity)["nodes"]] == ["approved"], restartedWorker=self.args.restart)
        rejected_canvas = self.canvas()
        self.scenario(rejected_canvas, "reject_seed", [tool("canvas_get_state"), apply(add("retained"))])
        rejected, _, _ = self.start(rejected_canvas, "approval_reject", [tool("canvas_get_state"), apply({"type": "delete_node", "id": "retained"})], "request_approval")
        view = wait(lambda: (row if (row := self.view(rejected))["status"] == "waiting_approval" else None))
        approval = next(row["payload"] for row in reversed(view["events"]) if row["type"] == "approval_requested")
        self.api("POST", f"/agent/runs/{rejected['id']}/approvals/{approval['approvalId']}/decision", {"decision": "reject"})
        terminal = wait(lambda: (row if (row := self.view(rejected))["status"] in TERMINAL else None))
        entry = next(row for row in self.report["runs"] if row["runId"] == rejected["id"])
        entry.update(status=terminal["status"], createdAt=terminal.get("createdAt"), updatedAt=terminal.get("updatedAt"), tools=[{"event": row["type"], "tool": row.get("payload", {}).get("toolName"), "errorClass": row.get("payload", {}).get("errorClass")} for row in terminal.get("events", []) if row["type"] in ("tool_completed", "tool_failed")])
        self.check("rejected deletion approval does not mutate", [node["id"] for node in self.project(rejected_canvas)["nodes"]] == ["retained"], status=terminal["status"])

    def readonly(self):
        identity = self.canvas()
        run, _, scenario = self.start(identity, "read_only_enforcement", [tool("canvas_get_state"), apply(add("forbidden"))], "read_only")
        self.finish(run, expected_failures=None)
        records = [row for row in self.stub("stats")["requests"] if row["scenario"] == scenario]
        self.check("read only excludes mutation schema and rejects forced call", not self.project(identity)["nodes"] and any(row["step"] == 2 for row in records) and all(not {"canvas_apply_ops", "canvas_undo", "canvas_redo"}.intersection(row["availableTools"]) for row in records))

    def guards(self):
        identity = self.canvas()
        self.scenario(identity, "guard_seed", [tool("canvas_get_state"), apply(add("existing", content="repeat repeat"))])
        run, _, _ = self.start(identity, "ambiguous_fragment_atomicity", [tool("canvas_get_state"), apply(add("rolled_back", content="must roll back"), {"type": "replace_text", "id": "existing", "match": "repeat", "replacement": "overwrite"}), tool("canvas_get_state")])
        self.finish(run, expected_failures=1)
        nodes = self.project(identity)["nodes"]
        self.check("ambiguous fragment rejects whole operation batch", len(nodes) == 1 and nodes[0]["id"] == "existing" and nodes[0]["metadata"]["content"] == "repeat repeat")
        run, _, _ = self.start(identity, "protected_task_field", [tool("canvas_get_state"), apply({"type": "update_node", "id": "existing", "patch": {"taskId": "forged-task"}}), tool("canvas_get_state")])
        self.finish(run, expected_failures=None)
        self.check("model cannot forge task identity", not self.project(identity)["nodes"][0]["metadata"].get("taskId"))

    def stale(self):
        identity = self.canvas()
        self.scenario(identity, "stale_seed", [tool("canvas_get_state"), apply(add("existing", content="initial"))])
        run, _, scenario = self.start(identity, "stale_snapshot", [tool("canvas_get_state"), apply({"type": "update_node", "id": "existing", "patch": {"content": "must not overwrite"}}, snapshot="$firstSnapshotHash"), tool("canvas_get_state")], gate=1)
        self.gate(run, scenario)
        project = self.project(identity)
        project["nodes"][0]["metadata"]["content"] = "manual concurrent edit"
        self.api("PUT", f"/canvas-projects/{identity}", {"project": project})
        self.stub("control", {"release": scenario})
        view = self.finish(run, expected_failures=1)
        self.check("stale snapshot rejects overwrite and preserves manual edit", self.project(identity)["nodes"][0]["metadata"]["content"] == "manual concurrent edit" and any(row.get("payload", {}).get("toolName") == "canvas_apply_ops" for row in view["events"] if row["type"] == "tool_failed"))

    def repair_scope(self):
        identity = self.canvas()
        self.scenario(identity, "repair_scope_seed", [tool("canvas_get_state"), apply(add("existing", content="initial"))])
        stale_edit = {"type": "update_node", "id": "existing", "patch": {"content": "recovered after reading current state"}}
        run, _, scenario = self.start(identity, "repeated_conflict_scope_recovery", [tool("canvas_get_state"),
            apply(stale_edit, snapshot="$firstSnapshotHash"), apply(stale_edit, snapshot="$firstSnapshotHash"),
            tool("canvas_get_state"), apply(stale_edit)], gate=1)
        self.gate(run, scenario)
        project = self.project(identity)
        project["nodes"][0]["metadata"]["content"] = "manual concurrent edit"
        self.api("PUT", f"/canvas-projects/{identity}", {"project": project})
        self.stub("control", {"release": scenario})
        view = self.finish(run, expected_failures=2)
        failures = [row["payload"] for row in view["events"] if row["type"] == "tool_failed"]
        self.check("scoped repair retains model catalog and recovers only after a fresh read",
            all(row.get("errorClass") == "state_conflict" for row in failures) and
            failures[-1].get("result", {}).get("retry", {}).get("ladder") == "scoped" and
            self.project(identity)["nodes"][0]["metadata"]["content"] == stale_edit["patch"]["content"],
            expectedRejectedCalls=2)
    def undo(self):
        ordered_canvas = self.canvas()
        original_order = ["first", "middle", "last"]
        ordered, _, scenario = self.start(ordered_canvas, "undo_restores_middle_node_order", [tool("canvas_get_state"), apply(*(add(node) for node in original_order)),
            apply({"type": "delete_node", "id": "middle"}), tool("canvas_undo", snapshotHash="$snapshotHash"),
            apply({"type": "reorder_nodes", "id": "ordering", "nodeIds": list(reversed(original_order))}), tool("canvas_undo", snapshotHash="$snapshotHash")], gate=3)
        self.gate(ordered, scenario)
        try:
            self.check("deleting the middle node persists the remaining sequence", [node["id"] for node in self.project(ordered_canvas)["nodes"]] == ["first", "last"])
        finally:
            self.stub("control", {"release": scenario})
        ordered_view = self.finish(ordered)
        self.check("undo restores middle node and original snapshot sequence", [node["id"] for node in self.project(ordered_canvas)["nodes"]] == original_order)
        deltas = [row.get("payload", {}) for row in ordered_view["events"] if row["type"] == "canvas_updated"]
        self.check("deletion SSE carries null and undo SSE carries complete order", all(not delta.get("requiresRefresh") for delta in deltas) and
            any(change.get("after", "absent") is None and change.get("before", {}).get("id") == "middle" for delta in deltas for change in delta.get("canvasPatch", {}).get("nodes", [])) and
            any(delta.get("operation") == "canvas_undo" and delta.get("canvasPatch", {}).get("nodeOrder") == original_order for delta in deltas))
        identity = self.canvas()
        run, _, scenario = self.start(identity, "multi_step_undo_redo_branch", [tool("canvas_get_state"), apply(add("undo_node", content="before")),
            apply({"type": "update_node", "id": "undo_node", "patch": {"content": "after"}}), tool("canvas_undo", snapshotHash="$snapshotHash"),
            tool("canvas_undo", snapshotHash="$snapshotHash"), tool("canvas_redo", snapshotHash="$snapshotHash"), tool("canvas_redo", snapshotHash="$snapshotHash"),
            tool("canvas_undo", snapshotHash="$snapshotHash"), apply({"type": "update_node", "id": "undo_node", "patch": {"content": "new branch"}}),
            tool("canvas_redo", snapshotHash="$snapshotHash"), tool("canvas_get_state")], gate=5)
        self.gate(run, scenario)
        try:
            self.check("two Agent undo steps restore the empty canvas", not self.project(identity)["nodes"])
        finally:
            self.stub("control", {"release": scenario})
        view = self.finish(run, expected_failures=1)
        self.check("multi step redo restores edits and new branch invalidates abandoned redo", self.project(identity)["nodes"][0]["metadata"]["content"] == "new branch" and len(self.results(view, "canvas_undo")) == 3 and len(self.results(view, "canvas_redo")) == 2 and any(row["type"] == "tool_failed" and row.get("payload", {}).get("toolName") == "canvas_redo" for row in view["events"]))
        identity2 = self.canvas()
        run, _, _ = self.start(identity2, "ui_undo_api", [tool("canvas_get_state"), apply(add("ui_undo"))])
        view = self.finish(run)
        result = self.results(view, "canvas_apply_ops")[-1]
        self.api("POST", f"/agent/runs/{run['id']}/undo", {"expectedSnapshotHash": result["snapshotHash"], "reason": "isolated acceptance"})
        self.check("existing user undo API restores persisted canvas", not self.project(identity2)["nodes"])

    def reload(self):
        if self.args.restart:
            print("Risk review: restart only canvas-agent-parity backend/agent, then web to refresh its upstream DNS; preserve its data volume and 3030 services.", flush=True)
            self.docker("restart", "backend", "agent")
            self.docker("restart", "web")
            def ready():
                try:
                    return self.api("GET", "/health/ready")
                except (OSError, AssertionError, json.JSONDecodeError):
                    return None
            wait(ready)
        reloaded = self.project(self.persisted_canvas)
        self.check("canvas persisted after fresh API reload", reloaded["nodes"] == self.persisted_project["nodes"] and reloaded["connections"] == self.persisted_project["connections"], restartedBackend=self.args.restart, revision=reloaded.get("revision"))
        self.check("existing 3030 containers remain unchanged", self.inspect_3030() == self.existing_3030)

    def resume_tail(self, source_path):
        source_file = (ROOT / source_path).resolve()
        if source_file == self.output.resolve():
            raise AssertionError("continuation output must preserve the original report")
        source = json.loads(source_file.read_text(encoding="utf-8"))
        if source.get("project") != "canvas-agent-parity" or source.get("port") != 3040:
            raise AssertionError("continuation requires evidence from the isolated project")
        self.report["mode"] = "continuation"
        self.report["sourceEvidence"] = {"report": str(source_file), "passedChecks": sum(bool(row["passed"]) for row in source["checks"]),
                                         "passed": source.get("passed"), "failure": source.get("failure"),
                                         "note": "Prior checks are evidence inputs, not rerun or counted as continuation checks."}
        ids = self.docker("ps", "-q").split()
        containers = json.loads(subprocess.check_output(["docker", "inspect", *ids], text=True))
        images = [{"service": row["Config"]["Labels"]["com.docker.compose.service"], "image": row["Config"]["Image"], "imageId": row["Image"]} for row in containers]
        original_images = next(row["services"] for row in source["checks"] if row["name"] == "isolated Compose labels and unique images")
        self.check("continuation uses the same four isolated images", len(images) == 4 and all(row["Config"]["Labels"].get("com.docker.compose.project") == "canvas-agent-parity" for row in containers) and sorted(images, key=lambda row: row["service"]) == sorted(original_images, key=lambda row: row["service"]), services=images)
        self.report["health"] = health = self.api("GET", "/health")
        self.check("continuation retains source build and schema 58", health.get("ready") is True and health["build"] == source["health"]["build"] and health["schema"]["current"] == health["schema"]["expected"] == 58)
        self.existing_3030 = source["retained3030"]
        self.report["retained3030"] = self.existing_3030
        self.check("3030 baseline is unchanged before continuation", self.inspect_3030() == self.existing_3030)
        self.api("POST", "/auth/login", json.loads((ROOT / ".local/agent-parity-account.json").read_text()))
        channels = self.api("GET", "/admin/channels")["channels"]
        self.channel = next(row["id"] for row in channels if row["name"] == "Isolated canvas parity stub")
        existing = next(row for row in source["runs"] if row["scenario"] == "multi_step_undo_redo_branch")
        view = self.view({"id": existing["runId"]})
        completed = [row for row in view["events"] if row["type"] == "tool_completed"]
        failed = [row for row in view["events"] if row["type"] == "tool_failed"]
        self.report["sourceEvidence"]["naturalRecovery"] = {"runId": view["id"], "status": view["status"], "createdAt": view["createdAt"], "updatedAt": view["updatedAt"],
                                                            "completedTools": len(completed), "failedTools": len(failed), "phase500Evidence": ".local/agent-parity-multi-step-backend.log"}
        self.check("existing multi-step run naturally completed with its expected redo rejection", view["status"] == "completed" and len(completed) == 11 and len(failed) == 1 and failed[0]["payload"].get("toolName") == "canvas_redo")
        self.check("existing history has three undo, two redo and the new branch content", len(self.results(view, "canvas_undo")) == 3 and len(self.results(view, "canvas_redo")) == 2 and self.project(existing["canvasId"])["nodes"][0]["metadata"]["content"] == "new branch")
        identity = self.canvas()
        run, _, _ = self.start(identity, "ui_undo_api", [tool("canvas_get_state"), apply(add("ui_undo"))])
        undo_view = self.finish(run)
        result = self.results(undo_view, "canvas_apply_ops")[-1]
        self.api("POST", f"/agent/runs/{run['id']}/undo", {"expectedSnapshotHash": result["snapshotHash"], "reason": "isolated acceptance continuation"})
        self.check("existing user undo API restores persisted canvas", not self.project(identity)["nodes"])
        crud = next(row for row in source["runs"] if row["scenario"] == "crud_geometry_copy_edges_group_long_text")
        self.persisted_canvas, self.sse_run = crud["canvasId"], crud["runId"]
        self.persisted_project = self.project(self.persisted_canvas)
        self.report["persistenceSource"] = {"canvasId": self.persisted_canvas, "completedRunId": self.sse_run}
        self.reload()
        self.report["healthAfterRestart"] = self.api("GET", "/health")
        self.sse()

    def sse(self):
        def events(after=None):
            headers = {"Last-Event-ID": str(after)} if after is not None else {}
            request = urllib.request.Request(self.base + f"/agent/runs/{self.sse_run}/events", headers=headers)
            with self.http.open(request, timeout=30) as response:
                return [int(line[3:].strip()) for line in response if line.startswith(b"id:")]
        initial = events()
        self.check("SSE delivers ordered persisted events", bool(initial) and initial == sorted(set(initial)), eventCount=len(initial))
        cursor = initial[len(initial) // 2]
        replay = events(cursor)
        self.check("SSE resume only replays events after cursor", replay == [value for value in initial if value > cursor], replayedEvents=len(replay))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--restart", action="store_true", help="Restart only this isolated project's agent/backend to verify persistence")
    parser.add_argument("--output", default=".local/agent-parity-acceptance.json")
    parser.add_argument("--resume-from-report", help="Read existing evidence and run only the unexecuted tail checks")
    args = parser.parse_args()
    acceptance = Acceptance(args)
    try:
        if args.resume_from_report:
            acceptance.resume_tail(args.resume_from_report)
        else:
            acceptance.init()
            acceptance.crud()
            acceptance.structured()
            acceptance.group_copy()
            acceptance.drawing()
            acceptance.asset_binding()
            acceptance.approvals()
            acceptance.readonly()
            acceptance.guards()
            acceptance.stale()
            acceptance.repair_scope()
            acceptance.undo()
            acceptance.reload()
            acceptance.sse()
        acceptance.report["passed"] = True
    except Exception as error:
        acceptance.report["passed"] = False
        acceptance.report["failure"] = str(error)
        try:
            requests = acceptance.stub("stats")["requests"]
            scenario = requests[-1]["scenario"] if requests else None
            acceptance.report["lastProtocolDiagnostics"] = [row for row in requests if row["scenario"] == scenario]
        except Exception:
            acceptance.report["protocolDiagnosticsUnavailable"] = True
        raise
    finally:
        acceptance.report["finishedAt"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        acceptance.save()
    print(json.dumps({"passed": True, "checks": len(acceptance.report["checks"]), "report": str(acceptance.output)}))


if __name__ == "__main__":
    main()
