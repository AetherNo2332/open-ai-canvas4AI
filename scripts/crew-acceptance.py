"""Local Canvas Crew HTTP acceptance. Never reads browser state or provider keys.

python scripts/crew-acceptance.py --auth-file <ignored-fixture.json> --output <result.json>
Authentication is supplied separately; fixture sessions are not a login-flow test.
"""
import argparse
import json
import time
import uuid
import urllib.request
import urllib.error
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--base", default="http://127.0.0.1:3000")
parser.add_argument("--auth-file", required=True)
parser.add_argument("--output", required=True)
parser.add_argument("--channel", default="CHANNEL_000001")
parser.add_argument("--model", default="deepseek-flash")
parser.add_argument("--config-only", action="store_true")
args = parser.parse_args()
if not args.base.startswith(("http://localhost:", "http://127.0.0.1:")):
    raise SystemExit("This runner only accepts a local test instance")
auth = json.loads(Path(args.auth_file).read_text())
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
evidence = {"checks": [], "model": args.model, "authMode": "isolated database session fixture"}

def check(name, condition):
    evidence["checks"].append({"name": name, "pass": bool(condition)})
    print(("PASS " if condition else "FAIL ") + name, flush=True)
    if not condition:
        raise AssertionError(name)

def call(method, path, body=None, user="admin", expect=200):
    headers = {"Content-Type": "application/json", "Origin": args.base}
    if user:
        headers["Cookie"] = auth[user]
    request = urllib.request.Request(args.base + "/api" + path, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
    try:
        response = opener.open(request, timeout=30)
    except urllib.error.HTTPError as error:
        response = error
    status = response.code
    wire = json.load(response)
    if status != expect or expect == 200 and wire.get("code") != 0:
        # Do not print response bodies, prompts, credentials or upstream URLs.
        raise AssertionError(f"{method} {path}: HTTP {status}, code {wire.get('code')}, reason {wire.get('reason')}")
    return wire.get("data")

def member(name, role, position):
    return {"name": name, "role": role, "permissionMode": "propose", "enabled": True, "position": position, "focusNodeIds": [], "modelConfig": {"model": args.model, "channelId": args.channel, "channelModelKey": args.model}, "budget": {"maxCredits": 20, "maxSteps": 30, "maxGenerationTasks": 0, "maxVideoSeconds": 0}}

def stream_probe(run_id, after, limit=2):
    request = urllib.request.Request(args.base + f"/api/agent/crew-runs/{run_id}/events?after={after}", headers={"Cookie": auth["admin"], "Accept": "text/event-stream", "Last-Event-ID": str(after)})
    sequences, events, snapshot = [], [], None
    with opener.open(request, timeout=20) as response:
        check("SSE content type", "text/event-stream" in response.headers.get("Content-Type", ""))
        frame = {}
        for line in response:
            line = line.decode().strip()
            if line.startswith(":"):
                if snapshot and not sequences:
                    break
                continue
            if not line:
                if frame.get("event") == "crew_snapshot":
                    snapshot = json.loads(frame["data"])
                    check("SSE snapshot identity", snapshot["id"] == run_id)
                elif frame.get("event") == "crew_event":
                    data = json.loads(frame["data"])
                    check("SSE event identity/cursor", data["crewRunId"] == run_id and data["sequence"] > after and (not sequences or data["sequence"] > sequences[-1]))
                    sequences.append(data["sequence"])
                    events.append(data["type"])
                    if len(sequences) >= limit:
                        break
                frame = {}
            elif ":" in line:
                key, value = line.split(":", 1)
                frame[key] = value.strip()
    evidence.setdefault("streams", []).append({"after": after, "sequences": sequences, "types": events})
    return sequences[-1] if sequences else after

def wait_approval(run_id):
    deadline = time.monotonic() + 300
    last = None
    while time.monotonic() < deadline:
        run = call("GET", f"/agent/crew-runs/{run_id}")
        state = (run["status"], tuple(row["status"] for row in run["members"]))
        if state != last:
            print("STATE", state, flush=True)
            last = state
        if run["status"] == "waiting_approval":
            return run
        if run["status"] in ("failed", "cancelled", "completed"):
            evidence["failedRun"] = {"id": run_id, "status": run["status"], "members": [{"id": row["id"], "status": row["status"], "errorCode": row.get("errorCode")} for row in run["members"]]}
            raise AssertionError("Real-model run terminated before approval")
        time.sleep(1)
    raise AssertionError("Real-model run did not reach approval within 300 seconds")

try:
    health = call("GET", "/health", user=None)
    evidence["build"] = health["build"]
    check("health/schema ready", health["ready"] and health["schema"]["current"] == health["schema"]["expected"])
    canvas = auth["canvasId"]
    path = f"/agent/workspaces/{canvas}/crews"
    before = call("GET", "/admin/settings/features")["features"]
    try:
        call("PATCH", "/admin/settings/features", {"agentCrewEnabled": False})
        call("GET", path, expect=403)
        check("disabled Crew route rejects", True)
        call("PATCH", "/admin/settings/features", {"agentCrewEnabled": True})
        after = call("GET", "/admin/settings/features")["features"]
        check("Crew toggle preserves other feature fields", {k: v for k, v in before.items() if k != "agentCrewEnabled" and isinstance(v, bool)} == {k: v for k, v in after.items() if k != "agentCrewEnabled" and isinstance(v, bool)})
        call("PATCH", "/admin/settings/features", {"agentCrewEnabled": True}, user="other", expect=403)
        call("GET", path, user=None, expect=401)
        crew = call("POST", path, {"name": "Crew script acceptance", "description": "Isolated functional fixture", "status": "enabled", "members": [member("Coordinator", "coordinator", 0), member("Writer A", "member", 1), member("Writer B", "member", 2)]})
        crew_path = "/agent/crews/" + crew["id"]
        evidence["crewId"] = crew["id"]
        call("GET", crew_path, user="other", expect=404)
        call("PATCH", crew_path, {"revision": crew["revision"], "name": "foreign", "description": "", "status": "enabled"}, user="other", expect=404)
        check("Crew cross-account read/write denied", True)
        old_revision = crew["revision"]
        crew = call("PATCH", crew_path, {"revision": old_revision, "name": "Crew script accepted", "description": "CAS test", "status": "enabled"})
        call("PATCH", crew_path, {"revision": old_revision, "name": "stale", "description": "", "status": "enabled"}, expect=409)
        check("Crew stale revision rejected", True)
        target = crew["members"][1]
        crew = call("PUT", "/agent/crew-members/" + target["id"] + "/skills", {"revision": crew["revision"], "skills": []})
        check("member skill empty collection saved", len(next(row for row in crew["members"] if row["id"] == target["id"])["skills"]) == 0)
        if not args.config_only:
            prompt = "这是一个功能验收任务。Coordinator 必须同时委派给两个成员，各成员写一句不同的故事要点。每个成员先用 canvas_get_state 获取快照，再调用 task_result，返回 proposal={snapshotHash,ops:[{type:'add_node',id:自己唯一的节点ID,nodeType:'text',title:'Crew脚本验收',content:故事要点,x:位置,y:0}]}，artifactIds为空。两个成员使用不同节点ID及x位置。Coordinator 必须用 crew_wait 收齐两个结果后调用 crew_propose 请求统一审批。不得绕过成员，禁止直接写画布，禁止提前 finish_run；请真实使用服务端提供的工具。"
            request = {"prompt": prompt, "skillIds": [], "idempotencyKey": "crew-script-" + uuid.uuid4().hex, "maxConcurrentMembers": 2}
            run = call("POST", crew_path + "/runs", request)
            evidence["runId"] = run["id"]
            replay = call("POST", crew_path + "/runs", request)
            check("create Run idempotency", replay["id"] == run["id"])
            call("GET", "/agent/crew-runs/" + run["id"], user="other", expect=404)
            cursor = stream_probe(run["id"], 0)
            run = wait_approval(run["id"])
            check("two real members completed", sum(row["role"] == "member" and row["status"] == "completed" for row in run["members"]) == 2)
            check("independent member Agent runs", len(set(row["agentRunId"] for row in run["members"])) == 3)
            stream_probe(run["id"], cursor)
            approval = run["approval"]
            commit = {"approvalId": approval["approvalId"], "expectedSnapshotHash": approval["snapshotHash"], "idempotencyKey": "crew-commit-" + uuid.uuid4().hex}
            call("POST", f"/agent/crew-runs/{run['id']}/commit", commit, expect=409)
            check("commit before approval rejected", True)
            call("POST", f"/agent/crew-runs/{run['id']}/approvals/{approval['approvalId']}", {"decision": "approve", "reason": ""})
            call("POST", f"/agent/crew-runs/{run['id']}/commit", {**commit, "expectedSnapshotHash": "wrong"}, expect=409)
            result = call("POST", f"/agent/crew-runs/{run['id']}/commit", commit)
            check("commit identical replay stable", call("POST", f"/agent/crew-runs/{run['id']}/commit", commit) == result)
            final = call("GET", "/agent/crew-runs/" + run["id"])
            check("committed Run completed", final["status"] == "completed" and bool(final["approval"].get("operationId")))
            project = call("GET", "/canvas-projects/" + canvas)["project"]
            check("member proposals persisted canvas nodes", len(project["nodes"]) >= 2)
            evidence["final"] = {"status": final["status"], "nodeCount": len(project["nodes"]), "latestSequence": final["latestSequence"]}
    finally:
        call("PATCH", "/admin/settings/features", {"agentCrewEnabled": before["agentCrewEnabled"]})
except Exception as error:
    evidence["error"] = str(error)
    print("ERROR", error, flush=True)
    raise
finally:
    Path(args.output).write_text(json.dumps(evidence, ensure_ascii=False, indent=2), encoding="utf-8")
