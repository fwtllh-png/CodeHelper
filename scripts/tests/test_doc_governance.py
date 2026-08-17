#!/usr/bin/env python3

from __future__ import annotations

import contextlib
import datetime as dt
import importlib.util
import io
import pathlib
import tempfile
import unittest
from unittest import mock


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

    def test_chapter_update_requires_both_languages(self) -> None:
        english = "docs/book/en/03-runtime-kernel/01-protocol.md"
        chinese = "docs/book/zh-CN/03-runtime-kernel/01-protocol.md"
        self.assertEqual(governance.documentation_ids([english]), set())
        self.assertEqual(
            governance.documentation_ids([english, chinese]),
            {"runtime-protocol"},
        )

    def test_set_front_matter_fields_rewrites_scalars(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = pathlib.Path(tmp) / "chapter.md"
            path.write_text(
                "---\nid: runtime-protocol\nstatus: verified\n"
                "last_verified: 2026-01-01\ntitle: Protocol\n---\nBody\n",
                encoding="utf-8",
            )
            self.assertTrue(
                governance.set_front_matter_fields(
                    path, {"status": "draft", "last_verified": None}
                )
            )
            self.assertEqual(
                path.read_text(encoding="utf-8"),
                "---\nid: runtime-protocol\nstatus: draft\n"
                "last_verified: null\ntitle: Protocol\n---\nBody\n",
            )
            self.assertFalse(
                governance.set_front_matter_fields(
                    path, {"status": "draft", "last_verified": None}
                )
            )

    def test_set_catalog_status_rewrites_one_chapter(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = pathlib.Path(tmp) / "catalog.json"
            original = (
                '{"chapters": [{"id": "c1", "slug": "01-c1", "status": "verified"}, '
                '{"id": "c2", "slug": "02-c2", "status": "verified"}]}\n'
            )
            path.write_text(original, encoding="utf-8")
            self.assertTrue(governance.set_catalog_status("c1", "draft", path))
            self.assertEqual(
                path.read_text(encoding="utf-8"),
                '{"chapters": [{"id": "c1", "slug": "01-c1", "status": "draft"}, '
                '{"id": "c2", "slug": "02-c2", "status": "verified"}]}\n',
            )
            with self.assertRaises(ValueError):
                governance.set_catalog_status("missing", "draft", path)

    def _run_reverify_patches(self, book: pathlib.Path, catalog: pathlib.Path):
        drift = {
            "c1": ["internal/runtime/protocol/types.go"],
            "c2": [],
        }
        stack = contextlib.ExitStack()
        stack.enter_context(mock.patch.object(governance, "BOOK", book))
        original_set_catalog_status = governance.set_catalog_status
        stack.enter_context(
            mock.patch.object(
                governance,
                "set_catalog_status",
                side_effect=lambda cid, status, catalog=catalog: (
                    original_set_catalog_status(cid, status, catalog)
                ),
            )
        )
        stack.enter_context(
            mock.patch.object(
                governance,
                "source_changes_after",
                side_effect=lambda chapter, verified: drift[
                    chapter["metadata"]["id"]
                ],
            )
        )
        return stack

    def test_run_reverify_dry_run_reports_counts(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            book = pathlib.Path(tmp) / "book"
            catalog = book / "catalog.json"
            for language in ("en", "zh-CN"):
                part = book / language / "01-runtime-kernel"
                part.mkdir(parents=True)
                for name in ("01-c1.md", "02-c2.md"):
                    part.joinpath(name).write_text(
                        "---\nid: "
                        + name[3:5]
                        + "\nstatus: verified\nlast_verified: 2020-01-01\n---\n",
                        encoding="utf-8",
                    )
            catalog.write_text(
                '{"chapters": [{"id": "c1", "slug": "01-c1", "status": "verified"}, '
                '{"id": "c2", "slug": "02-c2", "status": "verified"}]}\n',
                encoding="utf-8",
            )
            with self._run_reverify_patches(book, catalog):
                with mock.patch("sys.stdout", new_callable=io.StringIO) as out:
                    self.assertEqual(governance.run_reverify(dry_run=True), 0)
                self.assertIn(
                    "1 re-stamped, 1 downgraded to draft, 0 skipped",
                    out.getvalue(),
                )
            self.assertEqual(
                (book / "en" / "01-runtime-kernel" / "01-c1.md").read_text(
                    encoding="utf-8"
                ),
                "---\nid: c1\nstatus: verified\nlast_verified: 2020-01-01\n---\n",
            )

    def test_run_reverify_applies_downgrade_and_restamp(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            book = pathlib.Path(tmp) / "book"
            catalog = book / "catalog.json"
            for language in ("en", "zh-CN"):
                part = book / language / "01-runtime-kernel"
                part.mkdir(parents=True)
                for name in ("01-c1.md", "02-c2.md"):
                    part.joinpath(name).write_text(
                        "---\nid: "
                        + name[3:5]
                        + "\nstatus: verified\nlast_verified: 2020-01-01\n---\n",
                        encoding="utf-8",
                    )
            catalog.write_text(
                '{"chapters": [{"id": "c1", "slug": "01-c1", "status": "verified"}, '
                '{"id": "c2", "slug": "02-c2", "status": "verified"}]}\n',
                encoding="utf-8",
            )
            with self._run_reverify_patches(book, catalog):
                with mock.patch("sys.stdout", new_callable=io.StringIO):
                    self.assertEqual(governance.run_reverify(dry_run=False), 0)
            en_c1 = book / "en" / "01-runtime-kernel" / "01-c1.md"
            zh_c1 = book / "zh-CN" / "01-runtime-kernel" / "01-c1.md"
            en_c2 = book / "en" / "01-runtime-kernel" / "02-c2.md"
            self.assertEqual(
                governance.parse_front_matter(en_c1)["status"], "draft"
            )
            self.assertIsNone(governance.parse_front_matter(en_c1)["last_verified"])
            self.assertEqual(
                governance.parse_front_matter(zh_c1)["status"], "draft"
            )
            self.assertEqual(
                governance.parse_front_matter(en_c2)["status"], "verified"
            )
            self.assertEqual(
                governance.parse_front_matter(en_c2)["last_verified"],
                dt.date.today().isoformat(),
            )
            chapters = governance.load_json(catalog)["chapters"]
            self.assertEqual(chapters[0]["status"], "draft")
            self.assertEqual(chapters[1]["status"], "verified")


if __name__ == "__main__":
    unittest.main()
