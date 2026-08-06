#!/usr/bin/env bash
# Validate the bilingual Agent engineering book catalog and chapter contract.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

python3 scripts/render-book-navigation.py --check

python3 - "$ROOT" <<'PY'
from __future__ import annotations

import datetime
import glob
import json
import pathlib
import re
import sys


root = pathlib.Path(sys.argv[1]).resolve()
book = root / "docs" / "book"
catalog_path = book / "catalog.json"
schema_path = book / "schema" / "chapter.schema.json"
errors: list[str] = []


def error(message: str) -> None:
    errors.append(message)


def load_json(path: pathlib.Path) -> dict:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        error(f"{path.relative_to(root)}: invalid JSON: {exc}")
        return {}


catalog = load_json(catalog_path)
schema = load_json(schema_path)
languages = catalog.get("languages")
statuses = catalog.get("statuses")
parts = catalog.get("parts")

if catalog.get("schema_version") != 1:
    error("docs/book/catalog.json: schema_version must be 1")
if languages != ["en", "zh-CN"]:
    error("docs/book/catalog.json: languages must be exactly ['en', 'zh-CN']")
if statuses != ["planned", "draft", "verified"]:
    error("docs/book/catalog.json: unsupported status contract")
if not isinstance(parts, list) or not parts:
    error("docs/book/catalog.json: parts must be a non-empty array")
    parts = []

required_metadata = {
    "id",
    "title",
    "audience",
    "prerequisites",
    "code_paths",
    "test_paths",
    "source_of_truth",
    "status",
    "last_verified",
}
schema_required = set(schema.get("required", []))
if schema_required != required_metadata:
    error("docs/book/schema/chapter.schema.json: required fields drifted")

id_pattern = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
slug_pattern = re.compile(r"^[0-9]{2}-[a-z0-9]+(?:-[a-z0-9]+)*$")
allowed_audiences = {"learner", "user", "contributor", "operator", "agent"}
part_ids: set[str] = set()
part_orders: set[int] = set()
chapter_ids: set[str] = set()
catalog_chapters: dict[str, tuple[dict, dict]] = {}
catalog_paths: set[pathlib.PurePosixPath] = set()
core_chapters: list[str] = []

for part in parts:
    part_id = part.get("id")
    order = part.get("order")
    titles = part.get("titles")
    chapters = part.get("chapters")
    if not isinstance(part_id, str) or not id_pattern.fullmatch(part_id):
        error(f"catalog part has invalid id: {part_id!r}")
        continue
    if part_id in part_ids:
        error(f"catalog has duplicate part id: {part_id}")
    part_ids.add(part_id)
    if not isinstance(order, int) or order < 1 or order in part_orders:
        error(f"catalog part {part_id}: order must be a unique positive integer")
    else:
        part_orders.add(order)
    if not isinstance(titles, dict) or set(titles) != set(languages or []):
        error(f"catalog part {part_id}: titles must cover both languages")
    if not isinstance(chapters, list) or not chapters:
        error(f"catalog part {part_id}: chapters must be a non-empty array")
        continue
    slugs: set[str] = set()
    for chapter in chapters:
        chapter_id = chapter.get("id")
        slug = chapter.get("slug")
        status = chapter.get("status")
        chapter_titles = chapter.get("titles")
        if not isinstance(chapter_id, str) or not id_pattern.fullmatch(chapter_id):
            error(f"catalog part {part_id}: invalid chapter id {chapter_id!r}")
            continue
        if chapter_id in chapter_ids:
            error(f"catalog has duplicate chapter id: {chapter_id}")
        chapter_ids.add(chapter_id)
        catalog_chapters[chapter_id] = (part, chapter)
        if not isinstance(slug, str) or not slug_pattern.fullmatch(slug):
            error(f"catalog chapter {chapter_id}: invalid slug {slug!r}")
            continue
        if slug in slugs:
            error(f"catalog part {part_id}: duplicate chapter slug {slug}")
        slugs.add(slug)
        relative = pathlib.PurePosixPath(part_id) / f"{slug}.md"
        if relative in catalog_paths:
            error(f"catalog has duplicate chapter path: {relative}")
        catalog_paths.add(relative)
        if status not in (statuses or []):
            error(f"catalog chapter {chapter_id}: invalid status {status!r}")
        if not isinstance(chapter_titles, dict) or set(chapter_titles) != set(languages or []):
            error(f"catalog chapter {chapter_id}: titles must cover both languages")
        elif any(not isinstance(title, str) or not title.strip() for title in chapter_titles.values()):
            error(f"catalog chapter {chapter_id}: titles must be non-empty strings")
        core_reading = chapter.get("core_reading")
        if core_reading is not None and core_reading is not True:
            error(f"catalog chapter {chapter_id}: core_reading must be true when present")
        if core_reading is True:
            core_chapters.append(chapter_id)
        unexpected = set(chapter) - {"id", "slug", "status", "core_reading", "titles"}
        if unexpected:
            error(f"catalog chapter {chapter_id}: unexpected fields {sorted(unexpected)}")

if part_orders and part_orders != set(range(1, len(parts) + 1)):
    error("catalog part order must be contiguous starting at 1")
if len(core_chapters) != 6:
    error(f"catalog must declare exactly six core-reading chapters, found {len(core_chapters)}")


def markdown_map(language: str) -> dict[pathlib.PurePosixPath, pathlib.Path]:
    language_root = book / language
    if not language_root.is_dir():
        error(f"missing book language root: {language_root.relative_to(root)}")
        return {}
    return {
        pathlib.PurePosixPath(path.relative_to(language_root).as_posix()): path
        for path in language_root.rglob("*.md")
    }


english = markdown_map("en")
chinese = markdown_map("zh-CN")
for relative in sorted(set(english) | set(chinese), key=str):
    if relative not in english:
        error(f"English book mirror missing: docs/book/en/{relative}")
    if relative not in chinese:
        error(f"Chinese book mirror missing: docs/book/zh-CN/{relative}")

required_book_files = {
    pathlib.PurePosixPath("README.md"),
    pathlib.PurePosixPath("NAVIGATION.md"),
    pathlib.PurePosixPath("glossary.md"),
    pathlib.PurePosixPath("_templates/chapter.md"),
}
for relative in required_book_files:
    if relative not in english or relative not in chinese:
        error(f"book skeleton missing bilingual file: {relative}")

actual_chapter_paths = {
    relative
    for relative in set(english) | set(chinese)
    if relative not in required_book_files and "_templates" not in relative.parts
}
for relative in sorted(actual_chapter_paths - catalog_paths, key=str):
    error(f"chapter file is not declared in catalog: {relative}")


def parse_front_matter(path: pathlib.Path) -> dict:
    text = path.read_text(encoding="utf-8")
    lines = text.splitlines()
    if not lines or lines[0] != "---":
        error(f"{path.relative_to(root)}: missing Front Matter")
        return {}
    try:
        end = lines.index("---", 1)
    except ValueError:
        error(f"{path.relative_to(root)}: unclosed Front Matter")
        return {}
    result: dict[str, object] = {}
    active_list: str | None = None
    for number, line in enumerate(lines[1:end], start=2):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if line.startswith("  - "):
            if active_list is None:
                error(f"{path.relative_to(root)}:{number}: list item without a field")
                continue
            value = line[4:].strip()
            if not value:
                error(f"{path.relative_to(root)}:{number}: empty list item")
            cast = result.setdefault(active_list, [])
            if isinstance(cast, list):
                cast.append(value)
            continue
        match = re.fullmatch(r"([a-z_]+):(?: (.*))?", line)
        if not match:
            error(f"{path.relative_to(root)}:{number}: unsupported Front Matter syntax")
            active_list = None
            continue
        key, raw_value = match.groups()
        if key in result:
            error(f"{path.relative_to(root)}:{number}: duplicate field {key}")
        if raw_value is None or raw_value == "":
            result[key] = []
            active_list = key
            continue
        active_list = None
        value: object = raw_value.strip()
        if value == "null":
            value = None
        elif isinstance(value, str) and len(value) >= 2 and value[0] == value[-1] and value[0] in "'\"":
            value = value[1:-1]
        result[key] = value
    return result


def path_matches(pattern: str) -> bool:
    candidate = pathlib.Path(pattern)
    if candidate.is_absolute() or ".." in candidate.parts:
        return False
    return bool(glob.glob(str(root / pattern), recursive=True))


def validate_chapter(language: str, relative: pathlib.PurePosixPath, part: dict, chapter: dict) -> None:
    path = book / language / relative
    metadata = parse_front_matter(path)
    missing = required_metadata - set(metadata)
    extra = set(metadata) - required_metadata
    if missing:
        error(f"{path.relative_to(root)}: missing metadata fields {sorted(missing)}")
    if extra:
        error(f"{path.relative_to(root)}: unexpected metadata fields {sorted(extra)}")
    if metadata.get("id") != chapter["id"]:
        error(f"{path.relative_to(root)}: id does not match catalog")
    if metadata.get("title") != chapter["titles"][language]:
        error(f"{path.relative_to(root)}: title does not match catalog")
    if metadata.get("status") != chapter["status"]:
        error(f"{path.relative_to(root)}: status does not match catalog")
    audience = metadata.get("audience")
    if not isinstance(audience, list) or not audience:
        error(f"{path.relative_to(root)}: audience must be a non-empty list")
    elif len(audience) != len(set(audience)) or set(audience) - allowed_audiences:
        error(f"{path.relative_to(root)}: invalid or duplicate audience")
    prerequisites = metadata.get("prerequisites")
    if not isinstance(prerequisites, list):
        error(f"{path.relative_to(root)}: prerequisites must be a list")
    else:
        for prerequisite in prerequisites:
            if prerequisite not in chapter_ids:
                error(f"{path.relative_to(root)}: unknown prerequisite {prerequisite}")
        if chapter["id"] in prerequisites:
            error(f"{path.relative_to(root)}: chapter cannot require itself")
    for field in ("code_paths", "test_paths", "source_of_truth"):
        values = metadata.get(field)
        if not isinstance(values, list):
            error(f"{path.relative_to(root)}: {field} must be a list")
            continue
        if field == "source_of_truth" and not values:
            error(f"{path.relative_to(root)}: source_of_truth must not be empty")
        for value in values:
            if not isinstance(value, str) or not path_matches(value):
                error(f"{path.relative_to(root)}: {field} path does not exist: {value!r}")
    last_verified = metadata.get("last_verified")
    if chapter["status"] == "verified":
        if not isinstance(last_verified, str):
            error(f"{path.relative_to(root)}: verified chapter requires last_verified date")
        else:
            try:
                datetime.date.fromisoformat(last_verified)
            except ValueError:
                error(f"{path.relative_to(root)}: invalid last_verified date")
    elif last_verified is not None:
        error(f"{path.relative_to(root)}: draft chapter last_verified must be null")


for chapter_id, (part, chapter) in catalog_chapters.items():
    relative = pathlib.PurePosixPath(part["id"]) / f"{chapter['slug']}.md"
    present = relative in english or relative in chinese
    if chapter["status"] == "planned":
        if present:
            error(f"planned chapter must not have placeholder files: {chapter_id}")
        continue
    if relative not in english or relative not in chinese:
        error(f"{chapter['status']} chapter requires bilingual files: {chapter_id}")
        continue
    validate_chapter("en", relative, part, chapter)
    validate_chapter("zh-CN", relative, part, chapter)

secret_pattern = re.compile(
    r"(?:(?<![A-Za-z0-9])sk-[A-Za-z0-9_-]{16,}|"
    r"(?:api[_-]?key|token|secret)\s*[:=]\s*['\"]?[A-Za-z0-9_/-]{20,})",
    re.IGNORECASE,
)
for path in sorted(book.rglob("*")):
    if not path.is_file() or path.suffix not in {".md", ".json"}:
        continue
    text = path.read_text(encoding="utf-8")
    if secret_pattern.search(text):
        error(f"{path.relative_to(root)}: possible raw secret")
    if path.suffix == ".md":
        openings = len(re.findall(r"^```mermaid\s*$", text, re.MULTILINE))
        blocks = re.findall(r"^```mermaid\s*\n(.*?)^```\s*$", text, re.MULTILINE | re.DOTALL)
        if openings != len(blocks):
            error(f"{path.relative_to(root)}: unclosed Mermaid block")
        if any(not block.strip() for block in blocks):
            error(f"{path.relative_to(root)}: empty Mermaid block")

template_requirements = (
    "## Learning Objectives",
    "## Problem Background",
    "## CodeHelper Design",
    "## Code Map",
    "## Failure Modes and Security Boundaries",
    "## Tests and Verification",
    "## Hands-On Lab",
    "## Sources and Verification",
)
english_template = (book / "en" / "_templates" / "chapter.md").read_text(encoding="utf-8")
for heading in template_requirements:
    if heading not in english_template:
        error(f"English chapter template missing section: {heading}")

if errors:
    print("book check failed:", file=sys.stderr)
    for item in errors:
        print(f"  - {item}", file=sys.stderr)
    raise SystemExit(1)

print(
    "book check passed: "
    f"{len(parts)} parts, {len(chapter_ids)} planned/delivered chapters, "
    f"{len(actual_chapter_paths)} bilingual chapter pairs"
)
PY
