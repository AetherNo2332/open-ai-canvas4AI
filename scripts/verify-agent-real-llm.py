"""Explicitly authorized, billable LLM acceptance on isolated test canvases."""
import argparse
import concurrent.futures
import importlib.util
import json
import time
import uuid
from pathlib import Path
from threading import Lock

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("event_acceptance", ROOT / "scripts/verify-event-canary.py")
event = importlib.util.module_from_spec(spec)
spec.loader.exec_module(event)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--channel-id", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--parallel-sessions", type=int, choices=range(1, 17), default=4)
    args = parser.parse_args()
    report = {"channelId": args.channel_id, "model": args.model, "runs": []}
    output = ROOT / ".local/real-llm-acceptance.json"
    output_lock = Lock()

    def save():
        with output_lock:
            temporary = output.with_suffix(".tmp")
            temporary.write_text(json.dumps(report, ensure_ascii=False, indent=2))
            temporary.replace(output)

    def start(canvas_id, prompt, permission="read_only", parent=None):
        body = {"canvasId": canvas_id, "prompt": prompt, "channelId": args.channel_id,
                "channelModelKey": args.model, "permissionMode": permission,
                "contextScope": ["canvas"], "budget": {"maxCredits": 20, "maxSteps": 12},
                "idempotencyKey": str(uuid.uuid4())}
        run = event.api("POST", f"/agent/runs/{parent}/messages" if parent else "/agent/runs", body)["run"]
        report["runs"].append({"runId": run["id"], "canvasId": canvas_id, "startedAt": time.time()})
        save()
        return run

    def finished(run, seconds=300):
        view = event.wait_until(lambda: (v if (v := event.run_view(run["id"]))["status"] in
                                ("completed", "failed", "cancelled", "rejected") else None), seconds)
        entry = next(x for x in report["runs"] if x["runId"] == run["id"])
        entry.update(status=view["status"], finishedAt=time.time(),
                     toolEvents=[{"type": x["type"], "toolName": x.get("payload", {}).get("toolName")}
                                 for x in view.get("events", []) if x["type"].startswith("tool_")])
        save()
        assert view["status"] == "completed", {"runId": run["id"], "status": view["status"], "failure": view.get("failureMessage")}
        return view

    canvas_id = event.canvas()
    question = start(canvas_id, "这是一次真实端到端验收。先调用 canvas_get_state 读取画布，再调用 ask_user 询问短片方向，给出两个选项：A青春成长、B悬疑反转。必须调用工具，不要只用文本提问；options 使用工具规定的数组。暂时不要写入节点。")
    q = finished(question)
    assert any(x["type"] == "user_question" for x in q["events"]), "LLM did not ask through the actual tool"
    report["question"] = {"runId": question["id"], "status": q["status"]}
    save()
    print("Real LLM canvas read and ask_user passed", flush=True)

    followup = start(canvas_id, "我选择 A 青春成长。请写一个简短的1分钟校园短片脚本，4个镜头，总字数不超过250字。读取画布后仅新增一个文本节点，标题为‘真实LLM恢复验收’，内容为脚本；不要修改或删除其他节点，不要生成图片或视频。通过审批后写入并 finish_run。", "request_approval", question["id"])
    view = event.wait_until(lambda: (v if (v := event.run_view(followup["id"]))["status"] in
                                    ("waiting_approval", "completed", "failed") else None), 300)
    assert view["status"] == "waiting_approval", {"runId": followup["id"], "status": view["status"], "failure": view.get("failureMessage")}
    approval = next(x["payload"] for x in reversed(view["events"]) if x["type"] == "approval_requested")
    event.api("POST", f'/agent/runs/{followup["id"]}/approvals/{approval["approvalId"]}/decision', {"decision": "approve"})
    completed = finished(followup)
    project = event.api("GET", f"/canvas-projects/{canvas_id}")["project"]
    assert len(project["nodes"]) == 1, "write was missing or duplicated"
    node = project["nodes"][0]
    assert node.get("type") == "text" and node.get("metadata", {}).get("content"), "script text was not persisted"
    report["approvalWrite"] = {"runId": followup["id"], "nodeCount": len(project["nodes"]),
                               "conversationId": completed.get("conversationId"), "approved": True}
    report["sse"] = event.verify_sse(followup["id"])
    save()
    print("Real LLM continuation, approval, single write and SSE passed", flush=True)

    invalid = start(event.canvas(), "仅做参数校验恢复测试，不修改画布、不创建生成任务。请首先调用 ask_user，question 为‘选择测试方向’，故意把 options 填为字符串 \"[]\" 而非数组，以验证工具拒绝。如果收到参数错误，纠正为两个选项的数组（A、B），再次调用 ask_user。不要因参数错误结束，也不要只返回文本。")
    bad = finished(invalid)
    failures = [x for x in bad["events"] if x["type"] == "tool_failed"]
    report["argumentRecovery"] = {"runId": invalid["id"], "completed": True,
                                  "invalidArgumentEmitted": bool(failures),
                                  "userQuestion": any(x["type"] == "user_question" for x in bad["events"])}
    assert report["argumentRecovery"]["userQuestion"], "missing corrected ask_user result"
    save()
    print("Real LLM argument scenario completed; invalid emitted:", bool(failures), flush=True)

    canvases = [event.canvas() for _ in range(args.parallel_sessions)]
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.parallel_sessions) as pool:
        runs = list(pool.map(lambda i: start(canvases[i], f"这是独立会话并发验收 {i+1}。调用 canvas_get_state 读取当前画布，然后直接 finish_run，summary 仅写‘会话{i+1}完成’，不要提问、不要写入、不要创建计划。"), range(args.parallel_sessions)))
    for run in runs:
        finished(run)
    report["parallel"] = {"sessions": args.parallel_sessions, "allCompleted": True, "runIds": [x["id"] for x in runs]}
    save()
    print(args.parallel_sessions, "independent real LLM sessions passed", flush=True)


if __name__ == "__main__":
    main()
