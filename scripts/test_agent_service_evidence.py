"""Contract tests for the cross-process Agent evidence validator."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


MODULE = Path(__file__).with_name("verify-agent-service-evidence.py")
SPEC = importlib.util.spec_from_file_location("agent_service_evidence", MODULE)
evidence = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(evidence)


def valid_report():
    return {
        "project": "canvas-agent-service-smoke-123",
        "secretsRedacted": True,
        "compose": {"project": "canvas-agent-service-smoke-123", "isolated": True,
                     "services": ["postgres", "redis", "backend", "agent", "web", "model-stub"]},
        "deterministicModel": {"name": "agent-parity-model", "requestCount": 2},
        "worker": {"id": "smoke-worker", "engine": "pi", "protocol": "canvas-pi-wire/v4", "sessionFormat": "3"},
        "identity": {"sessionId": "session-1", "runId": "run-1", "triggerMessageId": "message-1",
                     "leaseEpoch": 2, "workItemId": "work-1", "assignmentEpoch": 3,
                     "budgetGrantId": "budget-1", "authorizationVersion": 4, "taskId": "task-1",
                     "operationId": "operation-1", "resourceId": "resource-1", "orderId": "order-1"},
        "checks": [{"name": "session scope", "passed": True}],
    }


class AgentServiceEvidenceTests(unittest.TestCase):
    def test_accepts_complete_redacted_pi_evidence(self):
        self.assertEqual(evidence.validate_evidence(valid_report()), [])

    def test_rejects_missing_identity_and_mismatched_compose_project(self):
        report = valid_report()
        del report["identity"]["operationId"]
        report["compose"]["project"] = "other-project"
        errors = evidence.validate_evidence(report)
        self.assertIn("compose project does not match report project", errors)
        self.assertIn("identity.operationId is required", errors)

    def test_rejects_unredacted_or_non_pi_worker_evidence(self):
        report = valid_report()
        report["secretsRedacted"] = False
        report["worker"]["engine"] = "go-runtime"
        errors = evidence.validate_evidence(report)
        self.assertIn("evidence is not marked secretsRedacted", errors)
        self.assertIn("worker engine must be pi", errors)

    def test_reads_report_written_with_a_utf8_bom(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.json"
            path.write_text(json.dumps(valid_report()), encoding="utf-8-sig")
            self.assertEqual(evidence.main(["--report", str(path)]), 0)


if __name__ == "__main__":
    unittest.main()
