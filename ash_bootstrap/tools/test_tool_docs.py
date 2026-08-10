import os
import subprocess
import sys
import unittest


ROOT = os.path.dirname(os.path.abspath(__file__))


class ToolDocumentationTests(unittest.TestCase):
    def run_tool(self, script_name):
        script_path = os.path.join(ROOT, script_name)
        return subprocess.run(
            [sys.executable, script_path, "--ai-docs"],
            capture_output=True,
            text=True,
            cwd=ROOT,
        )

    def test_headlines_docs(self):
        result = self.run_tool("headlines.py")
        self.assertEqual(result.returncode, 0, msg=result.stderr)
        self.assertIn("Capabilities", result.stdout)
        self.assertIn("--query", result.stdout)
        self.assertIn("Return format", result.stdout)

    def test_wikipedia_docs(self):
        result = self.run_tool("wikipedia.py")
        self.assertEqual(result.returncode, 0, msg=result.stderr)
        self.assertIn("Capabilities", result.stdout)
        self.assertIn("--query", result.stdout)
        self.assertIn("Return format", result.stdout)

    def test_yfinance_docs(self):
        result = self.run_tool("yfinance.py")
        self.assertEqual(result.returncode, 0, msg=result.stderr)
        self.assertIn("Capabilities", result.stdout)
        self.assertIn("TICKER", result.stdout)
        self.assertIn("Return format", result.stdout)


if __name__ == "__main__":
    unittest.main()
