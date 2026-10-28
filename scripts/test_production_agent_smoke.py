"""Safety and receipt assertions for the CI production smoke; never starts Docker."""
import importlib.util
import io
import json
from pathlib import Path
import re
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import urllib.error

MODULE = Path(__file__).with_name("verify-production-agent-smoke.py")
spec = importlib.util.spec_from_file_location("production_smoke", MODULE)
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class ProductionSmokeTests(unittest.TestCase):
    def test_empty_control_body_still_posts(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            with patch.object(harness, "compose", return_value=SimpleNamespace(stdout='{}')) as compose:
                harness.stub("media-control", {})
            self.assertIn("method:'POST'", compose.call_args.args[-1])
            self.assertIn("SMOKE_BODY={}", compose.call_args.args)

    def test_http_failure_explains_server_rejection_and_redacts_secrets(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            secret = "test-private-upstream-key"
            harness.secrets.add(secret)
            error = urllib.error.HTTPError(harness.base + "/test", 400, "Bad request", {},
                io.BytesIO(json.dumps({"msg": "missing capabilityConfig", "key": secret}).encode()))
            with patch.object(harness.http, "open", side_effect=error):
                with self.assertRaisesRegex(AssertionError, "missing capabilityConfig") as raised:
                    harness.api("POST", "/test", {})
            self.assertNotIn(secret, str(raised.exception))

    def test_health_wait_retries_startup_transport_errors(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            with patch.object(harness, "api", side_effect=[
                    ConnectionResetError(104, "Connection reset by peer"),
                    urllib.error.URLError(ConnectionRefusedError(111, "Connection refused")),
                    {"ready": True}]) as api, patch.object(smoke.time, "sleep"):
                self.assertTrue(smoke.wait(harness.health_ready, seconds=1))
            self.assertEqual(api.call_count, 3)

    def test_health_wait_does_not_hide_http_or_payload_errors(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            http_error = urllib.error.HTTPError(harness.base + "/health", 401, "Unauthorized", {}, None)
            for error in (http_error, urllib.error.URLError(ValueError("bad URL")), ValueError("invalid response")):
                with self.subTest(error=type(error).__name__), patch.object(harness, "api", side_effect=error):
                    with self.assertRaises(type(error)):
                        harness.health_ready()
            http_error.close()

    def test_seeded_channel_model_is_updated_by_id(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            harness.channel = "channel-1"
            with patch.object(harness, "api", side_effect=[
                    {"models": [{"id": "model-1", "modelKey": "agent-parity-model", "enabled": False}]},
                    {"model": {"id": "model-1"}}]) as api:
                harness.configure_channel_model()
            self.assertEqual(api.call_count, 2)
            self.assertEqual(api.call_args_list[0].args, ("GET", "/admin/channels/channel-1/models"))
            self.assertEqual(api.call_args_list[1].args[:2],
                             ("PATCH", "/admin/channels/channel-1/models/model-1"))
            self.assertEqual(api.call_args_list[1].args[2]["modelKey"], "agent-parity-model")
            self.assertTrue(api.call_args_list[1].args[2]["enabled"])

    def test_preflight_accepts_persisted_finish_and_canvas_receipts(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            scenario, canvas = "smoke_case", "canvas-1"
            view = {"id": "run-1", "status": "completed", "events": [
                {"type": "canvas_updated", "payload": {"canvasId": canvas}},
                {"type": "tool_completed", "payload": {"toolName": "canvas_apply_ops"}},
                {"type": "tool_completed", "payload": {"toolName": "finish_run", "result": {
                    "runId": "run-1", "committed": True, "terminalStatus": "completed"}}},
                {"type": "assistant_message", "payload": {"final": True, "via": "finish_run"}}]}
            with patch.object(harness, "stub", return_value={"requests": [{"scenario": scenario}]}), patch.object(
                    harness, "api", return_value={"project": {"nodes": [{"id": scenario}]}}):
                self.assertTrue(harness.preflight_receipt(scenario, canvas, view))

    def test_preflight_rejects_missing_finish_or_canvas_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            scenario, canvas = "smoke_case", "canvas-1"
            base_events = [
                {"type": "tool_completed", "payload": {"toolName": "canvas_apply_ops"}},
                {"type": "tool_completed", "payload": {"toolName": "finish_run", "result": {
                    "runId": "run-1", "committed": True, "terminalStatus": "completed"}}},
                {"type": "assistant_message", "payload": {"final": True, "via": "finish_run"}}]
            cases = (
                ("missing canvas write", base_events[1:], [{"id": scenario}]),
                ("missing finish receipt", [base_events[0], base_events[2]], [{"id": scenario}]),
                ("uncommitted finish", [base_events[0], {"type": "tool_completed", "payload": {
                    "toolName": "finish_run", "result": {"runId": "run-1", "committed": False,
                                                         "terminalStatus": "completed"}}}, base_events[2]], [{"id": scenario}]),
                ("missing final message", base_events[:2], [{"id": scenario}]),
                ("missing persisted node", base_events, []),
            )
            for name, events, nodes in cases:
                with self.subTest(name=name), patch.object(harness, "stub", return_value={
                        "requests": [{"scenario": scenario}]}), patch.object(
                        harness, "api", return_value={"project": {"nodes": nodes}}):
                    self.assertFalse(harness.preflight_receipt(scenario, canvas, {
                        "id": "run-1", "status": "completed", "events": events}))

    def test_compose_commands_are_bound_to_unique_project_and_three_inputs(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            self.assertTrue(harness.project.startswith("canvas-production-smoke-"))
            self.assertEqual(harness.command.count("-f"), 3)
            self.assertEqual(harness.command[harness.command.index("-p") + 1], harness.project)
            self.assertIn("ports: !override", harness.command[-1] and Path(harness.command[-1]).read_text())
            self.assertNotIn("3000:3000", Path(harness.command[-1]).read_text())
            with patch.object(smoke.subprocess, "run", return_value=SimpleNamespace(returncode=0, stdout="", stderr="")) as run:
                harness.compose("down", "--volumes", "--remove-orphans")
            command = run.call_args.args[0]
            self.assertEqual(command[-3:], ["down", "--volumes", "--remove-orphans"])
            self.assertEqual(command[command.index("-p") + 1], harness.project)

    def test_receipt_requires_terminal_completion(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            with patch.object(harness, "view", return_value={"status": "failed", "events": []}):
                with self.assertRaisesRegex(AssertionError, "real run reaches completed"):
                    harness.complete({"id": "run-test"})
            report = json.loads(harness.output.read_text())
            self.assertFalse(report["checks"][-1]["passed"])
            self.assertEqual(report["runs"][0]["status"], "failed")

    def test_rejected_config_never_creates_or_cleans_resources(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            invalid = json.dumps({"services": {"backend": {}}})
            with patch.object(harness, "compose", return_value=SimpleNamespace(stdout=invalid)) as compose:
                with self.assertRaisesRegex(AssertionError, "seven services"):
                    harness.execute()
                harness.cleanup()
            self.assertEqual([call.args[0] for call in compose.call_args_list], ["config"])

    def test_evidence_write_failure_still_attempts_cleanup(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            def started_then_failed():
                harness.isolated = harness.resources_created = True
                raise AssertionError("smoke failed")
            with patch.object(harness, "execute", side_effect=started_then_failed), patch.object(
                    harness, "compose", return_value=SimpleNamespace(stdout="")), patch.object(
                    harness, "save", side_effect=OSError("report disk full")), patch.object(
                    harness, "cleanup") as cleanup:
                with self.assertRaisesRegex(AssertionError, "smoke failed"):
                    smoke.run_smoke(harness)
            cleanup.assert_called_once()
            self.assertEqual(harness.report["evidenceError"], "OSError")

    def test_evidence_write_failure_after_success_still_cleans_and_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            def started():
                harness.isolated = harness.resources_created = True
            with patch.object(harness, "execute", side_effect=started), patch.object(
                    harness, "collect", side_effect=OSError("report disk full")), patch.object(
                    harness, "cleanup") as cleanup:
                with self.assertRaisesRegex(OSError, "report disk full"):
                    smoke.run_smoke(harness)
            cleanup.assert_called_once()

    def test_migration_failure_exercises_up_dependencies_and_keeps_diagnostics(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            results = [SimpleNamespace(returncode=1, stdout="", stderr="dependency failed: migrate"),
                       SimpleNamespace(returncode=0, stdout="password authentication failed", stderr="")]
            with patch.object(smoke.subprocess, "run", side_effect=results) as run, patch.object(
                    harness, "compose", return_value=SimpleNamespace(stdout="")) as compose:
                harness.verify_migration_gate()
            command = run.call_args_list[0].args[0]
            self.assertEqual(command[-6:], ["up", "-d", "--wait", "backend", "web", "agent"])
            self.assertIn("-f", command)
            self.assertEqual([call.args[2] for call in compose.call_args_list], ["backend", "web", "agent"])
            self.assertIn("password authentication failed", harness.report["migrationFailure"]["diagnostics"])

    def test_cleanup_refuses_unlabelled_volume_before_down(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            harness.isolated = harness.resources_created = True
            name = harness.project + "_postgres-data"
            harness.expected_volumes = {name}
            def fake_run(command, **_):
                if command[1:3] == ["volume", "inspect"]:
                    return SimpleNamespace(returncode=0, stdout=json.dumps([{"Name": name, "Labels": {}}]), stderr="")
                return SimpleNamespace(returncode=0, stdout="", stderr="")
            with patch.object(smoke.subprocess, "run", side_effect=fake_run), patch.object(harness, "compose") as compose:
                with self.assertRaisesRegex(AssertionError, "lacks project label"):
                    harness.cleanup()
            compose.assert_not_called()

    def test_all_generated_credentials_are_redacted_in_report(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            account, model, cookie = "account-secret-value", "model-secret-value", "cookie-secret-value"
            harness.secrets.update((account, model, cookie))
            harness.report["failure"] = f"{account} {model} {cookie} {harness.env['CANVAS_AGENT_WORKER_TOKEN']} {harness.env['POSTGRES_PASSWORD']}"
            harness.save()
            body = harness.output.read_text()
            for secret in (account, model, cookie, harness.env["CANVAS_AGENT_WORKER_TOKEN"], harness.env["POSTGRES_PASSWORD"]):
                self.assertNotIn(secret, body)
            self.assertIn("[redacted]", body)

    def test_smoke_uses_only_registered_canvas_routes(self):
        source = MODULE.read_text(encoding="utf-8")
        calls = re.findall(r'self\.api\(\s*"([A-Z]+)"\s*,\s*f?"([^"]*)"', source)
        self.assertTrue(calls)
        for method, path in calls:
            with self.subTest(method=method, path=path):
                self.assertNotIn("/desktop/", path)
                self.assertFalse(method == "PUT" and path.startswith("/canvas-projects/"))

    def test_canvas_is_created_through_operations_route(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            with patch.object(harness, "api", return_value={}) as api:
                identity = harness.canvas()
            method, path, body = api.call_args.args
            self.assertEqual((method, path), ("PATCH", f"/canvas-projects/{identity}/operations"))
            self.assertEqual(body["baseRevision"], 0)
            self.assertTrue(body["mutationId"])
            self.assertEqual(body["operations"][0], {"type": "create_canvas", "id": identity, "patch": {"title": "CI smoke"}})

    def test_override_names_every_built_image_for_project(self):
        with tempfile.TemporaryDirectory() as directory:
            harness = smoke.Smoke(directory, Path(directory) / "report.json")
            override = Path(harness.command[-1]).read_text()
            for key in ("CANVAS_BACKEND_IMAGE", "CANVAS_AGENT_IMAGE", "CANVAS_WEB_IMAGE", "CANVAS_MODEL_STUB_IMAGE"):
                self.assertIn("image: ${" + key + "}", override)
                self.assertTrue(harness.env[key].startswith(harness.project + "-"))


if __name__ == "__main__":
    unittest.main()
