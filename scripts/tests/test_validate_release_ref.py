import pathlib
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "validate-release-ref.sh"


class ValidateReleaseRefTest(unittest.TestCase):
    def test_accepts_full_commit_and_tag_but_rejects_branch(self):
        with tempfile.TemporaryDirectory() as directory:
            repository = pathlib.Path(directory)
            subprocess.run(["git", "init", "-q"], cwd=repository, check=True)
            subprocess.run(
                ["git", "config", "user.name", "Test"],
                cwd=repository,
                check=True,
            )
            subprocess.run(
                ["git", "config", "user.email", "test@example.com"],
                cwd=repository,
                check=True,
            )
            (repository / "README").write_text("release\n", encoding="utf-8")
            subprocess.run(["git", "add", "README"], cwd=repository, check=True)
            subprocess.run(
                [
                    "git",
                    "-c",
                    "core.hooksPath=/dev/null",
                    "commit",
                    "-q",
                    "-m",
                    "release",
                ],
                cwd=repository,
                check=True,
            )
            commit = subprocess.check_output(
                ["git", "rev-parse", "HEAD"],
                cwd=repository,
                text=True,
            ).strip()
            subprocess.run(["git", "tag", "v1.0.0"], cwd=repository, check=True)
            branch = subprocess.check_output(
                ["git", "branch", "--show-current"],
                cwd=repository,
                text=True,
            ).strip()

            for reference in (commit, "v1.0.0"):
                result = subprocess.run(
                    [str(SCRIPT), reference],
                    cwd=repository,
                    check=True,
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(result.stdout.strip(), commit)

            rejected = subprocess.run(
                [str(SCRIPT), branch],
                cwd=repository,
                capture_output=True,
                text=True,
            )
            self.assertEqual(rejected.returncode, 2)
            self.assertIn("full commit SHA or local release tag", rejected.stderr)


if __name__ == "__main__":
    unittest.main()
