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
        self.env = {**os.environ, "POSTGRES_DB": "canvas_smoke", "POSTGRES_USER": "canvas_smoke",
                    "POSTGRES_PASSWORD": password,
                    "DATABASE_URL": f"postgres://canvas_smoke:{password}@postgres:5432/canvas_smoke?sslmode=disable",
                    "CANVAS_BACKEND_IMAGE": self.project + "-backend:ci", "CANVAS_AGENT_IMAGE": self.project + "-agent:ci",
                    "CANVAS_WEB_IMAGE": self.project + "-web:ci", "CANVAS_AGENT_INTERNAL_TOKEN": secrets.token_urlsafe(48),
                    "CANVAS_REGISTRATION_ENABLED": "true", "CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS": "model-stub",
                    "CANVAS_CORS_ORIGINS": f"http://127.0.0.1:{self.port}", "CANVAS_HTTP_PORT": str(self.port),
                    "CANVAS_UPDATER_SOCKET_DIR": str(self.directory / "updater-socket"), "CANVAS_SHUTDOWN_TIMEOUT": "15s"}
        (self.directory / "updater-socket").mkdir()
        override = self.directory / "override.yml"
        override.write_text('''services:
  model-stub:
    build:
      context: .
      dockerfile: tools/agent-parity-stub.Dockerfile
  web:
    ports: !override
      - "127.0.0.1:${CANVAS_HTTP_PORT}:3000"
  backend:
    environment:
      CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS: model-stub
  agent:
    environment:
      CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS: model-stub
''', encoding="utf-8")
        self.command = ["docker", "compose", "--project-directory", str(ROOT), "-p", self.project,
                        "-f", str(ROOT / "docker-compose.deploy.yml"), "-f", str(ROOT / "docker-compose.build.yml"),
                        "-f", str(override)]
        self.http = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                                  urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.report = {"project": self.project, "checks": [], "runs": []}

    def save(self):
        self.output.parent.mkdir(parents=True, exist_ok=True)
        self.output.write_text(json.dumps(self.report, indent=2), encoding="utf-8")

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

    def api(self, method, path, body=None):
        request = urllib.request.Request(self.base + path, method=method,
                                         data=None if body is None else json.dumps(body).encode(),
                                         headers={"Content-Type": "application/json", "Origin": self.base[:-4]})
        with self.http.open(request, timeout=25) as response:
            payload = json.loads(response.read())
        if "code" in payload and payload["code"] != 0:
            raise AssertionError(f"{method} {path}: {payload.get('reason')}")
        return payload.get("data", payload)

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
            logs = self.compose("logs", "--no-color", "--tail", "120", success=False).stdout
            for secret in (self.env["POSTGRES_PASSWORD"], self.env["CANVAS_AGENT_INTERNAL_TOKEN"]):
                logs = logs.replace(secret, "[redacted]")
            logs = re.sub(r"(?i)(postgres(?:ql)?://)[^\s@]+@", r"\1[redacted]@", logs)
            (self.output.parent / "production-smoke-services.log").write_text(logs[-50000:], encoding="utf-8")
        except Exception as error:
            self.report["diagnosticsError"] = type(error).__name__
        self.save()

    def execute(self):
        config = json.loads(self.compose("config", "--format", "json").stdout)
        self.check("production deploy/build plus stub resolves seven services", set(config["services"]) == SERVICES,
                   services=sorted(config["services"]))
        ports = config["services"]["web"].get("ports", [])
        self.check("web binds only allocated loopback port", len(ports) == 1 and
                   ports[0]["host_ip"] == "127.0.0.1" and ports[0]["published"] == str(self.port))
        volumes = [mount for service in config["services"].values() for mount in service.get("volumes", [])]
        self.check("all persistent storage is project-scoped", all(
            volume["name"].startswith(self.project + "_") for volume in config.get("volumes", {}).values()) and
            all(mount["type"] == "volume" or
                (mount["type"] == "bind" and mount["source"] == str(self.directory / "updater-socket"))
                for mount in volumes))
        self.compose("build", "backend", "agent", "web", "model-stub", timeout=1200)
        self.compose("up", "-d", "--wait", "postgres", "redis")
        bad = self.compose("run", "--rm", "--no-deps", "-e",
                           "DATABASE_URL=postgres://bad:bad@postgres:5432/missing?sslmode=disable", "migrate",
                           timeout=60, success=False)
        failure_text = (bad.stdout + bad.stderr).lower()
        self.check("migration reports invalid database credentials", bad.returncode != 0 and
                   ("password authentication failed" in failure_text or "sqlstate 28p01" in failure_text),
                   stage="migrate", exitCode=bad.returncode)
        self.check("failed migration has not admitted backend", not self.compose("ps", "-q", "backend").stdout.strip())
        self.compose("up", "-d", "migrate", "backend", "web", "model-stub", timeout=300)
        wait(lambda: self.api("GET", "/health").get("ready") is True)
        account = {"username": "smoke_" + uuid.uuid4().hex[:10], "password": secrets.token_urlsafe(24)}
        self.api("POST", "/auth/register", {**account, "acceptedTerms": True})
        self.api("POST", "/auth/login", account)
        availability = self.api("GET", "/agent/capabilities")["availability"]
        self.check("absent worker has explicit offline stage", availability["readyWorkers"] == 0 and
                   availability["queueReason"] == "worker_offline", stage="worker_not_ready",
                   queueReason=availability["queueReason"])
        probe = self.compose("run", "--rm", "--no-deps", "agent", "node", "-e",
            "fetch('http://backend:8080/internal-agent/capacity',{method:'POST',headers:{Authorization:'Bearer '+process.env.CANVAS_AGENT_INTERNAL_TOKEN,'X-Agent-Protocol-Version':'canvas-pi-wire/v3','X-Pi-SDK-Version':'0.87.1','X-Pi-Session-Format':'3','Content-Type':'application/json'},body:JSON.stringify({owner:'ci-unready-probe',active:0,capacity:1,readiness:'degraded',readinessReason:'CI startup gate'})}).then(r=>{console.log(r.status);process.exit(r.status===200?0:1)})",
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
        self.channel = self.api("POST", "/admin/channels", {"name": "Production CI stub",
            "baseUrl": "http://model-stub:8080/v1", "apiKey": "non-secret-ci-key",
            "models": ["agent-parity-model"], "enabled": True})["channel"]["id"]
        self.api("POST", f"/admin/channels/{self.channel}/models", {"modelKey": "agent-parity-model",
            "providerModelKey": "agent-parity-model", "displayName": "CI stub", "capability": "text",
            "protocol": "chat-completion", "billingMode": "token", "priceConfigured": True, "enabled": True,
            "inputTokenPriceMicrocredits": 0, "outputTokenPriceMicrocredits": 0,
            "capabilityConfig": {"version": 1, "text": {"streaming": False, "thinking": False,
                "contextWindowTokens": 128000, "maxOutputTokens": 32000, "references": {"promptMaxChars": 32000}}}})
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


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="canvas-production-smoke-") as directory:
        smoke = Smoke(directory, args.output)
        try:
            smoke.execute()
            smoke.report["passed"] = True
        except Exception as error:
            smoke.report["passed"], smoke.report["failure"] = False, str(error)
            raise
        finally:
            smoke.collect()
            smoke.compose("down", "--volumes", "--remove-orphans", timeout=180, success=False)


if __name__ == "__main__":
    main()
