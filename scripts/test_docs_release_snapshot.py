import os
import pathlib
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
BASH = r"C:\Program Files\Git\bin\bash.exe" if os.name == "nt" else "bash"

class DocsSnapshotTests(unittest.TestCase):
    def test_existing_release_reuses_snapshot_without_reading_channel(self):
        with tempfile.TemporaryDirectory() as directory:
            fixture = pathlib.Path(directory)
            commit = "a" * 40
            import json
            snapshot = dict(schemaVersion=1, digest="ghcr.io/example/open-ai-canvas-docs@sha256:" + "a" * 64,
                            buildId="docs-" + commit, commit=commit, builtAt="2026-11-13T00:00:00Z", deploymentContract=1)
            (fixture / "published.json").write_text(json.dumps(snapshot), encoding="utf-8")
            script = r'''
gh() {
    case "$1 $2" in
        "release view") printf '%s\n' '["docs-image.json"]' ;;
        "release download") cp "$FIXTURE/published.json" "$SNAPSHOT_TEMP/docs-image.json" ;;
        "release upload") echo 'must not overwrite' >&2; return 99 ;;
        *) return 98 ;;
    esac
}
docker() { echo 'must not resolve docs-main' >&2; return 97; }
source "$ROOT_DIR/scripts/docs-release-snapshot.sh" prepare
source "$ROOT_DIR/scripts/docs-release-snapshot.sh" publish
'''
            env = {**os.environ, "FIXTURE": fixture.as_posix(), "ROOT_DIR": ROOT.as_posix(),
                   "GITHUB_REPOSITORY": "example/project", "GITHUB_REPOSITORY_OWNER": "example",
                   "GITHUB_REF_NAME": "v1.0.0", "DOCS_SNAPSHOT_OUTPUT": (fixture / "result.json").as_posix()}
            result = subprocess.run([BASH, "--noprofile", "--norc", "-s"], input=script, env=env,
                                    capture_output=True, text=True, encoding="utf-8", timeout=30)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads((fixture / "result.json").read_text()), snapshot)

if __name__ == "__main__":
    unittest.main()
