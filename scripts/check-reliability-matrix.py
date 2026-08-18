#!/usr/bin/env python3

import argparse
import json
import pathlib
import re
import subprocess
import sys


BOUNDARIES = {
    "provider",
    "tool",
    "approval",
    "input",
    "journal",
    "sqlite",
    "outbox",
    "host",
    "shutdown",
}
FAULTS = {
    "success",
    "retryable_failure",
    "permanent_failure",
    "cancellation",
    "crash_recovery",
}
INVARIANTS = {
    "no_duplicate_side_effect",
    "terminal_accounted",
    "not_permanently_running",
}


def fail(message):
    raise ValueError(message)


def load_matrix(path):
    with path.open(encoding="utf-8") as handle:
        value = json.load(handle)
    if value.get("version") != 1:
        fail("reliability matrix version must be 1")
    rows = value.get("boundaries")
    if not isinstance(rows, list):
        fail("reliability matrix boundaries must be an array")
    by_id = {}
    for row in rows:
        boundary = row.get("id")
        if boundary in by_id:
            fail(f"duplicate reliability boundary {boundary!r}")
        by_id[boundary] = row
    if set(by_id) != BOUNDARIES:
        fail(
            "reliability boundaries differ: "
            f"got={sorted(by_id)} want={sorted(BOUNDARIES)}"
        )
    references = {}
    for boundary, row in by_id.items():
        if set(row.get("invariants", [])) != INVARIANTS:
            fail(f"{boundary}: incomplete invariant set")
        cases = row.get("cases")
        if not isinstance(cases, dict) or set(cases) != FAULTS:
            fail(f"{boundary}: incomplete fault case set")
        for fault, case in cases.items():
            package = case.get("package", "")
            test = case.get("test", "")
            if (
                not package.startswith("./internal/")
                or not re.fullmatch(r"Test[A-Za-z0-9_]+", test)
                or not case.get("expected_state")
                or not case.get("recovery")
            ):
                fail(f"{boundary}/{fault}: invalid test contract")
            references.setdefault(package, set()).add(test)
    return references


def validate_tests(root, references, run):
    for package in sorted(references):
        tests = sorted(references[package])
        listed = subprocess.run(
            ["go", "test", package, "-list", "^Test"],
            cwd=root,
            check=True,
            text=True,
            stdout=subprocess.PIPE,
        ).stdout.splitlines()
        available = set(listed)
        missing = sorted(set(tests) - available)
        if missing:
            fail(f"{package}: missing tests {missing}")
        if run:
            pattern = "^(" + "|".join(re.escape(test) for test in tests) + ")$"
            subprocess.run(
                ["go", "test", "-count=1", package, "-run", pattern],
                cwd=root,
                check=True,
            )


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("matrix", type=pathlib.Path)
    parser.add_argument("--run", action="store_true")
    args = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parent.parent
    matrix = args.matrix
    if not matrix.is_absolute():
        matrix = root / matrix
    try:
        references = load_matrix(matrix)
        validate_tests(root, references, args.run)
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"reliability matrix failed: {error}", file=sys.stderr)
        return 1
    print(
        "reliability matrix passed: "
        f"{len(BOUNDARIES)} boundaries, {len(BOUNDARIES) * len(FAULTS)} cases"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
