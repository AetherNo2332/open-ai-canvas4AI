"""Safety and receipt assertions for the CI production smoke; never starts Docker."""
import importlib.util
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

MODULE = Path(__file__).with_name("verify-production-agent-smoke.py")
spec = importlib.util.spec_from_file_location("production_smoke", MODULE)
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class ProductionSmokeTests(unittest.TestCase):
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


if __name__ == "__main__":
    unittest.main()
