"""Isolated acceptance: configuration, canvas quotas, hot drain and retained channels."""
import importlib.util
import hashlib
import json
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("event_acceptance", ROOT / "scripts/verify-event-canary.py")
event = importlib.util.module_from_spec(spec)
spec.loader.exec_module(event)

def config():
    return event.api("GET", "/admin/settings/agent-scheduler")["setting"]

def update(**changes):
    before = config()
    body = {key: changes.get(key, before[key]) for key in ("dispatchConcurrency", "maxResidentSessions", "maxResidentPerCanvas")}
    body["expectedRevision"] = before["revision"]
    saved = event.api("PUT", "/admin/settings/agent-scheduler", body)["setting"]
    event.wait_until(lambda: any(row["online"] and row["appliedConfigRevision"] == saved["revision"] for row in event.api("GET", "/admin/settings/agent-scheduler/status")["instances"]), 35)
    return saved

def fair_admission():
    update(dispatchConcurrency=4, maxResidentSessions=64, maxResidentPerCanvas=2)
    event.stub("control", {"block": True, "reset": True, "mode": "text"})
    a, b = event.canvas(), event.canvas()
    runs = [event.start(a) for _ in range(8)]
    event.wait_until(lambda: event.stub("stats")["active"] == 2)
    started = time.monotonic()
    independent = event.start(b)
    event.wait_until(lambda: event.stub("stats")["active"] == 3, 20)
    seconds = time.monotonic() - started
    waiting = [event.run_view(run["id"]) for run in runs]
    assert sum(run.get("waitKind") == "canvas_capacity" for run in waiting) >= 6, waiting
    update(maxResidentPerCanvas=16)
    event.wait_until(lambda: event.stub("stats")["active"] == 9)
    event.stub("control", {"block": False})
    for run in runs + [independent]:
        assert event.terminal(run["id"])["status"] == "completed"
    return {"queuedInCanvasA": 8, "canvasLimit": 2, "canvasBAdmissionSeconds": seconds, "peakAfterExpansion": event.stub("stats")["peak"]}

def drain():
    update(dispatchConcurrency=4, maxResidentSessions=64, maxResidentPerCanvas=16)
    event.stub("control", {"block": True, "reset": True, "mode": "text"})
    canvases = [event.canvas() for _ in range(4)]
    runs = [event.start(canvases[i % 4]) for i in range(20)]
    event.wait_until(lambda: event.stub("stats")["active"] == 16)
    event.wait_until(lambda: any(row["online"] and row["resident"] >= 20 for row in event.api("GET", "/admin/settings/agent-scheduler/status")["instances"]), 35)
    saved = update(maxResidentSessions=16)
    rows = event.api("GET", "/admin/settings/agent-scheduler/status")["instances"]
    current = next(row for row in rows if row["online"] and row["appliedConfigRevision"] == saved["revision"])
    assert current["resident"] == 20 and current["capacity"] == 16 and current["draining"], current
    extra = event.start(event.canvas())
    time.sleep(2)
    assert event.run_view(extra["id"])["status"] == "queued"
    event.stub("control", {"block": False})
    for run in runs + [extra]:
        assert event.terminal(run["id"])["status"] == "completed"
    update(dispatchConcurrency=8, maxResidentSessions=64)
    final = update(dispatchConcurrency=4)
    return {"residentAtShrink": 20, "newLimit": 16, "modelLimit": 16, "extraWaited": True, "expandedAndRestoredSlots": [4, 8, 4], "finalRevision": final["revision"]}

if __name__ == "__main__":
    original = config()
    result = {}
    try:
        result["fairAdmission"] = fair_admission()
        result["hotDrain"] = drain()
        stale = event.client().put(event.BASE + "/admin/settings/agent-scheduler", json={"expectedRevision": 0, "dispatchConcurrency": 4, "maxResidentSessions": 64, "maxResidentPerCanvas": 16}, timeout=15)
        assert stale.status_code == 409
        result["staleConfigHTTP"] = stale.status_code
        rows = event.api("GET", "/admin/channels")["channels"]
        before = json.loads((ROOT / ".local/pre-orchestrator-channels.json").read_text())
        digest = hashlib.sha256(json.dumps(rows, sort_keys=True).encode()).hexdigest()
        assert digest == before["sha256"], "Existing channel configuration changed"
        result["retainedChannels"] = before["count"]
    finally:
        event.stub("control", {"block": False})
        update(**{key: original[key] for key in ("dispatchConcurrency", "maxResidentSessions", "maxResidentPerCanvas")})
        (ROOT / ".local/orchestrator-acceptance.json").write_text(json.dumps(result, ensure_ascii=False, indent=2))
    print(json.dumps(result, ensure_ascii=False))
