"""Exercise installer worker ID validation without starting Docker or the installer."""

from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent
INSTALLERS = ("install-server.sh", "install-server-image.sh")


def shell() -> str:
    git_bash = Path("C:/Program Files/Git/bin/bash.exe")
    return str(git_bash) if git_bash.exists() else shutil.which("bash") or "bash"


def isolated_token_function(name: str) -> str:
    source = (ROOT / name).read_text(encoding="utf-8")
    function = source.split("ensure_agent_token() {\n", 1)[1].split("\n}\n", 1)[0]
    return "ensure_agent_token() {\n" + function + "\n}\n"


class InstallerWorkerIDTests(unittest.TestCase):
    def test_64_characters_are_kept_and_65_are_rejected_before_env_rewrite(self):
        for installer in INSTALLERS:
            with self.subTest(installer=installer), tempfile.TemporaryDirectory() as directory:
                env_file = Path(directory) / ".env"
                script = (
                    "set -Eeuo pipefail\n"
                    "fail() { printf '%s\\n' \"$1\" >&2; exit 1; }\n"
                    + isolated_token_function(installer)
                    + "ensure_agent_token\n"
                )
                for length, expected in ((64, 0), (65, 1)):
                    worker_id = "w" * length
                    original = (
                        "CANVAS_AGENT_INTERNAL_TOKEN=" + "s" * 32 + "\n"
                        "CANVAS_AGENT_WORKER_TOKEN=" + "t" * 32 + "\n"
                        "CANVAS_AGENT_WORKER_ID=" + worker_id + "\n"
                    )
                    env_file.write_text(original, encoding="utf-8")
                    result = subprocess.run(
                        [shell(), "-c", script], cwd=directory, capture_output=True, text=True,
                        encoding="utf-8",
                    )
                    self.assertEqual(result.returncode, expected, result.stderr)
                    self.assertEqual(env_file.read_text(encoding="utf-8"), original)
                    if expected:
                        self.assertIn("CANVAS_AGENT_WORKER_ID", result.stderr)
                        self.assertIn(".env", result.stderr)


if __name__ == "__main__":
    unittest.main()
