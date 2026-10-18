"""Isolated acceptance for L0/L1 concrete tool advertisement.

    python scripts/verify-agent-tool-disclosure.py

Uses canvas-agent-parity on loopback 3040 and the model stub's availableTools.
Does not change canvas-canary-3030. Reports stay under ignored .local.
"""
import argparse
import importlib.util
import json
import time
import uuid
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("parity", ROOT / "scripts/verify-agent-canvas-parity.py")
parity = importlib.util.module_from_spec(spec)
spec.loader.exec_module(parity)

HEAVY_L1 = ("canvas_edit_storyboard", "canvas_edit_batch_table", "canvas_edit_drawing", "image_layer_split")
STORYBOARD = ("canvas_read_storyboard", "canvas_create_storyboard", "canvas_edit_storyboard")


class DisclosureAcceptance(parity.Acceptance):
    def start_prompt(self, identity, name, steps, prompt_suffix, permission="auto"):
        scenario = name + "_" + uuid.uuid4().hex[:8]
        self.stub("control", {"scenario": scenario, "steps": [*steps, parity.tool("finish_run", summary="Acceptance scenario completed")]})
        body = {
            "canvasId": identity,
            "prompt": "AGENT_PARITY_SCENARIO:" + scenario + " " + prompt_suffix,
            "channelId": self.channel,
            "channelModelKey": "agent-parity-model",
            "permissionMode": permission,
            "reasoningMode": "off",
            "contextScope": ["canvas"],
            "skillIds": [],
            "budget": {"maxCredits": 1, "maxSteps": 60},
            "idempotencyKey": str(uuid.uuid4()),
        }
        run = self.api("POST", "/agent/runs", body)["run"]
        self.report["runs"].append({"scenario": name, "runId": run["id"], "canvasId": identity, "stubScenario": scenario})
        self.save()
        return run, scenario

    def wait_run(self, run):
        return parity.wait_terminal(lambda: self.view(run), lambda *_: None)

    def stub_records(self, scenario):
        return [row for row in self.stub("stats")["requests"] if row.get("scenario") == scenario]

    def tools_at(self, scenario, step=0):
        records = self.stub_records(scenario)
        self.check(scenario + " stub recorded model tools", len(records) > step, steps=len(records))
        return list(records[step].get("availableTools") or [])

    def script_canvas(self):
        identity = str(uuid.uuid4())
        now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        self.api("PUT", f"/canvas-projects/{identity}", {"project": {
            "id": identity, "revision": 0, "title": "Disclosure storyboard inventory",
            "nodes": [{"id": "board", "type": "script", "title": "分镜", "position": {"x": 0, "y": 0}, "width": 920, "height": 360,
                       "metadata": {"status": "idle", "workflowKind": "script",
                                    "storyboard": {"rows": [], "visibleColumns": ["shotNumber"], "referenceNodeIds": []}}}],
            "connections": [], "createdAt": now, "updatedAt": now, "viewport": {"x": 0, "y": 0, "zoom": 1},
        }})
        return identity

    def empty_read(self):
        identity = self.canvas()
        run, scenario = self.start_prompt(identity, "empty_read", [parity.tool("canvas_get_state")], "现在画布有几个节点")
        view = self.wait_run(run)
        tools = self.tools_at(scenario)
        self.check("empty_read completed", view["status"] == "completed", status=view["status"])
        self.check("empty_read keeps L0 canvas_get_state", "canvas_get_state" in tools, tools=tools)
        self.check("empty_read hides heavy L1", all(name not in tools for name in HEAVY_L1), tools=tools)
        return identity, tools

    def storyboard_inventory(self):
        identity = self.script_canvas()
        run, scenario = self.start_prompt(identity, "storyboard_inventory", [
            parity.tool("canvas_read_storyboard", nodeId="board", offset=0, rows=5),
        ], "读取分镜")
        view = self.wait_run(run)
        tools = self.tools_at(scenario)
        self.check("storyboard_inventory completed", view["status"] == "completed", status=view["status"])
        self.check("storyboard_inventory advertises storyboard tools", all(name in tools for name in STORYBOARD), tools=tools)
        receipts = [row for row in view.get("events", []) if row.get("type") == "tool_completed" and row.get("payload", {}).get("toolName") == "canvas_read_storyboard"]
        self.check("storyboard_inventory executed storyboard read", bool(receipts) and isinstance((receipts[0].get("payload") or {}).get("result", {}).get("snapshotHash"), str))

    def storyboard_intent(self):
        identity = self.canvas()
        run, scenario = self.start_prompt(identity, "storyboard_intent", [parity.tool("canvas_get_state")], "帮我写分镜")
        view = self.wait_run(run)
        tools = self.tools_at(scenario)
        self.check("storyboard_intent completed", view["status"] == "completed", status=view["status"])
        self.check("storyboard_intent advertises storyboard tools on empty canvas", all(name in tools for name in STORYBOARD), tools=tools)

    def unadvertised_rejected(self):
        identity = self.canvas()
        run, scenario = self.start_prompt(identity, "unadvertised_rejected", [
            parity.tool("canvas_edit_storyboard", snapshotHash="deadbeef", nodeId="board", action="append",
                        patch={"durationSeconds": 3, "plotDescription": "should not persist"}),
        ], "现在画布有几个节点")
        view = self.wait_run(run)
        nodes = self.project(identity).get("nodes") or []
        tools = self.tools_at(scenario)
        executed = [(row.get("payload") or {}).get("toolName") for row in view.get("events", [])
                    if row.get("type") in ("tool_completed", "tool_failed", "tool_skipped")]
        self.check("unadvertised storyboard edit is not advertised", "canvas_edit_storyboard" not in tools, tools=tools)
        self.check("unadvertised storyboard edit did not write nodes", nodes == [], nodeCount=len(nodes), status=view["status"])
        self.check("unadvertised storyboard edit was not executed", "canvas_edit_storyboard" not in executed, status=view["status"], executed=executed)

    def stable_prefix(self):
        identity = self.canvas()
        run, scenario = self.start_prompt(identity, "stable_prefix", [
            parity.tool("canvas_get_state"),
            parity.tool("plan_update", items=[{"id": "1", "title": "读取画布", "status": "done"}]),
        ], "现在画布有几个节点")
        view = self.wait_run(run)
        first = self.tools_at(scenario, 0)
        second = self.tools_at(scenario, 1)
        self.check("stable_prefix completed", view["status"] == "completed", status=view["status"])
        self.check("stable_prefix advertised tools unchanged across steps", first == second, first=first, second=second)

    def next_round_recompile(self):
        identity = self.canvas()
        first_run, first_scenario = self.start_prompt(identity, "next_round_first", [parity.tool("canvas_get_state")], "现在画布有几个节点")
        first_view = self.wait_run(first_run)
        self.check("next_round first run completed", first_view["status"] == "completed", status=first_view["status"])
        first_tools = self.tools_at(first_scenario)
        self.check("first round hides arrange_nodes", "canvas_arrange_nodes" not in first_tools, tools=first_tools)
        second_run, second_scenario = self.start_prompt(identity, "next_round_recompile", [parity.tool("canvas_get_state")], "整理排版")
        second_view = self.wait_run(second_run)
        second_tools = self.tools_at(second_scenario)
        self.check("next_round second run completed", second_view["status"] == "completed", status=second_view["status"])
        self.check("next round advertises canvas_arrange_nodes", "canvas_arrange_nodes" in second_tools, tools=second_tools)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", default=".local/agent-tool-disclosure-report.json")
    args = parser.parse_args()
    acceptance = DisclosureAcceptance(args)
    try:
        acceptance.init()
        before_3030 = acceptance.existing_3030
        acceptance.empty_read()
        acceptance.storyboard_inventory()
        acceptance.storyboard_intent()
        acceptance.unadvertised_rejected()
        acceptance.stable_prefix()
        acceptance.next_round_recompile()
        after_3030 = acceptance.inspect_3030()
        acceptance.check("3030 containers unchanged", before_3030 == after_3030, before=before_3030, after=after_3030)
        acceptance.report["passed"] = True
    except Exception as error:
        acceptance.report["passed"] = False
        acceptance.report["failure"] = str(error)
        raise
    finally:
        acceptance.report["finishedAt"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        acceptance.save()
    print(json.dumps({"passed": True, "checks": len(acceptance.report["checks"]), "report": str(acceptance.output)}))


if __name__ == "__main__":
    main()
