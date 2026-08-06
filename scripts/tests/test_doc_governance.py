#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import pathlib
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]
MODULE_PATH = ROOT / "scripts" / "check-doc-governance.py"
SPEC = importlib.util.spec_from_file_location("doc_governance", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
governance = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(governance)


class DocumentationGovernanceTest(unittest.TestCase):
    def test_matches_files_directories_and_globs(self) -> None:
        self.assertTrue(governance.matches("Makefile", "Makefile"))
        self.assertTrue(
            governance.matches(
                "internal/runtime/protocol/types.go", "internal/runtime/**"
            )
        )
        self.assertTrue(
            governance.matches(
                "internal/adapter/tool/guard/guard.go", "internal/adapter/tool"
            )
        )
        self.assertFalse(
            governance.matches("internal/host/cli/run.go", "internal/runtime/**")
        )

    def test_runtime_change_maps_to_runtime_chapters(self) -> None:
        impacted = governance.impacted_chapters(
            ["internal/runtime/protocol/types.go"]
        )
        self.assertIn("runtime-protocol", impacted)
        self.assertIn("overview-runtime-vocabulary", impacted)
        self.assertNotIn("host-vscode", impacted)

    def test_codeowners_is_generated_from_registry(self) -> None:
        expected = governance.expected_codeowners(
            governance.load_json(governance.CONFIG_PATH)
        )
        actual = governance.CODEOWNERS_PATH.read_text(encoding="utf-8")
        self.assertEqual(expected, actual)

    def test_chapter_update_requires_both_languages(self) -> None:
        english = "docs/book/en/03-runtime-kernel/01-protocol.md"
        chinese = "docs/book/zh-CN/03-runtime-kernel/01-protocol.md"
        self.assertEqual(governance.documentation_ids([english]), set())
        self.assertEqual(
            governance.documentation_ids([english, chinese]),
            {"runtime-protocol"},
        )


if __name__ == "__main__":
    unittest.main()
