#!/usr/bin/env python3
"""Render Chinese knowledge-book navigation from docs/book/catalog.json."""

from __future__ import annotations

import argparse
import json
import pathlib
import sys


ROOT = pathlib.Path(__file__).resolve().parents[1]
BOOK = ROOT / "docs" / "book"
CATALOG = BOOK / "catalog.json"

LANGUAGE = {
    "zh-CN": {
        "title": "CodeHelper Agent 工程知识书籍：导航",
        "intro": (
            "本导航由 `docs/book/catalog.json` 生成。规划章节可以清晰可见，"
            "但不会通过空占位文件伪装成已完成内容。"
        ),
        "legend": "## 状态",
        "legend_lines": [
            "- `planned`：只有 Catalog 条目，正文尚未开始。",
            "- `draft`：中文正文已存在，但尚未完成全部验证。",
            "- `verified`：中文内容、源码引用和验证命令均通过章节门禁。",
        ],
        "priority": "## 核心阅读路径",
        "priority_intro": "建议先阅读以下六章：",
        "contents": "## 完整规划目录",
        "part": "部分",
        "path": "规划路径",
        "footer": (
            "不要直接编辑此文件。修改 `docs/book/catalog.json` 后运行 "
            "`python3 scripts/render-book-navigation.py`。"
        ),
    },
}


def load_catalog() -> dict:
    return json.loads(CATALOG.read_text(encoding="utf-8"))


def chapter_path(part: dict, chapter: dict) -> pathlib.PurePosixPath:
    return pathlib.PurePosixPath(part["id"]) / f"{chapter['slug']}.md"


def chapter_line(language: str, part: dict, chapter: dict) -> str:
    relative = chapter_path(part, chapter)
    target = BOOK / language / relative
    title = chapter["titles"][language]
    if target.is_file():
        rendered_title = f"[{title}](./{relative.as_posix()})"
    else:
        rendered_title = title
    return (
        f"- {rendered_title} — `{chapter['id']}` — `{chapter['status']}` "
        f"— {LANGUAGE[language]['path']}: `{relative.as_posix()}`"
    )


def render(language: str, catalog: dict) -> str:
    text = LANGUAGE[language]
    lines = [
        f"# {text['title']}",
        "",
        text["intro"],
        "",
        text["legend"],
        "",
        *text["legend_lines"],
        "",
        text["priority"],
        "",
        text["priority_intro"],
        "",
    ]
    for part in catalog["parts"]:
        for chapter in part["chapters"]:
            if chapter.get("core_reading") is True:
                lines.append(chapter_line(language, part, chapter))
    lines.extend(["", text["contents"], ""])
    for part in catalog["parts"]:
        lines.extend(
            [
                f"### {text['part']} {part['order']}: {part['titles'][language]}",
                "",
            ]
        )
        lines.extend(chapter_line(language, part, chapter) for chapter in part["chapters"])
        lines.append("")
    lines.extend(["---", "", text["footer"], ""])
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--check",
        action="store_true",
        help="fail when committed navigation differs from the catalog",
    )
    args = parser.parse_args()
    catalog = load_catalog()
    stale: list[pathlib.Path] = []
    for language in catalog["languages"]:
        target = BOOK / language / "NAVIGATION.md"
        expected = render(language, catalog)
        if args.check:
            if not target.is_file() or target.read_text(encoding="utf-8") != expected:
                stale.append(target)
        else:
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(expected, encoding="utf-8")
    if stale:
        for target in stale:
            print(
                f"stale generated navigation: {target.relative_to(ROOT)}",
                file=sys.stderr,
            )
        print(
            "run: python3 scripts/render-book-navigation.py",
            file=sys.stderr,
        )
        return 1
    if not args.check:
        print("rendered Chinese book navigation")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
