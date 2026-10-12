"""One local HTTP/real-model acceptance run; never controls a browser."""
import argparse
import json
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--auth-file", required=True, help="Local ignored JSON containing admin/other Cookies and canvasId")
parser.add_argument("--output", required=True)
parser.add_argument("--base", default="http://127.0.0.1:3000")
parser.add_argument("--channel", default="CHANNEL_000001")
parser.add_argument("--model", default="deepseek-flash")
parser.add_argument("--commit", required=True)
args = parser.parse_args()
auth = json.loads(Path(args.auth_file).read_text(encoding="utf-8-sig"))
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
report = {"checks": []}


def call(method, path, body=None, user="admin", expected=200):
    headers = {"Content-Type": "application/json"}
    if user:
        headers["Cookie"] = auth[user]
    req = urllib.request.Request(args.base + "/api" + path, method=method, headers=headers,
                                 data=None if body is None else json.dumps(body).encode())
    try:
        with opener.open(req, timeout=30) as response:
            status, wire = response.status, json.load(response)
    except urllib.error.HTTPError as error:
        status, wire = error.code, json.load(error)
    assert status == expected, f"{method} {path}: HTTP {status}, reason={wire.get('reason', '')}"
    if expected == 200:
        assert wire["code"] == 0, wire.get("reason", "business error")
    return wire.get("data")


def check(name, passed):
    report["checks"].append({"name": name, "pass": bool(passed)})
    print(("PASS " if passed else "FAIL ") + name, flush=True)
    assert passed, name


run_id = None
scheduler_before = None
policy_before = None
policy_path = "/agent/subagent-policy?canvasId=" + urllib.parse.quote(auth["canvasId"])
terminal = {"completed", "failed", "cancelled", "rejected"}
try:
    health = call("GET", "/health", user=None)
    report["build"] = health["build"]
    check("current image commit and schema 57 ready", health["ready"] and health["schema"]["current"] == 57 and health["build"]["commit"] == args.commit)
    call("PATCH", "/admin/settings/features", {"agentSubagentsEnabled": True})
    call("PATCH", "/admin/settings/features", {"agentSubagentsEnabled": True}, user="other", expected=403)
    call("GET", policy_path, user=None, expected=401)
    call("GET", policy_path, user="other", expected=404)
    check("administrator and canvas ownership enforced", True)
    policy_before = call("GET", policy_path)
    policy = call("PUT", "/agent/subagent-policy", {"canvasId": auth["canvasId"], "enabled": True, "expectedRevision": policy_before["revision"]})
    call("PUT", "/agent/subagent-policy", {"canvasId": auth["canvasId"], "enabled": False, "expectedRevision": policy_before["revision"]}, expected=409)
    check("persistent consent with stale CAS rejection", call("GET", policy_path) == policy)
    scheduler_before = call("GET", "/admin/settings/agent-scheduler")["setting"]
    call("PUT", "/admin/settings/agent-scheduler", {"expectedRevision": scheduler_before["revision"], "dispatchConcurrency": 1, "maxResidentSessions": 1, "maxResidentPerCanvas": 1})
    request = {
        "canvasId": auth["canvasId"], "model": args.model, "channelId": args.channel, "channelModelKey": args.model,
        "permissionMode": "read_only", "reasoningMode": "off", "contextScope": ["canvas"], "skillIds": [], "subagentEnabled": True,
        "idempotencyKey": "dynamic-acceptance-" + uuid.uuid4().hex,
        "budget": {"maxCredits": 30, "maxSteps": 20, "maxGenerationTasks": 0, "maxVideoSeconds": 0},
        "prompt": "功能验收：必须真实调用 spawn_subagent 创建两个子代理，自行命名和赋予不同角色及目标。第一个核对 2+3，第二个核对 7-4；每个子代理先 send_parent_message(kind=progress)，然后 finish_subagent 提交答案。创建两个后父 Agent 必须 wait_subagents 收齐最终结果，最后 finish_run 总结。不要读画布，不要创建生成任务，不要提问用户，不要用正文代替真实工具。",
    }
    run = call("POST", "/agent/runs", request)["run"]
    run_id = run["id"]
    report["runId"] = run_id
    replay = call("POST", "/agent/runs", request)["run"]
    check("parent request retry preserves run identity", replay["id"] == run_id)
    call("GET", f"/agent/runs/{run_id}/subagents", user="other", expected=404)
    deadline = time.monotonic() + 480
    cursor, types, last, links = 0, [], None, []
    while time.monotonic() < deadline:
        run = call("GET", f"/agent/runs/{run_id}?sinceSeq={cursor}&eventLimit=500")["run"]
        for event in run.get("events", []):
            if event["seq"] > cursor:
                types.append(event["type"])
                cursor = event["seq"]
        links = call("GET", f"/agent/runs/{run_id}/subagents")
        state = (run["status"], tuple(row["status"] for row in links))
        if state != last:
            print("STATE", state, flush=True)
            last = state
        if run["status"] in terminal:
            break
        time.sleep(2)
    report["final"] = {"status": run["status"], "children": [{"id": row["id"], "childRunId": row["childRunId"], "status": row["status"], "messageKinds": [msg["kind"] for msg in row["messages"]]} for row in links], "eventTypes": sorted(set(types)), "latestSeq": cursor}
    check("real parent and two children complete at residency=1", run["status"] == "completed" and len(links) == 2 and all(row["status"] == "completed" for row in links))
    check("dynamic names roles and objectives persisted", all(row["displayName"] and row["roleLabel"] and row["objective"] for row in links))
    child_ids = {row["childRunId"] for row in links}
    check("children are independent explicitly linked runs", len(child_ids) == 2 and run_id not in child_ids and all(row["parentRunId"] == run_id for row in links))
    for row in links:
        child = call("GET", "/agent/runs/" + row["childRunId"])["run"]
        check("child identity and read-only boundary " + row["id"], child["parentId"] == run_id and child["permissionMode"] == "read_only")
        seqs = [message["sequence"] for message in row["messages"]]
        check("ordered progress and final result " + row["id"], seqs == sorted(set(seqs)) and "progress" in [m["kind"] for m in row["messages"]] and "final_result" in [m["kind"] for m in row["messages"]])
    check("parent event journal includes spawn and child messages", "subagent_spawned" in types and "subagent_message" in types)
    check("consent remains enabled after the completed turn", call("GET", policy_path)["enabled"] is True)
    policy = call("GET", policy_path)
    call("PUT", "/agent/subagent-policy", {"canvasId": auth["canvasId"], "enabled": False, "expectedRevision": policy["revision"]})
    request["idempotencyKey"] += "-disabled"
    call("POST", "/agent/runs", request, expected=403)
    check("closing consent rejects the next authorized run", True)
finally:
    if run_id:
        try:
            current = call("GET", "/agent/runs/" + run_id)["run"]
            if current["status"] not in terminal:
                call("POST", "/agent/runs/" + run_id + "/cancel")
        except Exception as error:
            report["cleanupRun"] = type(error).__name__
    if policy_before:
        latest = call("GET", policy_path)
        call("PUT", "/agent/subagent-policy", {"canvasId": auth["canvasId"], "enabled": policy_before["enabled"], "expectedRevision": latest["revision"]})
    if scheduler_before:
        latest = call("GET", "/admin/settings/agent-scheduler")["setting"]
        call("PUT", "/admin/settings/agent-scheduler", {"expectedRevision": latest["revision"], **{key: scheduler_before[key] for key in ("dispatchConcurrency", "maxResidentSessions", "maxResidentPerCanvas")}})
    Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
