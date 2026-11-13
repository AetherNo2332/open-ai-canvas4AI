"""Documentation deployment contracts; real HTTP smoke is a separate command."""
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]

class DocsDeploymentTests(unittest.TestCase):
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
