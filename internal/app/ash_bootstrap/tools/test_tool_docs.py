# Copyright (c) 2026 Jonathan L. Conradt
# SPDX-License-Identifier: MIT

import contextlib
import importlib.util
import io
import os
import subprocess
import sys
import unittest
from unittest.mock import patch

ROOT = os.path.dirname(os.path.abspath(__file__))
SPEC = importlib.util.spec_from_file_location("wikipedia", os.path.join(ROOT, "wikipedia.py"))
wikipedia = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(wikipedia)


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


class WikipediaArgumentTests(unittest.TestCase):
    def run_main(self, argv):
        output = io.StringIO()
        with contextlib.ExitStack() as stack:
            stack.enter_context(patch.object(sys, "argv", ["wikipedia.py", *argv]))
            search = stack.enter_context(patch.object(wikipedia, "wikipedia_search_tool"))
            stack.enter_context(contextlib.redirect_stdout(output))
            search.return_value = "{}"
            wikipedia.main()
        return search, output.getvalue()

    def test_positional_query(self):
        search, _ = self.run_main(["Nebraska"])
        search.assert_called_once_with("Nebraska", "MyAIAgent/1.0 (contact@example.com)")

    def test_multiword_positional_query(self):
        search, _ = self.run_main(["New", "York"])
        search.assert_called_once_with("New York", "MyAIAgent/1.0 (contact@example.com)")

    def test_query_option_takes_precedence(self):
        search, _ = self.run_main(["positional", "--query", "explicit"])
        search.assert_called_once_with("explicit", "MyAIAgent/1.0 (contact@example.com)")

    def test_query_option_takes_precedence_when_first(self):
        search, _ = self.run_main(["--query", "explicit", "positional"])
        search.assert_called_once_with("explicit", "MyAIAgent/1.0 (contact@example.com)")

    def test_default_query_is_preserved(self):
        search, _ = self.run_main([])
        search.assert_called_once_with("quantum computing", "MyAIAgent/1.0 (contact@example.com)")

    def test_unknown_option_is_rejected(self):
        with self.assertRaises(SystemExit) as raised:
            self.run_main(["--unknown"])
        self.assertEqual(raised.exception.code, 2)


if __name__ == "__main__":
    unittest.main()
