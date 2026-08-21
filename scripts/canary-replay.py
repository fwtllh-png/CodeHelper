#!/usr/bin/env python3
"""canary-replay — Record or replay benchmark runs to detect behavioral regressions.

Usage:
  scripts/canary-replay.py record [--output BASELINE]
      Record the current benchmark run as the golden baseline.

  scripts/canary-replay.py check [--baseline BASELINE] [--report REPORT]
      Run benchmarks and compare against the golden baseline.
      Exits non-zero when any regression is detected.

  scripts/canary-replay.py diff [--baseline BASELINE] [--report REPORT]
      Show a human-readable diff between the current run and the baseline.

The script runs the hermetic benchmark suite (fixture provider, no network/model)
and compares the results against a stored JSON baseline. It detects:

  - Tasks that changed from pass → fail (regressions)
  - Tasks that changed from fail → pass (improvements)
  - Changes in terminal outcome
  - Significant token-count drift (>10%)
  - New or removed tasks

Environment:
  BENCH_REPORT   Path to write the benchmark JSON report (default: .tmp/canary-report.json)
"""

import json
import os
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_BASELINE = ROOT / "testdata" / "canary" / "benchmark-baseline.json"
DEFAULT_REPORT = ROOT / ".tmp" / "canary-report.json"


def run_benchmarks(report_path: Path) -> Path:
    """Run the hermetic benchmark suite and return the report path."""
    report_path.parent.mkdir(parents=True, exist_ok=True)

    env = os.environ.copy()
    env["CODEHELPER_BENCH_REPORT"] = str(report_path)

    result = subprocess.run(
        [
            "go", "test",
            "-tags=capability",
            "-count=1", "-v",
            "./internal/host/bench/...",
        ],
        cwd=ROOT,
        env=env,
        capture_output=True,
        text=True,
        timeout=300,  # 5 minutes
    )

    if result.returncode != 0:
        print(f"canary-replay: benchmark run failed (exit {result.returncode})")
        print(result.stderr[-2000:] if len(result.stderr) > 2000 else result.stderr)
        sys.exit(result.returncode or 1)

    if not report_path.exists():
        print(f"canary-replay: benchmark report not written at {report_path}")
        sys.exit(1)

    return report_path


def load_report(path: Path) -> dict:
    """Load and validate a benchmark report."""
    with open(path) as f:
        report = json.load(f)

    if "results" not in report:
        print(f"canary-replay: invalid report format in {path} (missing 'results')")
        sys.exit(1)

    return report


def build_result_index(report: dict) -> dict:
    """Build a lookup table task_name → result."""
    return {r["task"]: r for r in report["results"]}


def compare_results(baseline: dict, current: dict) -> dict:
    """Compare two result dictionaries and return a diff."""
    baseline_idx = build_result_index(baseline)
    current_idx = build_result_index(current)

    all_tasks = sorted(set(baseline_idx.keys()) | set(current_idx.keys()))

    regressions = []
    improvements = []
    drifts = []
    new_tasks = []
    removed_tasks = []

    for task in all_tasks:
        b = baseline_idx.get(task)
        c = current_idx.get(task)

        if b is None:
            new_tasks.append(task)
            continue
        if c is None:
            removed_tasks.append(task)
            continue

        # Check pass/fail status.
        if b["passed"] and not c["passed"]:
            regressions.append({
                "task": task,
                "category": c.get("category", ""),
                "was": "passed",
                "now": "failed",
                "failures": c.get("failures", []),
            })
        elif not b["passed"] and c["passed"]:
            improvements.append({
                "task": task,
                "category": c.get("category", ""),
                "was": "failed",
                "now": "passed",
            })

        # Check terminal outcome.
        if b.get("terminal") != c.get("terminal"):
            if c["passed"]:
                drifts.append({
                    "task": task,
                    "field": "terminal",
                    "baseline": b.get("terminal"),
                    "current": c.get("terminal"),
                })

        # Check token drift (>10%).
        for token_field in [
            "input_tokens", "output_tokens", "reasoning_tokens",
            "cached_tokens", "uncached_input_tokens",
        ]:
            b_val = b.get(token_field, 0) or 0
            c_val = c.get(token_field, 0) or 0
            if b_val > 0:
                drift_pct = abs(c_val - b_val) / b_val
                if drift_pct > 0.10:
                    drifts.append({
                        "task": task,
                        "field": token_field,
                        "baseline": b_val,
                        "current": c_val,
                        "drift_pct": round(drift_pct * 100, 1),
                    })

    return {
        "regressions": regressions,
        "improvements": improvements,
        "drifts": drifts,
        "new_tasks": new_tasks,
        "removed_tasks": removed_tasks,
    }


def cmd_record(output: Path):
    """Record a new golden baseline."""
    run_benchmarks(output)

    if output != DEFAULT_BASELINE:
        DEFAULT_BASELINE.parent.mkdir(parents=True, exist_ok=True)
        import shutil
        shutil.copy2(output, DEFAULT_BASELINE)

    report = load_report(output)
    summary = {
        "total": report["total"],
        "passed": report["passed"],
        "failed": report["failed"],
        "unavailable": report.get("unavailable", 0),
        "duration_ms": report.get("duration_ms", 0),
        "generated_at": report.get("generated_at", time.time()),
    }

    if output == DEFAULT_BASELINE:
        # Write a compact summary alongside the baseline.
        summary_path = DEFAULT_BASELINE.parent / "baseline-summary.json"
        with open(summary_path, "w") as f:
            json.dump(summary, f, indent=2)

    print(f"canary-replay: recorded baseline ({summary['passed']}/{summary['total']} passed)")
    print(f"  baseline: {output}")


def cmd_check(baseline_path: Path, report_path: Path):
    """Check current run against baseline."""
    if not baseline_path.exists():
        print(f"canary-replay: no baseline found at {baseline_path}")
        print("  Run 'scripts/canary-replay.py record' to create one.")
        sys.exit(1)

    run_benchmarks(report_path)

    baseline = load_report(baseline_path)
    current = load_report(report_path)
    diff = compare_results(baseline, current)

    # Print summary.
    print(f"canary-replay: {current['total']} tasks, {current['passed']} passed, {current['failed']} failed")

    if diff["regressions"]:
        print(f"\n  REGRESSIONS ({len(diff['regressions'])}):")
        for r in diff["regressions"]:
            print(f"    ✗ {r['task']} ({r['category']}): {r['was']} → {r['now']}")
            for f in r.get("failures", [])[:3]:
                print(f"      - {f}")

    if diff["improvements"]:
        print(f"\n  IMPROVEMENTS ({len(diff['improvements'])}):")
        for r in diff["improvements"]:
            print(f"    ✓ {r['task']} ({r['category']}): {r['was']} → {r['now']}")

    if diff["drifts"]:
        print(f"\n  DRIFTS ({len(diff['drifts'])}):")
        for d in diff["drifts"][:20]:
            print(f"    ~ {d['task']}: {d['field']} {d['baseline']} → {d['current']} ({d.get('drift_pct', '?')}%)")

    if diff["new_tasks"]:
        print(f"\n  NEW TASKS ({len(diff['new_tasks'])}):")
        for t in diff["new_tasks"]:
            print(f"    + {t}")

    if diff["removed_tasks"]:
        print(f"\n  REMOVED TASKS ({len(diff['removed_tasks'])}):")
        for t in diff["removed_tasks"]:
            print(f"    - {t}")

    # Update the baseline summary.
    if diff["new_tasks"] or diff["removed_tasks"]:
        print("\n  Note: task list changed. Run 'scripts/canary-replay.py record' to update the baseline.")

    # Exit non-zero on regressions.
    if diff["regressions"]:
        print(f"\ncanary-replay: FAILED — {len(diff['regressions'])} regression(s) detected")
        sys.exit(1)

    if not diff["regressions"] and not diff["drifts"]:
        print("\ncanary-replay: OK — no regressions or drifts detected")
    else:
        print("\ncanary-replay: OK — no regressions detected (drifts are informational)")


def cmd_diff(baseline_path: Path, report_path: Path):
    """Show a human-readable diff."""
    if not baseline_path.exists():
        print(f"canary-replay: no baseline found at {baseline_path}")
        sys.exit(1)

    run_benchmarks(report_path)

    baseline = load_report(baseline_path)
    current = load_report(report_path)
    diff = compare_results(baseline, current)

    print(json.dumps(diff, indent=2))


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(1)

    mode = sys.argv[1]

    # Parse optional arguments.
    args = sys.argv[2:]
    baseline_path = DEFAULT_BASELINE
    report_path = DEFAULT_REPORT
    output_path = DEFAULT_BASELINE

    i = 0
    while i < len(args):
        if args[i] == "--baseline" and i + 1 < len(args):
            baseline_path = Path(args[i + 1])
            i += 2
        elif args[i] == "--report" and i + 1 < len(args):
            report_path = Path(args[i + 1])
            i += 2
        elif args[i] == "--output" and i + 1 < len(args):
            output_path = Path(args[i + 1])
            i += 2
        else:
            print(f"canary-replay: unknown argument: {args[i]}")
            sys.exit(1)

    # Override from environment.
    if "CANARY_BASELINE" in os.environ:
        baseline_path = Path(os.environ["CANARY_BASELINE"])
    if "CANARY_REPORT" in os.environ:
        report_path = Path(os.environ["CANARY_REPORT"])

    if mode == "record":
        cmd_record(output_path)
    elif mode == "check":
        cmd_check(baseline_path, report_path)
    elif mode == "diff":
        cmd_diff(baseline_path, report_path)
    else:
        print(f"canary-replay: unknown mode: {mode}")
        print("  Use: record, check, or diff")
        sys.exit(1)


if __name__ == "__main__":
    main()