"""Exercise real Go/Node/SSE boundaries against an isolated, non-billing model stub."""
import concurrent.futures
import json
import pathlib
import subprocess
import time
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]
import requests

credentials = json.loads((ROOT / ".local/test-session.json").read_text())
BASE = "http://127.0.0.1:3004/api"

def client():
    session = requests.Session()
    session.trust_env = False
    session.cookies.update(credentials["cookies"])
    return session

def api(method, path, body=None):
    response = client().request(method, BASE + path, json=body, timeout=30)
    data = response.json()
    if data.get("code") != 0:
        raise RuntimeError(f"{path}: HTTP {response.status_code}: {data.get('msg')}")
    return data["data"]

stub_ip = subprocess.check_output(["docker", "inspect", "canvas-event-canary-model-stub-1", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}"], text=True).strip()

def stub(path, body=None):
    session = requests.Session()
    session.trust_env = False
    response = session.request("GET" if body is None else "POST", f"http://{stub_ip}:8080/{path}", json=body, timeout=10)
    response.raise_for_status()
    return response.json()

def canvas():
    identity = str(uuid.uuid4())
    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    api("PUT", f"/canvas-projects/{identity}", {"project": {"id": identity, "revision": 0, "title": "事件调度隔离验收", "nodes": [], "connections": [], "createdAt": now, "updatedAt": now, "viewport": {"x": 0, "y": 0, "zoom": 1}}})
    return identity

def start(canvas_id, permission="read_only", prompt="请直接回复一句你好。不需要执行工具或制作计划。"):
    return api("POST", "/agent/runs", {"canvasId": canvas_id, "prompt": prompt, "channelId": credentials["channelId"], "channelModelKey": "event-test-model", "permissionMode": permission, "contextScope": ["canvas"], "budget": {"maxCredits": 1, "maxSteps": 12}, "idempotencyKey": str(uuid.uuid4())})["run"]

def wait_until(predicate, seconds=90):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(.25)
    raise AssertionError("acceptance timed out")

def run_view(run_id):
    return api("GET", f"/agent/runs/{run_id}")["run"]

def verify_parallel():
    canvases = [canvas() for _ in range(4)]
    stub("control", {"block": True, "reset": True, "mode": "text"})
    with concurrent.futures.ThreadPoolExecutor(max_workers=16) as executor:
        runs = list(executor.map(lambda i: start(canvases[i % 4]), range(16)))
    wait_until(lambda: stub("stats")["active"] == 16)
    before = stub("stats")
    assert before["peak"] == 16 and before["count"] == 16, before
    # Keep the requests in flight across one 15s capacity heartbeat.
    time.sleep(16)
    logs=subprocess.check_output(["docker","logs","--since","25s","canvas-event-canary-agent-1"],text=True,stderr=subprocess.DEVNULL)
    metrics=[json.loads(line) for line in logs.splitlines() if line.startswith('{') and 'scheduler_capacity' in line]
    assert any(item["residentSessions"]>=16 and item["dispatchActive"]<=4 and item["schedulerPeak"]<=4 for item in metrics),metrics
    resources=subprocess.check_output(["docker","stats","--no-stream","--format","{{json .}}","canvas-event-canary-agent-1","canvas-event-canary-backend-1","canvas-event-canary-postgres-1"],text=True)
    stub("control", {"block": False})
    for run in runs:
        view = wait_until(lambda: (value if (value := run_view(run["id"]))["status"] in ("completed", "failed") else None))
        assert view["status"] == "completed", {"id": run["id"], "status": view["status"], "failure": view.get("failureMessage")}
    return {"sessions": 16, "peakModelRequests": before["peak"], "modelRequests": before["count"],"schedulerMetrics":metrics,"resources":[json.loads(line) for line in resources.splitlines()], "requests": stub("stats")["requests"], "runIds": [run["id"] for run in runs]}

def terminal(run_id):
    return wait_until(lambda: (value if (value := run_view(run_id))["status"] in ("completed", "failed", "cancelled", "rejected") else None), 120)

def verify_approval():
    stub("control", {"block": False, "reset": True, "mode": "tool"})
    runs = [start(canvas(), "request_approval", "请读取画布并添加一个文本节点，内容为test。") for _ in range(4)]
    for run in runs:
        view=wait_until(lambda: (value if (value:=run_view(run["id"]))["status"] in ("waiting_approval", "failed", "completed") else None))
        assert view["status"] == "waiting_approval", view
    stub("control", {"mode": "text"})
    others=[start(canvas()) for _ in range(12)]
    for run in others:
        assert terminal(run["id"])["status"] == "completed"
    assert all(run_view(run["id"])["status"] == "waiting_approval" for run in runs)
    views=[run_view(run["id"]) for run in runs]
    (ROOT/".local/approval-views.json").write_text(json.dumps(views,ensure_ascii=False,indent=2))
    first_view=views[0]
    first_approval=next(item["payload"] for item in reversed(first_view["events"]) if item["type"]=="approval_requested")
    began=time.monotonic()
    subprocess.run(["docker","stop","--time","10","canvas-event-canary-agent-1"],check=True,capture_output=True)
    try:
        # Approval and the Go write/receipt commit while the Agent is absent.
        api("POST",f'/agent/runs/{first_view["id"]}/approvals/{first_approval["approvalId"]}/decision',{"decision":"approve"})
        wait_until(lambda:len(api("GET",f'/canvas-projects/{first_view["canvasId"]}')["project"]["nodes"])==1,20)
    finally:
        subprocess.run(["docker","start","canvas-event-canary-agent-1"],check=True,capture_output=True)
    assert terminal(runs[0]["id"])["status"]=="completed"
    assert len(api("GET",f'/canvas-projects/{first_view["canvasId"]}')["project"]["nodes"])==1
    assert all(run_view(run["id"])["status"]=="waiting_approval" for run in runs[1:])
    for view in views[1:]:
        approval=next(item["payload"] for item in reversed(view["events"]) if item["type"] == "approval_requested")
        api("POST",f'/agent/runs/{view["id"]}/approvals/{approval["approvalId"]}/decision', {"decision":"reject"})
    for index,run in enumerate(runs):
        view=terminal(run["id"])
        assert view["status"] == ("completed" if index==0 else "rejected"), view
    return {"approvalWaiters":4,"otherSessionsCompleted":12,"approved":1,"rejected":3,"writeCountAfterAgentRestart":1,"restartAfterApprovalSeconds":round(time.monotonic()-began,2),"requests":stub("stats")["requests"]}

def verify_restart():
    stub("control", {"block":True,"reset":True,"mode":"text"})
    run=start(canvas())
    wait_until(lambda:stub("stats")["active"]==1)
    began=time.monotonic()
    subprocess.run(["docker","restart","canvas-event-canary-agent-1"],check=True,capture_output=True)
    # Let the previous 45s lease expire. The Go model task remains in flight.
    time.sleep(50)
    assert stub("stats")["count"]==1,stub("stats")
    released=time.monotonic()
    stub("control", {"block":False})
    view=terminal(run["id"])
    assert view["status"]=="completed",view
    assert stub("stats")["count"]==1,stub("stats")
    return {"runId":run["id"],"modelRequests":1,"elapsedSeconds":round(time.monotonic()-began,2),"completionAfterReleaseSeconds":round(time.monotonic()-released,2),"requests":stub("stats")["requests"]}

def verify_sse(run_id):
    def events(after=None):
        headers={"Last-Event-ID":str(after)} if after is not None else {}
        response=client().get(BASE+f"/agent/runs/{run_id}/events",headers=headers,stream=True,timeout=30)
        response.raise_for_status()
        ids=[]
        for line in response.iter_lines():
            if line.startswith(b"id:"):ids.append(int(line[3:].strip()))
        response.close()
        return ids
    initial=events()
    assert initial and initial==sorted(set(initial)),initial
    cursor=initial[len(initial)//2]
    replay=events(cursor)
    assert replay==[value for value in initial if value>cursor],(initial,replay)
    return {"runId":run_id,"events":len(initial),"resumeAfter":cursor,"replayed":len(replay)}

if __name__ == "__main__":
    import sys
    path = ROOT / ".local/acceptance.json"
    result=json.loads(path.read_text()) if path.exists() else {}
    selected=sys.argv[1:] or ["parallel","approval","restart","sse"]
    for name in selected:
        result[name]={"parallel":verify_parallel,"approval":verify_approval,"restart":verify_restart,"sse":lambda:verify_sse(result["parallel"]["runIds"][0])}[name]()
        path.write_text(json.dumps(result, ensure_ascii=False, indent=2))
        print(json.dumps({name:{key:value for key,value in result[name].items() if key not in ("requests","runIds")}},ensure_ascii=False),flush=True)
