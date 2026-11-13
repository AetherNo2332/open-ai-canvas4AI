"""Documentation deployment contracts; real HTTP smoke is a separate command."""
import pathlib
import unittest
import os
import subprocess
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[1]

class DocsDeploymentTests(unittest.TestCase):
    def test_repeat_install_preserves_selected_docs_without_channel_lookup(self):
        source = (ROOT / "scripts/install-server-image.sh").read_text(encoding="utf-8").rsplit('\nmain "$@"', 1)[0]
        image = "ghcr.io/example/open-ai-canvas-docs@sha256:" + "a" * 64
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory)
            (path / ".env").write_text("CANVAS_DOCS_IMAGE=" + image + "\n")
            (path / "docker-compose.deploy.yml").write_text("services:\n  docs:\n    image: selected\n")
            bash = r"C:\Program Files\Git\bin\bash.exe" if os.name == "nt" else "bash"
            env = {**os.environ, "REPOSITORY": "example/project", "IMAGE_REPOSITORY": "ghcr.io/example/open-ai-canvas"}
            result = subprocess.run([bash, "--noprofile", "--norc", "-s"], input=source + '\ncurl() { return 91; }\nprepare_docs_image\n',
                                    cwd=path, env=env, capture_output=True, text=True, encoding="utf-8", timeout=20)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((path / ".env").read_text().strip(), "CANVAS_DOCS_IMAGE=" + image)

    def test_independent_context_and_runtime(self):
        source = (ROOT / "docs/Dockerfile").read_text()
        for contract in ("--frozen-lockfile", ".next/standalone", ".next/static", "USER node", "3001"):
            self.assertIn(contract, source)
        self.assertNotIn("COPY ../", source)

    def test_subpath_and_search(self):
        config = (ROOT / "docs/next.config.mjs").read_text()
        self.assertIn("basePath: '/docs'", config)
        self.assertIn("output: 'standalone'", config)
        self.assertIn("/docs/api/search", (ROOT / "docs/components/providers.tsx").read_text())
        self.assertFalse(list((ROOT / "docs/app/docs").rglob("*.tsx")))

    def test_gateway_does_not_require_docs_at_startup(self):
        source = (ROOT / "nginx.conf").read_text(encoding="utf-8")
        self.assertIn("location = /docs", source)
        self.assertIn("location ^~ /docs/", source)
        self.assertIn("resolver 127.0.0.11", source)
        self.assertIn("proxy_set_header Cookie", source)

if __name__ == "__main__":
    unittest.main()
