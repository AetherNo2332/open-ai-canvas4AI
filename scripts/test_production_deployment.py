"""Bounded deployment checks; never builds images or starts containers."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
BASH = shutil.which("bash") if os.name != "nt" else r"C:\Program Files\Git\bin\bash.exe"
TOKEN = "deployment-test-token-" + "x" * 32


class ProductionDeploymentTests(unittest.TestCase):
    def test_image_install_starts_after_tokens_and_pins(self):
        source = (ROOT / "scripts/install-server-image.sh").read_text(encoding="utf-8").rsplit('\nmain "$@"', 1)[0]
        updater = (ROOT / "scripts/install-host-updater.sh").read_text(encoding="utf-8").rsplit('\nmain "$@"', 1)[0]
        for existing, requested in ((False, True), (True, True), (True, False)):
            with self.subTest(existing=existing, requested=requested), tempfile.TemporaryDirectory(prefix="canvas install ") as directory:
                path = Path(directory)
                if existing:
                    (path / ".env").write_text("POSTGRES_PASSWORD=test\nDATABASE_URL=test\nCANVAS_IMAGE_TAG=1.5.0\nCANVAS_AGENT_INTERNAL_TOKEN=" + TOKEN + "\nCANVAS_UPDATER_TOKEN=" + TOKEN + "\n")
                env = {key: value for key, value in os.environ.items() if not key.startswith(("CANVAS_", "REPOSITORY", "IMAGE_REPOSITORY", "INSTALL_DIR"))}
                env.update(INSTALL_DIR=path.as_posix())
                if requested:
                    env["CANVAS_IMAGE_TAG"] = "v1.6.0"
                commands = '''
require_root() { :; }
hostname() { printf '127.0.0.1\\n'; }
install_packages() { :; }
install_docker() { :; }
download_compose() { touch "$COMPOSE_FILE"; }
install_host_updater() { (
''' + updater + '''
UPDATER_ENV="$INSTALL_DIR/updater.env"
for service in BACKEND WEB AGENT; do
    grep -Eq "^CANVAS_${service}_IMAGE=.+@sha256:" "$INSTALL_DIR/.env" || exit 21
done
ensure_token
); }
docker() {
    case "$1 $2" in
        "image inspect") printf '%s@sha256:%064d\\n' "${3%:*}" 0 ;;
        "compose --env-file")
            case "$*" in
                *" up -d "*)
                    grep -Eq '^CANVAS_AGENT_INTERNAL_TOKEN=.{32,}$' .env || exit 22
                    grep -Eq '^CANVAS_AGENT_WORKER_TOKEN=.{32,}$' .env || exit 25
                    grep -Eq '^CANVAS_AGENT_WORKER_ID=.+$' .env || exit 26
                    grep -Eq '^CANVAS_UPDATER_TOKEN=.{32,}$' .env || exit 23
                    test -f updater.env || exit 24 ;;
            esac ;;
        *) return 1 ;;
    esac
}
main
cp .env saved.env
main
'''
                result = subprocess.run([BASH, "--noprofile", "--norc", "-s"], input=source + commands,
                                        cwd=directory, env=env, capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, 0, result.stderr)
                values = dict(line.split("=", 1) for line in (path / ".env").read_text().splitlines())
                saved = dict(line.split("=", 1) for line in (path / "saved.env").read_text().splitlines())
                self.assertEqual(values["CANVAS_IMAGE_TAG"], "1.6.0" if requested else "1.5.0")
                for key in ("CANVAS_AGENT_INTERNAL_TOKEN", "CANVAS_AGENT_WORKER_TOKEN", "CANVAS_AGENT_WORKER_ID", "CANVAS_UPDATER_TOKEN"):
                    self.assertEqual(values[key], saved[key])
                    if existing:
                        if key in ("CANVAS_AGENT_INTERNAL_TOKEN", "CANVAS_UPDATER_TOKEN"):
                            self.assertEqual(values[key], TOKEN)

    def test_source_status_command_is_copyable_with_local_images(self):
        source = (ROOT / "scripts/install-server.sh").read_text(encoding="utf-8").rsplit('\nmain "$@"', 1)[0]
        with tempfile.TemporaryDirectory(prefix="canvas status ") as directory:
            env = {key: value for key, value in os.environ.items() if not key.startswith("CANVAS_")}
            env["INSTALL_DIR"] = Path(directory).as_posix()
            commands = '''
docker() {
    test "${CANVAS_BACKEND_IMAGE:-}" = open-ai-canvas-backend:server &&
    test "${CANVAS_WEB_IMAGE:-}" = open-ai-canvas-web:server &&
    test "${CANVAS_AGENT_IMAGE:-}" = open-ai-canvas-agent:server
}
start_services
status="$(print_result | sed -n 's/^查看状态：//p')"
unset CANVAS_BACKEND_IMAGE CANVAS_WEB_IMAGE CANVAS_AGENT_IMAGE
eval "$status"
'''
            result = subprocess.run([BASH, "--noprofile", "--norc", "-s"], input=source + commands,
                                    cwd=directory, env=env, capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stderr)

    def compose(self, files, token=TOKEN):
        env = {key: value for key, value in os.environ.items() if not key.startswith("CANVAS_")}
        env.update(POSTGRES_PASSWORD="test-password", DATABASE_URL="postgresql://test:test@postgres/test",
                   CANVAS_BACKEND_IMAGE="example.invalid/backend:test", CANVAS_WEB_IMAGE="example.invalid/web:test",
                   CANVAS_AGENT_IMAGE="example.invalid/agent:test", CANVAS_AGENT_INTERNAL_TOKEN=token,
                   CANVAS_AGENT_WORKER_ID="compose-worker", CANVAS_AGENT_WORKER_TOKEN=TOKEN)
        command = ["docker", "compose", "--env-file", ".env.example"]
        for file in files:
            command += ["-f", file]
        return subprocess.run(command + ["config", "--format", "json"], cwd=ROOT, env=env,
                              capture_output=True, text=True, encoding="utf-8", timeout=30)

    def test_production_entries_start_agent_with_distinct_worker_token(self):
        for files in (["docker-compose.deploy.yml"], ["docker-compose.server.yml"],
                      ["docker-compose.deploy.yml", "docker-compose.build.yml"]):
            with self.subTest(files=files):
                result = self.compose(files)
                self.assertEqual(result.returncode, 0, result.stderr)
                services = json.loads(result.stdout)["services"]
                self.assertIn("agent", list(services))
                agent = services["agent"]
                self.assertFalse(agent.get("profiles"))
                self.assertFalse(agent.get("ports"))
                self.assertNotIn("CANVAS_AGENT_INTERNAL_TOKEN", agent["environment"])
                self.assertEqual(agent["environment"]["CANVAS_AGENT_WORKER_TOKEN"], TOKEN)
                self.assertEqual(agent["environment"]["CANVAS_AGENT_WORKER_ID"], "compose-worker")
                self.assertEqual(services["backend"]["environment"]["CANVAS_AGENT_INTERNAL_TOKEN"], TOKEN)
                self.assertEqual(services["backend"]["environment"]["CANVAS_AGENT_WORKER_TOKEN"], TOKEN)
                self.assertEqual(agent["depends_on"]["backend"]["condition"], "service_healthy")

    def test_production_rejects_missing_internal_token(self):
        for file in ("docker-compose.deploy.yml", "docker-compose.server.yml"):
            with self.subTest(file=file):
                result = self.compose([file], token="")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("CANVAS_AGENT_INTERNAL_TOKEN", result.stderr)

    def test_installers_generate_and_preserve_agent_token(self):
        for script_name in ("install-server.sh", "install-server-image.sh"):
            source = (ROOT / "scripts" / script_name).read_text(encoding="utf-8")
            # Load the actual functions without running installation or contacting Docker.
            source = source.rsplit('\nmain "$@"', 1)[0]
            for configured in ("new", None, "", TOKEN, "too-short"):
                with self.subTest(script=script_name, configured=bool(configured)):
                    with tempfile.TemporaryDirectory(prefix="canvas-deployment-") as directory:
                        path = Path(directory) / ".env"
                        initial = "POSTGRES_PASSWORD=test\nDATABASE_URL=postgresql://test:test@postgres/test\nCANVAS_IMAGE_TAG=1.6.0\n"
                        if configured not in (None, "new"):
                            initial += "CANVAS_AGENT_INTERNAL_TOKEN=" + configured + "\n"
                        if configured != "new":
                            path.write_text(initial, encoding="utf-8")
                        env = {key: value for key, value in os.environ.items()
                               if not key.startswith(("CANVAS_", "REPOSITORY", "IMAGE_REPOSITORY", "INSTALL_DIR"))}
                        env["INSTALL_DIR"] = Path(directory).as_posix()
                        env["CANVAS_IMAGE_TAG"] = "1.6.0"
                        result = subprocess.run([BASH, "--noprofile", "--norc", "-s"],
                                                input=source + "\nprepare_environment\ncp .env saved.env\nprepare_environment\n",
                                                cwd=directory, env=env, capture_output=True, text=True, timeout=30)
                        if configured == "too-short":
                            self.assertNotEqual(result.returncode, 0)
                            continue
                        self.assertEqual(result.returncode, 0, result.stderr)
                        tokens = [line.split("=", 1)[1] for line in path.read_text().splitlines()
                                  if line.startswith("CANVAS_AGENT_INTERNAL_TOKEN=")]
                        self.assertEqual(len(tokens), 1)
                        saved_tokens = [line for line in (Path(directory) / "saved.env").read_text().splitlines()
                                        if line.startswith("CANVAS_AGENT_INTERNAL_TOKEN=")]
                        self.assertEqual(saved_tokens, ["CANVAS_AGENT_INTERNAL_TOKEN=" + tokens[0]])
                        worker_tokens = [line.split("=", 1)[1] for line in path.read_text().splitlines()
                                         if line.startswith("CANVAS_AGENT_WORKER_TOKEN=")]
                        saved_worker_tokens = [line for line in (Path(directory) / "saved.env").read_text().splitlines()
                                               if line.startswith("CANVAS_AGENT_WORKER_TOKEN=")]
                        self.assertEqual(len(worker_tokens), 1)
                        self.assertEqual(saved_worker_tokens, ["CANVAS_AGENT_WORKER_TOKEN=" + worker_tokens[0]])
                        self.assertNotEqual(worker_tokens[0], tokens[0])
                        if configured == TOKEN:
                            self.assertEqual(tokens[0], configured)
                        else:
                            self.assertRegex(tokens[0], r"^[a-f0-9]{64}$")

    def test_source_install_resolves_local_images_before_build(self):
        source = (ROOT / "scripts/install-server.sh").read_text(encoding="utf-8").rsplit('\nmain "$@"', 1)[0]
        with tempfile.TemporaryDirectory(prefix="canvas-build-config-") as directory:
            path = Path(directory)
            for file in ("docker-compose.deploy.yml", "docker-compose.build.yml"):
                shutil.copyfile(ROOT / file, path / file)
            (path / ".env").write_text("POSTGRES_PASSWORD=test\nDATABASE_URL=postgresql://test:test@postgres/test\nCANVAS_AGENT_INTERNAL_TOKEN=" + TOKEN + "\nCANVAS_AGENT_WORKER_ID=compose-worker\nCANVAS_AGENT_WORKER_TOKEN=" + TOKEN + "\n")
            env = {key: value for key, value in os.environ.items() if not key.startswith("CANVAS_")}
            # Replace only container mutations. The real Compose parser checks interpolation.
            commands = '''
docker() {
    case "$*" in
        *" build backend web agent") command docker compose --env-file .env -f "$COMPOSE_FILE" -f "$BUILD_COMPOSE_FILE" config --format json > config.json ;;
        *" up -d "*) return 0 ;;
        *) return 1 ;;
    esac
}
start_services
'''
            result = subprocess.run([BASH, "--noprofile", "--norc", "-s"], input=source + commands,
                                    cwd=directory, env=env, capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stderr)
            services = json.loads((path / "config.json").read_text())["services"]
            self.assertEqual(services["agent"]["image"], "open-ai-canvas-agent:server")
            self.assertEqual(services["agent"]["build"]["dockerfile"], "agent/Dockerfile")

    def test_image_install_pins_all_three_images_on_first_install(self):
        source = (ROOT / "scripts/install-server-image.sh").read_text(encoding="utf-8").rsplit('\nmain "$@"', 1)[0]
        with tempfile.TemporaryDirectory(prefix="canvas-image-install-") as directory:
            env = {key: value for key, value in os.environ.items()
                   if not key.startswith(("CANVAS_", "REPOSITORY", "IMAGE_REPOSITORY", "INSTALL_DIR"))}
            env.update(INSTALL_DIR=Path(directory).as_posix(), CANVAS_IMAGE_TAG="v1.6.0")
            commands = '''
docker() {
    case "$1 $2" in
        "compose --env-file") return 0 ;;
        "image inspect") printf '%s@sha256:%064d\\n' "${3%:*}" 0 ;;
        *) return 1 ;;
    esac
}
prepare_environment
pull_and_pin_images
start_services
'''
            result = subprocess.run([BASH, "--noprofile", "--norc", "-s"], input=source + commands,
                                    cwd=directory, env=env, capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stderr)
            values = dict(line.split("=", 1) for line in (Path(directory) / ".env").read_text().splitlines() if "=" in line)
            for service in ("BACKEND", "WEB", "AGENT"):
                self.assertEqual(values["CANVAS_" + service + "_IMAGE"],
                                 "ghcr.io/aetherno2332/open-ai-canvas-" + service.lower() + "@sha256:" + "0" * 64)


if __name__ == "__main__":
    unittest.main()
