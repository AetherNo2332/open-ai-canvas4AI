"""Validate evidence emitted by a real cross-process Agent smoke run.

The validator intentionally does not create containers or infer missing values.
The producer must record the identities observed at each boundary, and this
entry point only accepts a complete, redacted report.
"""
import argparse
import json
from pathlib import Path
import sys


REQUIRED_SERVICES = {"postgres", "redis", "backend", "agent", "web", "model-stub"}
REQUIRED_IDENTITY_FIELDS = (
    "sessionId", "runId", "triggerMessageId", "leaseEpoch", "workItemId",
    "assignmentEpoch", "budgetGrantId", "authorizationVersion", "taskId",
    "operationId", "resourceId", "orderId",
)


def _required(mapping, key, label, errors):
    value = mapping.get(key) if isinstance(mapping, dict) else None
    if value is None or value == "":
        errors.append(f"{label}.{key} is required")
    return value


def validate_evidence(report):
    errors = []
    if not isinstance(report, dict):
        return ["evidence report must be an object"]
    project = _required(report, "project", "report", errors)
    if report.get("secretsRedacted") is not True:
        errors.append("evidence is not marked secretsRedacted")

    compose = report.get("compose")
    compose_project = _required(compose, "project", "compose", errors)
    if project and compose_project and compose_project != project:
        errors.append("compose project does not match report project")
    if not isinstance(compose, dict) or compose.get("isolated") is not True:
        errors.append("compose evidence is not marked isolated")
    services = set(compose.get("services", [])) if isinstance(compose, dict) else set()
    missing_services = sorted(REQUIRED_SERVICES - services)
    if missing_services:
        errors.append("compose services missing: " + ", ".join(missing_services))

    model = report.get("deterministicModel")
    _required(model, "name", "deterministicModel", errors)
    request_count = model.get("requestCount") if isinstance(model, dict) else 0
    if not isinstance(request_count, int) or request_count < 1:
        errors.append("deterministicModel.requestCount must be positive")

    worker = report.get("worker")
    _required(worker, "id", "worker", errors)
    if not isinstance(worker, dict) or worker.get("engine") != "pi":
        errors.append("worker engine must be pi")
    if not isinstance(worker, dict) or worker.get("protocol") != "canvas-pi-wire/v4":
        errors.append("worker protocol must be canvas-pi-wire/v4")
    if not isinstance(worker, dict) or str(worker.get("sessionFormat")) != "3":
        errors.append("worker session format must be 3")

    identity = report.get("identity")
    for field in REQUIRED_IDENTITY_FIELDS:
        _required(identity, field, "identity", errors)
    for field in ("leaseEpoch", "assignmentEpoch", "authorizationVersion"):
        value = identity.get(field) if isinstance(identity, dict) else None
        if not isinstance(value, int) or value < 1:
            errors.append(f"identity.{field} must be a positive integer")

    checks = report.get("checks")
    if not isinstance(checks, list) or not checks:
        errors.append("checks must contain at least one result")
    elif any(not isinstance(check, dict) or check.get("passed") is not True for check in checks):
        errors.append("checks contains a failed result")
    return errors


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", required=True, type=Path)
    args = parser.parse_args(argv)
    try:
        report = json.loads(args.report.read_text(encoding="utf-8-sig"))
    except (OSError, json.JSONDecodeError) as error:
        print(json.dumps({"passed": False, "errors": [f"cannot read report: {error}"]}, ensure_ascii=False))
        return 2
    errors = validate_evidence(report)
    print(json.dumps({"passed": not errors, "errors": errors}, ensure_ascii=False, indent=2))
    return 0 if not errors else 1


if __name__ == "__main__":
    sys.exit(main())
