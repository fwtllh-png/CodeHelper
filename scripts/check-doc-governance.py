#!/usr/bin/env python3
"""Validate and execute QCode documentation governance contracts."""

from __future__ import annotations

import argparse
import datetime as dt
import fnmatch
import hashlib
import json
import os
import pathlib
import re
import subprocess
import sys
import urllib.error
import urllib.request


ROOT = pathlib.Path(__file__).resolve().parent.parent
BOOK = ROOT / "docs" / "book"
CONFIG_PATH = BOOK / "governance.json"
CATALOG_PATH = BOOK / "catalog.json"
CHAPTER_FIELDS = {
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


def load_json(path: pathlib.Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def parse_front_matter(path: pathlib.Path) -> dict[str, object]:
    lines = path.read_text(encoding="utf-8").splitlines()
    if not lines or lines[0] != "---":
        raise ValueError(f"{path.relative_to(ROOT)}: missing Front Matter")
    try:
        end = lines.index("---", 1)
    except ValueError as exc:
        raise ValueError(f"{path.relative_to(ROOT)}: unclosed Front Matter") from exc
    result: dict[str, object] = {}
    active: str | None = None
    for line in lines[1:end]:
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if line.startswith("  - "):
            if active is None:
                raise ValueError(f"{path.relative_to(ROOT)}: orphan list item")
            value = line[4:].strip()
            values = result.setdefault(active, [])
            if not isinstance(values, list):
                raise ValueError(f"{path.relative_to(ROOT)}: invalid list {active}")
            values.append(value)
            continue
        match = re.fullmatch(r"([a-z_]+):(?: (.*))?", line)
        if not match:
            raise ValueError(f"{path.relative_to(ROOT)}: unsupported Front Matter")
        key, raw = match.groups()
        if raw is None or raw == "":
            result[key] = []
            active = key
        else:
            active = None
            result[key] = None if raw == "null" else raw.strip("'\"")
    return result


def chapters() -> dict[str, dict]:
    result: dict[str, dict] = {}
    for path in sorted((BOOK / "zh-CN").glob("*/*.md")):
        if "_templates" in path.parts:
            continue
        metadata = parse_front_matter(path)
        chapter_id = metadata.get("id")
        if isinstance(chapter_id, str):
            result[chapter_id] = {
                "path": path,
                "relative": path.relative_to(BOOK / "zh-CN"),
                "metadata": metadata,
            }
    return result


def matches(path: str, pattern: str) -> bool:
    normalized = path.strip("/")
    candidate = pattern.strip("/")
    if any(char in candidate for char in "*?["):
        return fnmatch.fnmatch(normalized, candidate)
    absolute = ROOT / candidate
    if absolute.is_dir():
        return normalized == candidate or normalized.startswith(candidate + "/")
    return normalized == candidate


def check(strict_drift: bool) -> list[str]:
    errors: list[str] = []
    config = load_json(CONFIG_PATH)
    catalog = load_json(CATALOG_PATH)
    chapter_map = chapters()
    owner_ids = {owner.get("id") for owner in config.get("owners", [])}
    if config.get("schema_version") != 1:
        errors.append("governance.json: schema_version must be 1")
    max_age = config.get("freshness", {}).get("verified_max_age_days")
    warning_age = config.get("freshness", {}).get("warning_age_days")
    if (
        not isinstance(max_age, int)
        or not isinstance(warning_age, int)
        or not 0 < warning_age < max_age
    ):
        errors.append("governance.json: freshness must satisfy 0 < warning < maximum")
        max_age = 0
        warning_age = 0
    assignments: dict[str, list[str]] = {chapter_id: [] for chapter_id in chapter_map}
    domain_ids: set[str] = set()
    for domain in config.get("domains", []):
        domain_id = domain.get("id")
        if domain_id in domain_ids:
            errors.append(f"governance.json: duplicate domain {domain_id}")
        domain_ids.add(domain_id)
        if domain.get("owner") not in owner_ids:
            errors.append(f"governance.json: domain {domain_id} has unknown owner")
        for pattern in domain.get("chapter_patterns", []):
            matched = [
                chapter_id for chapter_id in chapter_map
                if fnmatch.fnmatch(chapter_id, pattern)
            ]
            if not matched:
                errors.append(f"governance.json: chapter pattern matches nothing: {pattern}")
            for chapter_id in matched:
                assignments[chapter_id].append(domain_id)
        for pattern in domain.get("source_patterns", []):
            if pattern.startswith("/") or ".." in pathlib.PurePosixPath(pattern).parts:
                errors.append(f"governance.json: invalid source pattern {pattern}")
    for chapter_id, domains in assignments.items():
        if len(domains) != 1:
            errors.append(
                f"governance.json: chapter {chapter_id} must have one owner domain, got {domains}"
            )
    catalog_ids = {
        chapter["id"]
        for part in catalog["parts"]
        for chapter in part["chapters"]
        if chapter["status"] != "planned"
    }
    if catalog_ids != set(chapter_map):
        errors.append("governance.json: delivered chapter set differs from catalog")
    today = dt.date.today()
    for chapter_id, chapter in chapter_map.items():
        metadata = chapter["metadata"]
        if set(metadata) != CHAPTER_FIELDS:
            errors.append(f"{chapter['relative']}: metadata fields drifted")
            continue
        if metadata["status"] != "verified":
            continue
        try:
            verified = dt.date.fromisoformat(str(metadata["last_verified"]))
        except ValueError:
            errors.append(f"{chapter['relative']}: invalid last_verified")
            continue
        if verified > today:
            errors.append(f"{chapter['relative']}: last_verified is in the future")
            continue
        if (today - verified).days > max_age:
            errors.append(
                f"{chapter['relative']}: verification is older than {max_age} days"
            )
        elif (today - verified).days > warning_age:
            print(
                "documentation governance warning: "
                f"{chapter['relative']} is older than {warning_age} days",
                file=sys.stderr,
            )
        if strict_drift:
            changed = source_changes_after(chapter, verified)
            if changed:
                errors.append(
                    f"{chapter['relative']}: source changed after verification: {changed[0]}"
                )
    registered = {item["path"] for item in config.get("screenshots", [])}
    image_paths = {
        path.relative_to(ROOT).as_posix()
        for path in BOOK.rglob("*")
        if path.suffix.lower() in {".png", ".jpg", ".jpeg", ".svg", ".webp"}
    }
    if registered != image_paths:
        errors.append("governance.json: screenshot manifest differs from book images")
    for item in config.get("screenshots", []):
        path = ROOT / item["path"]
        digest = hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() else ""
        if digest != item.get("sha256"):
            errors.append(f"governance.json: screenshot digest drifted: {item['path']}")
    fact_ids: set[str] = set()
    for fact in config.get("release_facts", []):
        if fact.get("id") in fact_ids:
            errors.append(f"governance.json: duplicate release fact {fact.get('id')}")
        fact_ids.add(fact.get("id"))
        if not fact.get("command") or not all(
            isinstance(arg, str) and arg for arg in fact["command"]
        ):
            errors.append(f"governance.json: invalid release command {fact.get('id')}")
    return errors


def source_changes_after(chapter: dict, verified: dt.date) -> list[str]:
    metadata = chapter["metadata"]
    patterns = [
        value
        for field in ("code_paths", "test_paths", "source_of_truth")
        for value in metadata[field]
        if isinstance(value, str)
    ]
    changed: list[str] = []
    for pattern in patterns:
        command = ["git", "log", "-1", "--format=%cs", "--", pattern]
        result = subprocess.run(command, cwd=ROOT, text=True, capture_output=True)
        value = result.stdout.strip()
        if value and dt.date.fromisoformat(value) > verified:
            changed.append(pattern)
    return changed


def set_front_matter_fields(path: pathlib.Path, fields: dict[str, str | None]) -> bool:
    """Rewrite scalar Front Matter fields in place; returns True when changed.

    Only ``key: value`` lines inside the Front Matter block are touched; the
    rest of the file is preserved byte-for-byte, including the trailing
    newline.
    """
    text = path.read_text(encoding="utf-8")
    lines = text.splitlines()
    if not lines or lines[0] != "---":
        raise ValueError(f"{path.relative_to(ROOT)}: missing Front Matter")
    try:
        end = lines.index("---", 1)
    except ValueError as exc:
        raise ValueError(f"{path.relative_to(ROOT)}: unclosed Front Matter") from exc
    changed = False
    for index in range(1, end):
        match = re.fullmatch(r"([a-z_]+):( .*)?", lines[index])
        if not match or match.group(1) not in fields:
            continue
        value = fields[match.group(1)]
        replacement = (
            f"{match.group(1)}: null" if value is None else f"{match.group(1)}: {value}"
        )
        if lines[index] != replacement:
            lines[index] = replacement
            changed = True
    if not changed:
        return False
    joined = "\n".join(lines)
    path.write_text(joined + ("\n" if text.endswith("\n") else ""), encoding="utf-8")
    return True


def set_catalog_status(
    chapter_id: str, status: str, path: pathlib.Path = CATALOG_PATH
) -> bool:
    """Rewrite one chapter status in catalog.json without reformatting the file."""
    text = path.read_text(encoding="utf-8")
    pattern = re.compile(
        r'(\{"id": "' + re.escape(chapter_id) + r'", "slug": "[^"]*", )"status": "[a-z]+"'
    )
    updated, count = pattern.subn(r'\1"status": "' + status + '"', text)
    if count == 0:
        raise ValueError(f"{path.relative_to(ROOT)}: chapter {chapter_id} status not found")
    if updated == text:
        return False
    path.write_text(updated, encoding="utf-8")
    return True


def run_reverify(dry_run: bool) -> int:
    """Reconcile verified chapters with their sources.

    A chapter whose declared source paths changed after ``last_verified`` is
    downgraded to ``draft`` in the Chinese file and catalog; a chapter whose
    sources are unchanged is re-stamped with today's date.
    """
    today = dt.date.today()
    restamped: list[str] = []
    downgraded: list[str] = []
    skipped: list[str] = []
    for chapter_id, chapter in sorted(chapters().items()):
        metadata = chapter["metadata"]
        if metadata.get("status") != "verified":
            continue
        try:
            verified = dt.date.fromisoformat(str(metadata["last_verified"]))
        except (TypeError, ValueError):
            skipped.append(chapter_id)
            print(
                f"reverify: skip {chapter['relative']}: invalid last_verified",
                file=sys.stderr,
            )
            continue
        if verified > today:
            skipped.append(chapter_id)
            print(
                f"reverify: skip {chapter['relative']}: last_verified is in the future",
                file=sys.stderr,
            )
            continue
        drifted = bool(source_changes_after(chapter, verified))
        fields = (
            {"status": "draft", "last_verified": None}
            if drifted
            else {"last_verified": today.isoformat()}
        )
        action = "downgrade to draft" if drifted else f"re-stamp {today.isoformat()}"
        if dry_run:
            print(f"reverify: {chapter['relative']}: {action}")
            if drifted:
                downgraded.append(chapter_id)
            else:
                restamped.append(chapter_id)
            continue
        zh_path = BOOK / "zh-CN" / chapter["relative"]
        try:
            zh_metadata = parse_front_matter(zh_path)
        except ValueError as exc:
            skipped.append(chapter_id)
            print(f"reverify: {chapter['relative']}: {exc}", file=sys.stderr)
            continue
        if not fields.keys() <= zh_metadata.keys():
            skipped.append(chapter_id)
            print(
                f"reverify: {chapter['relative']}: Front Matter fields not found",
                file=sys.stderr,
            )
            continue
        set_front_matter_fields(zh_path, fields)
        if drifted:
            set_catalog_status(chapter_id, "draft")
            downgraded.append(chapter_id)
        else:
            restamped.append(chapter_id)
        print(f"reverify: {chapter['relative']}: {action}")
    print(
        f"reverify: {len(restamped)} re-stamped, {len(downgraded)} downgraded to draft, "
        f"{len(skipped)} skipped"
    )
    return 1 if skipped else 0


def changed_paths(base: str, head: str) -> list[str]:
    result = subprocess.run(
        ["git", "diff", "--name-only", f"{base}...{head}"],
        cwd=ROOT,
        text=True,
        capture_output=True,
        check=True,
    )
    return [line for line in result.stdout.splitlines() if line]


def impacted_chapters(paths: list[str]) -> set[str]:
    config = load_json(CONFIG_PATH)
    chapter_map = chapters()
    impacted: set[str] = set()
    for path in paths:
        direct: set[str] = set()
        for chapter_id, chapter in chapter_map.items():
            metadata = chapter["metadata"]
            patterns = [
                value
                for field in ("code_paths", "test_paths", "source_of_truth")
                for value in metadata[field]
                if isinstance(value, str)
            ]
            if any(matches(path, pattern) for pattern in patterns):
                direct.add(chapter_id)
        if direct:
            impacted.update(direct)
            continue
        for domain in config["domains"]:
            if any(matches(path, pattern) for pattern in domain["source_patterns"]):
                impacted.update(
                    chapter_id
                    for chapter_id in chapter_map
                    if any(
                        fnmatch.fnmatch(chapter_id, pattern)
                        for pattern in domain["chapter_patterns"]
                    )
                )
    return impacted


def documentation_ids(paths: list[str]) -> set[str]:
    result: set[str] = set()
    prefix = "docs/book/zh-CN/"
    for path in paths:
        if path.startswith(prefix) and path.endswith(".md"):
            file_path = ROOT / path
            if file_path.is_file():
                chapter_id = parse_front_matter(file_path).get("id")
                if isinstance(chapter_id, str):
                    result.add(chapter_id)
    return result


def pr_body() -> str:
    event_path = os.environ.get("GITHUB_EVENT_PATH")
    if not event_path or not pathlib.Path(event_path).is_file():
        return ""
    event = load_json(pathlib.Path(event_path))
    return str(event.get("pull_request", {}).get("body") or "")


def run_impact(base: str, head: str, body: str) -> int:
    paths = changed_paths(base, head)
    impacted = impacted_chapters(paths)
    updated = documentation_ids(paths)
    missing = sorted(impacted - updated)
    override = re.search(r"(?mi)^Documentation-impact:\s*none\s*$", body)
    rationale = re.search(r"(?mi)^Documentation-rationale:\s*(\S.+)$", body)
    valid_rationale = bool(
        rationale and rationale.group(1).strip().upper() not in {"N/A", "NA", "NONE"}
    )
    affected = re.search(r"(?mi)^Documentation-impact:\s*affected\s*$", body)
    chapter_line = re.search(r"(?mi)^Documentation-chapters:\s*(\S.+)$", body)
    declared = {
        item.strip()
        for item in chapter_line.group(1).split(",")
        if item.strip() and item.strip().upper() != "N/A"
    } if chapter_line else set()
    print("changed paths:", len(paths))
    print("affected chapters:", ", ".join(sorted(impacted)) or "none")
    print("updated chapters:", ", ".join(sorted(updated)) or "none")
    if missing and not (override and valid_rationale):
        print(
            "documentation impact check failed; update the Chinese chapter or "
            "declare Documentation-impact: none with a rationale",
            file=sys.stderr,
        )
        print("missing chapter updates: " + ", ".join(missing), file=sys.stderr)
        return 1
    if impacted and not missing and (not affected or not impacted.issubset(declared)):
        print(
            "documentation impact check failed; declare Documentation-impact: "
            "affected and list every affected chapter ID",
            file=sys.stderr,
        )
        print(
            "undeclared chapter IDs: " + ", ".join(sorted(impacted - declared)),
            file=sys.stderr,
        )
        return 1
    if missing:
        assert rationale is not None
        print("documented no-change rationale:", rationale.group(1))
    return 0


def run_release() -> int:
    errors = check(strict_drift=True)
    if errors:
        print_errors(errors)
        return 1
    for fact in load_json(CONFIG_PATH)["release_facts"]:
        print(f"release fact [{fact['id']}]: {' '.join(fact['command'])}", flush=True)
        result = subprocess.run(fact["command"], cwd=ROOT)
        if result.returncode:
            return result.returncode
    print("release documentation facts verified")
    return 0


def external_links() -> int:
    config = load_json(CONFIG_PATH)
    excluded = set(config.get("external_link_exclusions", []))
    pattern = re.compile(r"!?\[[^\]]*\]\((https?://[^ )]+)")
    links: set[str] = set()
    for path in ROOT.rglob("*.md"):
        if any(part in {".git", "node_modules", "dist"} for part in path.parts):
            continue
        links.update(pattern.findall(path.read_text(encoding="utf-8")))
    errors: list[str] = []
    for link in sorted(links - excluded):
        request = urllib.request.Request(
            link, headers={"User-Agent": "QCode-doc-governance/1"}
        )
        try:
            with urllib.request.urlopen(request, timeout=15) as response:
                if response.status >= 400:
                    errors.append(f"{link}: HTTP {response.status}")
        except (urllib.error.URLError, TimeoutError) as exc:
            errors.append(f"{link}: {exc}")
    if errors:
        print_errors(errors)
        return 1
    print(f"external link check passed: {len(links - excluded)} links")
    return 0


def print_errors(errors: list[str]) -> None:
    print("documentation governance check failed:", file=sys.stderr)
    for error in errors:
        print(f"  - {error}", file=sys.stderr)


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    check_parser = subparsers.add_parser("check")
    check_parser.add_argument("--strict-drift", action="store_true")
    reverify_parser = subparsers.add_parser("reverify")
    reverify_parser.add_argument("--dry-run", action="store_true")
    impact_parser = subparsers.add_parser("impact")
    impact_parser.add_argument("--base", required=True)
    impact_parser.add_argument("--head", default="HEAD")
    impact_parser.add_argument("--body", default=None)
    subparsers.add_parser("release")
    subparsers.add_parser("external-links")
    args = parser.parse_args()
    if args.command == "check":
        errors = check(args.strict_drift)
        if errors:
            print_errors(errors)
            return 1
        print("documentation governance check passed")
        return 0
    if args.command == "reverify":
        return run_reverify(args.dry_run)
    if args.command == "impact":
        return run_impact(args.base, args.head, args.body if args.body is not None else pr_body())
    if args.command == "release":
        return run_release()
    if args.command == "external-links":
        return external_links()
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
