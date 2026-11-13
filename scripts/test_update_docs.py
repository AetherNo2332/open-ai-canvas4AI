import pathlib
import subprocess
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]

class UpdateDocsTests(unittest.TestCase):
    def test_invalid_action_fails_before_accessing_token(self):
        bash = r"C:\Program Files\Git\bin\bash.exe" if __import__("os").name == "nt" else "bash"
        result = subprocess.run([bash, str(ROOT / "scripts/update-docs.sh"), "invalid"], capture_output=True, text=True)
        self.assertEqual(result.returncode, 2)
        self.assertIn("Usage:", result.stderr)

    def test_token_is_not_in_curl_arguments(self):
        source = (ROOT / "scripts/update-docs.sh").read_text(encoding="utf-8")
        self.assertIn("curl --config -", source)
        self.assertNotIn('curl -H "Authorization', source)
        self.assertNotIn("docker ", source)

if __name__ == "__main__":
    unittest.main()
