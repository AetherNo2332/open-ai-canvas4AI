"""CI-only real deploy/build Compose smoke. The model endpoint is deterministic."""
import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]
SERVICES = {"postgres", "redis", "migrate", "backend", "agent", "web", "model-stub"}
TERMINAL = {"completed", "failed", "cancelled", "rejected"}


def wait(predicate, seconds=150):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        value = predicate()
        if value:
            return value
        time.sleep(.5)
    raise AssertionError(f"smoke stage timed out after {seconds}s")


class Smoke:
    def __init__(self, directory, output):
        self.project = "canvas-production-smoke-" + uuid.uuid4().hex[:10]
        self.directory, self.output = Path(directory), Path(output)
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            self.port = listener.getsockname()[1]
        self.base = f"http://127.0.0.1:{self.port}/api"
        password = secrets.token_urlsafe(24)
        self.secrets = {password}
        self.isolated = False
        self.resources_created = False
        self.expected_volumes = set()
        self.env = {**os.environ, "POSTGRES_DB": "canvas_smoke", "POSTGRES_USER": "canvas_smoke",
                    "POSTGRES_PASSWORD": password,
                    "DATABASE_URL": f"postgres://canvas_smoke:{password}@postgres:5432/canvas_smoke?sslmode=disable",
                    "CANVAS_BACKEND_IMAGE": self.project + "-backend:ci", "CANVAS_AGENT_IMAGE": self.project + "-agent:ci",
                    "CANVAS_WEB_IMAGE": self.project + "-web:ci", "CANVAS_AGENT_INTERNAL_TOKEN": secrets.token_urlsafe(48),
                    "CANVAS_AGENT_WORKER_ID": "smoke-worker", "CANVAS_AGENT_WORKER_TOKEN": secrets.token_urlsafe(48),
                    "CANVAS_REGISTRATION_ENABLED": "true", "CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS": "model-stub",
                    "CANVAS_CORS_ORIGINS": f"http://127.0.0.1:{self.port}", "CANVAS_HTTP_PORT": str(self.port),
                    "CANVAS_UPDATER_SOCKET_DIR": str(self.directory / "updater-socket"), "CANVAS_SHUTDOWN_TIMEOUT": "15s"}
        (self.directory / "updater-socket").mkdir()
        override = self.directory / "override.yml"
        override.write_text('''services:
  model-stub:
    image: ${CANVAS_MODEL_STUB_IMAGE}
    build:
      context: .
      dockerfile: tools/agent-parity-stub.Dockerfile
  web:
    image: ${CANVAS_WEB_IMAGE}
    ports: !override
      - "127.0.0.1:${CANVAS_HTTP_PORT}:3000"
  backend:
    image: ${CANVAS_BACKEND_IMAGE}
    environment:
      CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS: model-stub
  agent:
    image: ${CANVAS_AGENT_IMAGE}
    environment:
      CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS: model-stub
  migrate:
    image: ${CANVAS_BACKEND_IMAGE}
''', encoding="utf-8")
        self.env["CANVAS_MODEL_STUB_IMAGE"] = self.project + "-model-stub:ci"
        self.secrets.update((self.env["CANVAS_AGENT_INTERNAL_TOKEN"], self.env["CANVAS_AGENT_WORKER_TOKEN"]))
        self.command = ["docker", "compose", "--project-directory", str(ROOT), "-p", self.project,
                        "-f", str(ROOT / "docker-compose.deploy.yml"), "-f", str(ROOT / "docker-compose.build.yml"),
                        "-f", str(override)]
        self.cookiejar = http.cookiejar.CookieJar()
        self.http = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                                  urllib.request.HTTPCookieProcessor(self.cookiejar))
        self.report = {"project": self.project, "checks": [], "runs": []}

    def redact(self, value):
        if isinstance(value, dict):
            return {key: self.redact(item) for key, item in value.items()}
        if isinstance(value, list):
            return [self.redact(item) for item in value]
        if not isinstance(value, str):
            return value
        for secret in sorted(self.secrets, key=len, reverse=True):
            if secret:
                value = value.replace(secret, "[redacted]")
        return re.sub(r"(?i)(postgres(?:ql)?://)[^\s@]+@", r"\1[redacted]@", value)

    def save(self):
        self.output.parent.mkdir(parents=True, exist_ok=True)
        self.output.write_text(json.dumps(self.redact(self.report), indent=2), encoding="utf-8")

    def check(self, name, passed, **details):
        self.report["checks"].append({"name": name, "passed": bool(passed), **details})
        self.save()
        if not passed:
            raise AssertionError(name)
        print("PASS:", name, flush=True)

    def compose(self, *args, timeout=180, success=True):
        result = subprocess.run([*self.command, *args], cwd=ROOT, env=self.env, capture_output=True,
                                text=True, timeout=timeout)
        if success and result.returncode:
            raise AssertionError(f"Compose {args[0]} stage exited {result.returncode}; see service logs")
        return result

    def cleanup(self):
        if not (self.isolated and self.resources_created):
            return
        containers = subprocess.run(["docker", "ps", "-aq", "--filter",
            f"label=com.docker.compose.project={self.project}"], capture_output=True, text=True, timeout=30, check=True).stdout.split()
        if containers:
            rows = json.loads(subprocess.check_output(["docker", "inspect", *containers], text=True, timeout=30))
            if any(row["Config"]["Labels"].get("com.docker.compose.project") != self.project or
                   row["Config"]["Labels"].get("com.docker.compose.service") not in SERVICES for row in rows):
                raise AssertionError("cleanup refused: unexpected container labels")
        volumes = subprocess.run(["docker", "volume", "ls", "-q", "--filter",
            f"label=com.docker.compose.project={self.project}"], capture_output=True, text=True, timeout=30, check=True).stdout.split()
        if volumes:
            rows = json.loads(subprocess.check_output(["docker", "volume", "inspect", *volumes], text=True, timeout=30))
            if any(row["Name"] not in self.expected_volumes or
                   (row.get("Labels") or {}).get("com.docker.compose.project") != self.project for row in rows):
                raise AssertionError("cleanup refused: unexpected volume labels or names")
        for name in self.expected_volumes:
            result = subprocess.run(["docker", "volume", "inspect", name], capture_output=True, text=True, timeout=30)
            if result.returncode:
                if "no such volume" not in result.stderr.lower():
                    raise AssertionError("cleanup refused: could not inspect expected volume")
                continue
            row = json.loads(result.stdout)[0]
            if row["Name"] != name or (row.get("Labels") or {}).get("com.docker.compose.project") != self.project:
                raise AssertionError("cleanup refused: expected volume lacks project label")
        self.compose("down", "--volumes", "--remove-orphans", timeout=180)

    def api(self, method, path, body=None):
        request = urllib.request.Request(self.base + path, method=method,
                                         data=None if body is None else json.dumps(body).encode(),
                                         headers={"Content-Type": "application/json", "Origin": self.base[:-4]})
        with self.http.open(request, timeout=25) as response:
            payload = json.loads(response.read())
        self.secrets.update(cookie.value for cookie in self.cookiejar)
        if "code" in payload and payload["code"] != 0:
            raise AssertionError(self.redact(f"{method} {path}: {payload.get('reason')}"))
        return payload.get("data", payload)

    def health_ready(self):
        try:
            return self.api("GET", "/health").get("ready") is True
        except (ConnectionError, TimeoutError):
            return False
        except urllib.error.HTTPError:
            raise
        except urllib.error.URLError as error:
            if isinstance(error.reason, (ConnectionError, TimeoutError)):
                return False
            raise

    def stub(self, path, body=None):
        script = ("const r=await fetch('http://127.0.0.1:8080/" + path + "'," +
                  ("{method:'POST',headers:{'Content-Type':'application/json'},body:process.env.SMOKE_BODY}" if body else "{}") +
                  ");if(!r.ok)process.exit(2);console.log(await r.text())")
        return json.loads(self.compose("exec", "-T", "-e", "SMOKE_BODY=" + json.dumps(body), "model-stub",
                                       "node", "--input-type=module", "-e", script).stdout)

    def view(self, run):
        return self.api("GET", f"/agent/runs/{run['id']}?eventLimit=200")["run"]

    def complete(self, run):
        view = wait(lambda: (row if (row := self.view(run))["status"] in TERMINAL else None), 180)
        self.report["runs"].append({"id": run["id"], "status": view["status"], "events": [
            {"seq": e["seq"], "type": e["type"], "tool": e.get("payload", {}).get("toolName")}
            for e in view["events"]]})
        self.check("real run reaches completed", view["status"] == "completed", status=view["status"])
        return view

    def canvas(self):
        identity = str(uuid.uuid4())
        now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        self.api("PUT", f"/canvas-projects/{identity}", {"project": {"id": identity, "revision": 0,
            "title": "CI smoke", "nodes": [], "connections": [], "createdAt": now, "updatedAt": now,
            "viewport": {"x": 0, "y": 0, "zoom": 1}}})
        return identity

    def start(self, canvas, permission):
        scenario = "smoke_" + uuid.uuid4().hex[:8]
        self.stub("control", {"scenario": scenario, "steps": [
            {"tool": "canvas_get_state"}, {"tool": "canvas_apply_ops", "args": {"snapshotHash": "$snapshotHash",
                "ops": [{"type": "add_node", "id": scenario, "nodeType": "text", "title": "CI node",
                         "content": "persisted by real worker"}]}},
            {"tool": "finish_run", "args": {"summary": "CI smoke done"}}]})
        run = self.api("POST", "/agent/runs", {"canvasId": canvas, "prompt": "AGENT_PARITY_SCENARIO:" + scenario,
            "channelId": self.channel, "channelModelKey": "agent-parity-model", "permissionMode": permission,
            "reasoningMode": "off", "contextScope": ["canvas"], "skillIds": [],
            "budget": {"maxCredits": 1, "maxSteps": 12}, "idempotencyKey": str(uuid.uuid4())})["run"]
        return run, scenario

    def collect(self):
        try:
            self.report["serviceStatus"] = self.compose("ps", "--format", "json", success=False).stdout[-12000:]
            logs = self.compose("logs", "--no-color", "--tail", "120", *sorted(SERVICES), success=False).stdout
            (self.output.parent / "production-smoke-services.log").write_text(self.redact(logs[-50000:]), encoding="utf-8")
        except Exception as error:
            self.report["diagnosticsError"] = type(error).__name__
        self.save()

    def verify_migration_gate(self):
        failure = self.directory / "migration-failure.yml"
        failure.write_text("services:\n  migrate:\n    environment:\n      DATABASE_URL: postgres://bad:bad@postgres:5432/missing?sslmode=disable\n", encoding="utf-8")
        bad_command = [*self.command, "-f", str(failure)]
        bad = subprocess.run([*bad_command, "up", "-d", "--wait", "backend", "web", "agent"],
            cwd=ROOT, env=self.env, capture_output=True, text=True, timeout=180)
        migration_logs = subprocess.run([*bad_command, "logs", "--no-color", "migrate"],
            cwd=ROOT, env=self.env, capture_output=True, text=True, timeout=30)
        failure_text = (bad.stdout + bad.stderr + migration_logs.stdout + migration_logs.stderr).lower()
        self.report["migrationFailure"] = {"exitCode": bad.returncode,
            "diagnostics": self.redact(failure_text[-4000:])}
        self.check("startup migration reports invalid database credentials", bad.returncode != 0 and
                   ("password authentication failed" in failure_text or "sqlstate 28p01" in failure_text),
                   stage="migrate", exitCode=bad.returncode)
        self.check("failed startup migration blocks backend and web", all(
            not self.compose("ps", "-q", service).stdout.strip() for service in ("backend", "web", "agent")),
            stage="dependency_gate")

    def configure_channel_model(self):
        model_path = f"/admin/channels/{self.channel}/models"
        models = self.api("GET", model_path)["models"]
        existing = next((row for row in models if row["modelKey"] == "agent-parity-model"), None)
        if existing is None:
            raise AssertionError("created channel lacks seeded agent-parity-model")
        self.api("PATCH", f"{model_path}/{existing['id']}", {"modelKey": "agent-parity-model",
            "providerModelKey": "agent-parity-model", "displayName": "CI stub", "capability": "text",
            "protocol": "chat-completion", "billingMode": "token", "priceConfigured": True, "enabled": True,
            "inputTokenPriceMicrocredits": 0, "outputTokenPriceMicrocredits": 0,
            "capabilityConfig": {"version": 1, "text": {"streaming": False, "thinking": False,
                "contextWindowTokens": 128000, "maxOutputTokens": 32000,
                "references": {"promptMaxChars": 32000}}}})

    def execute(self):
        config = json.loads(self.compose("config", "--format", "json").stdout)
        self.check("production deploy/build plus stub resolves seven services", set(config["services"]) == SERVICES,
                   services=sorted(config["services"]))
        ports = config["services"]["web"].get("ports", [])
        self.check("web binds only allocated loopback port", len(ports) == 1 and
                   ports[0]["host_ip"] == "127.0.0.1" and ports[0]["published"] == str(self.port))
        volumes = [mount for service in config["services"].values() for mount in service.get("volumes", [])]
        self.check("all persistent storage is project-scoped", all(
            volume["name"].startswith(self.project + "_") and not volume.get("external")
            for volume in config.get("volumes", {}).values()) and
            all(mount["type"] == "volume" or
                (mount["type"] == "bind" and mount["source"] == str(self.directory / "updater-socket"))
                for mount in volumes))
        self.expected_volumes = {volume["name"] for volume in config.get("volumes", {}).values()}
        self.check("all built images use this project", all(
            config["services"][service]["image"] == self.env[variable] for service, variable in (
                ("migrate", "CANVAS_BACKEND_IMAGE"), ("backend", "CANVAS_BACKEND_IMAGE"),
                ("agent", "CANVAS_AGENT_IMAGE"), ("web", "CANVAS_WEB_IMAGE"),
                ("model-stub", "CANVAS_MODEL_STUB_IMAGE"))))
        self.isolated = True
        self.compose("build", "backend", "agent", "web", "model-stub", timeout=1200)
        self.resources_created = True
        self.compose("up", "-d", "--wait", "postgres", "redis")
        self.verify_migration_gate()
        self.compose("up", "-d", "migrate", "backend", "web", "model-stub", timeout=300)
        wait(self.health_ready)
        account = {"username": "smoke_" + uuid.uuid4().hex[:10], "password": secrets.token_urlsafe(24)}
        self.secrets.add(account["password"])
        self.api("POST", "/auth/register", {**account, "acceptedTerms": True})
        self.api("POST", "/auth/login", account)
        self.secrets.update(cookie.value for cookie in self.cookiejar)
        availability = self.api("GET", "/agent/capabilities")["availability"]
        self.check("absent worker has explicit offline stage", availability["readyWorkers"] == 0 and
                   availability["queueReason"] == "worker_offline", stage="worker_not_ready",
                   queueReason=availability["queueReason"])
        probe = self.compose("run", "--rm", "--no-deps", "agent", "node", "-e",
            "fetch('http://backend:8080/internal-agent/capacity',{method:'POST',headers:{Authorization:'Bearer '+process.env.CANVAS_AGENT_WORKER_TOKEN,'X-Agent-Identity':process.env.CANVAS_AGENT_WORKER_ID,'X-Agent-Protocol-Version':'canvas-pi-wire/v4','X-Pi-SDK-Version':'0.87.1','X-Pi-Session-Format':'3','Content-Type':'application/json'},body:JSON.stringify({owner:process.env.CANVAS_AGENT_WORKER_ID+':ci-probe',active:0,capacity:1,readiness:'degraded',readinessReason:'CI startup gate'})}).then(r=>{console.log(r.status);process.exit(r.status===200?0:1)})",
            timeout=60, success=False)
        self.check("unready worker report is accepted", probe.returncode == 0 and probe.stdout.strip() == "200")
        availability = self.api("GET", "/agent/capabilities")["availability"]
        self.check("live unready worker has explicit stage", availability["readyWorkers"] == 0 and
                   availability["queueReason"] == "worker_not_ready", stage="worker_not_ready",
                   queueReason=availability["queueReason"])
        self.compose("up", "-d", "--wait", "agent", timeout=240)
        wait(lambda: self.api("GET", "/agent/capabilities")["availability"]["readyWorkers"] > 0)
        ids = self.compose("ps", "--all", "-q").stdout.split()
        rows = json.loads(subprocess.check_output(["docker", "inspect", *ids], text=True, timeout=30))
        self.check("seven containers carry only this project label", len(rows) == 7 and
                   {r["Config"]["Labels"]["com.docker.compose.service"] for r in rows} == SERVICES and
                   all(r["Config"]["Labels"].get("com.docker.compose.project") == self.project for r in rows))
        wrong = self.compose("exec", "-T", "agent", "node", "-e",
            "fetch('http://backend:8080/internal-agent/events?after=0',{headers:{Authorization:'Bearer wrong-ci-token'}}).then(r=>{console.log(r.status);process.exit(r.status===401?0:1)})",
            success=False)
        self.check("wrong token is rejected at internal auth", wrong.returncode == 0 and wrong.stdout.strip() == "401",
                   stage="internal_auth", httpStatus=wrong.stdout.strip())
        model_key = secrets.token_urlsafe(24)
        self.secrets.add(model_key)
        self.channel = self.api("POST", "/admin/channels", {"name": "Production CI stub",
            "baseUrl": "http://model-stub:8080/v1", "apiKey": model_key,
            "models": ["agent-parity-model"], "enabled": True})["channel"]["id"]
        self.configure_channel_model()
        canvas = self.canvas()
        run, scenario = self.start(canvas, "auto")
        view = self.complete(run)
        self.check("preflight reaches model and durable write receipt", any(r["scenario"] == scenario for r in self.stub("stats")["requests"]) and
                   any(e["type"] == "tool_completed" and e.get("payload", {}).get("toolName") == "canvas_apply_ops" for e in view["events"]) and
                   any(e["type"] == "run_completed" for e in view["events"]) and
                   self.api("GET", f"/canvas-projects/{canvas}")["project"]["nodes"][0]["id"] == scenario)
        approval_canvas = self.canvas()
        approval_run, approval_scenario = self.start(approval_canvas, "request_approval")
        pending = wait(lambda: (r if (r := self.view(approval_run))["status"] in {"waiting_approval", *TERMINAL} else None))
        self.check("approval blocks unapproved write", pending["status"] == "waiting_approval" and
                   not self.api("GET", f"/canvas-projects/{approval_canvas}")["project"]["nodes"])
        approval = next(e["payload"]["approvalId"] for e in pending["events"] if e["type"] == "approval_requested")
        self.api("POST", f"/agent/runs/{approval_run['id']}/approvals/{approval}/decision", {"decision": "approve"})
        self.complete(approval_run)
        self.check("approved write persists", self.api("GET", f"/canvas-projects/{approval_canvas}")["project"]["nodes"][0]["id"] == approval_scenario)
        def replay(after=None):
            headers = {"Last-Event-ID": str(after)} if after is not None else {}
            with self.http.open(urllib.request.Request(self.base + f"/agent/runs/{run['id']}/events", headers=headers), timeout=30) as response:
                return [int(line[3:].strip()) for line in response if line.startswith(b"id:")]
        initial = replay()
        cursor = initial[len(initial) // 2]
        self.check("SSE cursor replays later persisted events", initial == sorted(set(initial)) and
                   replay(cursor) == [value for value in initial if value > cursor], eventCount=len(initial))


def run_smoke(smoke):
    try:
        try:
            smoke.execute()
            smoke.report["passed"] = True
        except Exception as error:
            smoke.report["passed"], smoke.report["failure"] = False, smoke.redact(str(error))
            raise
    finally:
        primary_error = sys.exc_info()[1]
        evidence_error = None
        cleanup_error = None
        try:
            smoke.collect()
        except Exception as error:
            evidence_error = error
            smoke.report["evidenceError"] = type(error).__name__
            smoke.report["passed"] = False
        finally:
            try:
                smoke.cleanup()
            except Exception as error:
                cleanup_error = error
                smoke.report["cleanupError"] = smoke.redact(str(error))
                smoke.report["passed"] = False
                try:
                    smoke.save()
                except Exception:
                    pass  # Evidence failure must not hide the smoke or cleanup error.
        if primary_error is None:
            if evidence_error is not None:
                raise evidence_error
            if cleanup_error is not None:
                raise cleanup_error


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="canvas-production-smoke-") as directory:
        run_smoke(Smoke(directory, args.output))


if __name__ == "__main__":
    main()
