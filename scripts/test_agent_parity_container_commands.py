"""Container selection checks; no Docker process or container is started."""
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location(
    "agent_canvas_parity", Path(__file__).with_name("verify-agent-canvas-parity.py")
)
parity = importlib.util.module_from_spec(spec)
spec.loader.exec_module(parity)


class ParityContainerCommandsTest(unittest.TestCase):
    def setUp(self):
        self.acceptance = parity.Acceptance.__new__(parity.Acceptance)
        self.result = SimpleNamespace(returncode=0, stdout="ok", stderr="")

    def test_discovery_is_scoped_to_the_isolated_project(self):
        with patch.object(parity.subprocess, "run", return_value=self.result) as run:
            self.acceptance.docker("ps", "-q")
        command = run.call_args.args[0]
        self.assertEqual(command[:3], ["docker", "ps", "--quiet"])
        self.assertIn("label=com.docker.compose.project=canvas-agent-parity", command)

    def test_exec_resolves_project_and_service_labels_and_preserves_stdin(self):
        with patch.object(parity.subprocess, "check_output", return_value="stub-id\n") as select:
            with patch.object(parity.subprocess, "run", return_value=self.result) as run:
                self.acceptance.docker("exec", "-T", "model-stub", "node", "-", input="fixture script")
        filters = select.call_args.args[0]
        self.assertIn("label=com.docker.compose.project=canvas-agent-parity", filters)
        self.assertIn("label=com.docker.compose.service=model-stub", filters)
        self.assertEqual(run.call_args.args[0], ["docker", "exec", "-i", "stub-id", "node", "-"])
        self.assertEqual(run.call_args.kwargs["input"], "fixture script")

    def test_restart_resolves_every_target_before_mutation(self):
        with patch.object(parity.subprocess, "check_output", side_effect=["backend-id\n", "agent-id\n"]):
            with patch.object(parity.subprocess, "run", return_value=self.result) as run:
                self.acceptance.docker("restart", "backend", "agent")
        self.assertEqual(run.call_args.args[0], ["docker", "restart", "backend-id", "agent-id"])

    def test_unknown_missing_or_ambiguous_targets_never_restart(self):
        for service, matches in [("production", "prod-id\n"), ("agent", ""), ("agent", "one\ntwo\n")]:
            with self.subTest(service=service, matches=matches):
                with patch.object(parity.subprocess, "check_output", side_effect=["backend-id\n", matches]):
                    with patch.object(parity.subprocess, "run") as run:
                        with self.assertRaises(AssertionError):
                            self.acceptance.docker("restart", "backend", service)
                        run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
