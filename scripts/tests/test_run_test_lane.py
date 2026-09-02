import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "run-test-lane.py"


class RunTestLaneTest(unittest.TestCase):
    def run_lane(self, *arguments: str):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        report = Path(directory.name) / "lane.json"
        completed = subprocess.run(
            [
                sys.executable,
                str(SCRIPT),
                "fixture",
                "--report",
                str(report),
                *arguments,
            ],
            check=False,
            capture_output=True,
            text=True,
        )
        return completed, json.loads(report.read_text())

    def test_records_a_passed_command(self):
        completed, report = self.run_lane(
            "--", sys.executable, "-c", "raise SystemExit(0)"
        )

        self.assertEqual(completed.returncode, 0)
        self.assertEqual(report["status"], "passed")
        self.assertEqual(report["exit_code"], 0)

    def test_preserves_a_command_failure(self):
        completed, report = self.run_lane(
            "--", sys.executable, "-c", "raise SystemExit(7)"
        )

        self.assertEqual(completed.returncode, 7)
        self.assertEqual(report["status"], "failed")
        self.assertEqual(report["exit_code"], 7)

    def test_records_an_optional_missing_capability(self):
        completed, report = self.run_lane(
            "--requires-command",
            "qcode-command-that-does-not-exist",
            "--",
            sys.executable,
            "-c",
            "raise SystemExit(0)",
        )

        self.assertEqual(completed.returncode, 0)
        self.assertEqual(report["status"], "unavailable")
        self.assertIsNone(report["exit_code"])
        self.assertIn("is not installed", report["unavailable_reasons"][0])

    def test_can_require_a_capability(self):
        completed, report = self.run_lane(
            "--requires-command",
            "qcode-command-that-does-not-exist",
            "--require-available",
            "--",
            sys.executable,
            "-c",
            "raise SystemExit(0)",
        )

        self.assertEqual(completed.returncode, 1)
        self.assertEqual(report["status"], "unavailable")

    def test_classifies_a_stable_command_marker_as_unavailable(self):
        completed, report = self.run_lane(
            "--unavailable-pattern",
            "sandbox_unavailable",
            "--",
            sys.executable,
            "-c",
            "print('sandbox_unavailable: fixture'); raise SystemExit(2)",
        )

        self.assertEqual(completed.returncode, 0)
        self.assertEqual(report["status"], "unavailable")
        self.assertEqual(report["exit_code"], 2)
        self.assertIn("unavailable marker", report["unavailable_reasons"][0])


if __name__ == "__main__":
    unittest.main()
