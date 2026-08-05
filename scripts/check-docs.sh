#!/usr/bin/env bash
# Validate maintained Markdown links and bilingual document parity.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

python3 - "$ROOT" <<'PY'
from __future__ import annotations

import pathlib
import re
import sys
import urllib.parse

root = pathlib.Path(sys.argv[1]).resolve()
errors: list[str] = []

markdown_files = sorted(
    path for path in root.rglob("*.md")
    if not any(part in {".git", "node_modules", "dist", ".vscode-test"} for part in path.parts)
)
link_pattern = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")

for source in markdown_files:
    text = source.read_text(encoding="utf-8")
    for raw_target in link_pattern.findall(text):
        target = raw_target.strip()
        if not target or target.startswith(("#", "http://", "https://", "mailto:")):
            continue
        target = target.split(maxsplit=1)[0].strip("<>")
        path_text = urllib.parse.unquote(target.split("#", 1)[0])
        if not path_text:
            continue
        resolved = (source.parent / path_text).resolve()
        try:
            resolved.relative_to(root)
        except ValueError:
            errors.append(f"{source.relative_to(root)}: link escapes repository: {raw_target}")
            continue
        if not resolved.exists():
            errors.append(f"{source.relative_to(root)}: missing link target: {raw_target}")

english = root / "docs" / "en"
chinese = root / "docs" / "zh-CN"
for source_dir, mirror_dir, label in (
    (english, chinese, "English"),
    (chinese, english, "Chinese"),
):
    for source in sorted(source_dir.glob("*.md")):
        mirror = mirror_dir / source.name
        if not mirror.is_file():
            errors.append(f"{label} document has no mirror: {source.relative_to(root)}")

required_pairs = (
    ("README.md", "README.zh-CN.md"),
    ("CONTRIBUTING.md", "CONTRIBUTING.zh-CN.md"),
    ("scripts/README.md", "scripts/README.zh-CN.md"),
)
for first, second in required_pairs:
    if not (root / first).is_file() or not (root / second).is_file():
        errors.append(f"missing bilingual pair: {first} <-> {second}")

removed_patterns = (
    "docs/rfc/",
    "docs/ARCHITECTURE.zh-CN.md",
    "docs/USAGE.zh-CN.md",
    "docs/ROADMAP.zh-CN.md",
)
for source in markdown_files:
    text = source.read_text(encoding="utf-8")
    for pattern in removed_patterns:
        if pattern in text:
            errors.append(f"{source.relative_to(root)}: references removed document: {pattern}")

if errors:
    print("documentation check failed:", file=sys.stderr)
    for error in errors:
        print(f"  - {error}", file=sys.stderr)
    raise SystemExit(1)

print(f"documentation check passed: {len(markdown_files)} Markdown files")
PY
