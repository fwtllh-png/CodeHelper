#!/usr/bin/env python3
"""canary-perf — Track benchmark performance and flag latency regressions.

Usage:
  scripts/canary-perf.py baseline [--output BASELINE]
      Record the current performance numbers as the baseline.

  scripts/canary-perf.py check [--baseline BASELINE] [--report REPORT] [--reuse-report]
      Run benchmarks and compare performance against the baseline.
      Exits non-zero when any task exceeds the regression threshold.

  scripts/canary-perf.py show [--baseline BASELINE] [--report REPORT] [--reuse-report]
      Display a performance comparison table between the current run and baseline.

The script runs the hermetic benchmark suite and extracts duration and token-usage
metrics. It compares P50/P95 latency against a stored baseline and flags tasks
whose P95 duration has regressed beyond the threshold (default: 30%).

Metrics tracked per task:
  - duration_ms       — wall-clock duration
  - input_tokens      — total input tokens
  - output_tokens     — total output tokens
  - reasoning_tokens  — reasoning tokens
  - cost_microunits   — cost estimate

Environment:
  BENCH_REPORT        Path to write the benchmark JSON report (default: .tmp/canary-perf-report.json)
  CANARY_PERF_THRESHOLD  Regression threshold as a fraction (default: 0.30 = 30%)
"""

import json
import os
import statistics
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_BASELINE = ROOT / "testdata" / "canary" / "perf-baseline.json"
DEFAULT_REPORT = ROOT / ".tmp" / "canary-perf-report.json"
DEFAULT_THRESHOLD = 0.30  # 30% regression threshold


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
        timeout=300,
    )

    if result.returncode != 0:
        print(f"canary-perf: benchmark run failed (exit {result.returncode})")
        print(result.stderr[-2000:] if len(result.stderr) > 2000 else result.stderr)
        sys.exit(result.returncode or 1)

    if not report_path.exists():
        print(f"canary-perf: benchmark report not written at {report_path}")
        sys.exit(1)

    return report_path


def load_report(path: Path) -> dict:
    """Load a benchmark report."""
    with open(path) as f:
        return json.load(f)


def extract_metrics(report: dict) -> dict:
    """Extract per-task performance metrics from a report."""
    metrics = {}
    for result in report.get("results", []):
        if result.get("error") or result.get("unavailable_reason"):
            continue
        task = result["task"]
        metrics[task] = {
            "category": result.get("category", ""),
            "duration_ms": result.get("duration_ms", 0),
            "input_tokens": result.get("input_tokens", 0),
            "output_tokens": result.get("output_tokens", 0),
            "reasoning_tokens": result.get("reasoning_tokens", 0),
            "cached_tokens": result.get("cached_tokens", 0),
            "uncached_input_tokens": result.get("uncached_input_tokens", 0),
            "cost_microunits": result.get("cost_microunits", 0),
            "passed": result.get("passed", False),
        }
    return metrics


def compute_summary(metrics: dict) -> dict:
    """Compute aggregate statistics from per-task metrics."""
    durations = [m["duration_ms"] for m in metrics.values() if m["duration_ms"] > 0]
    if not durations:
        return {"p50_ms": 0, "p95_ms": 0, "total_duration_ms": 0, "task_count": 0}

    sorted_durations = sorted(durations)
    p50 = sorted_durations[len(sorted_durations) // 2]
    p95_idx = int(len(sorted_durations) * 0.95)
    p95 = sorted_durations[min(p95_idx, len(sorted_durations) - 1)]

    return {
        "p50_ms": p50,
        "p95_ms": p95,
        "mean_ms": round(statistics.mean(durations), 1),
        "stdev_ms": round(statistics.stdev(durations), 1) if len(durations) > 1 else 0,
        "total_duration_ms": sum(durations),
        "task_count": len(metrics),
        "total_input_tokens": sum(m["input_tokens"] for m in metrics.values()),
        "total_output_tokens": sum(m["output_tokens"] for m in metrics.values()),
        "total_reasoning_tokens": sum(m["reasoning_tokens"] for m in metrics.values()),
        "total_cost_microunits": sum(m["cost_microunits"] for m in metrics.values()),
    }


def compare_perf(
    baseline_metrics: dict,
    current_metrics: dict,
    threshold: float,
) -> dict:
    """Compare performance metrics and return regressions."""
    regressions = []
    improvements = []
    new_tasks = []
    removed_tasks = []

    all_tasks = sorted(set(baseline_metrics.keys()) | set(current_metrics.keys()))

    for task in all_tasks:
        b = baseline_metrics.get(task)
        c = current_metrics.get(task)

        if b is None:
            new_tasks.append(task)
            continue
        if c is None:
            removed_tasks.append(task)
            continue

        # Duration regression check.
        b_dur = b["duration_ms"]
        c_dur = c["duration_ms"]

        if b_dur > 0 and c_dur > 0:
            ratio = c_dur / b_dur
            if ratio > 1.0 + threshold:
                regressions.append({
                    "task": task,
                    "category": b["category"],
                    "metric": "duration_ms",
                    "baseline": b_dur,
                    "current": c_dur,
                    "ratio": round(ratio, 2),
                    "change_pct": round((ratio - 1.0) * 100, 1),
                })
            elif ratio < 1.0 - threshold:
                improvements.append({
                    "task": task,
                    "category": b["category"],
                    "metric": "duration_ms",
                    "baseline": b_dur,
                    "current": c_dur,
                    "ratio": round(ratio, 2),
                    "change_pct": round((1.0 - ratio) * 100, 1),
                })

        # Token-cost regression (>50% increase in input tokens).
        for token_field in ["input_tokens", "output_tokens"]:
            b_tok = b.get(token_field, 0) or 0
            c_tok = c.get(token_field, 0) or 0
            if b_tok > 100 and c_tok > b_tok * 1.5:
                regressions.append({
                    "task": task,
                    "category": b["category"],
                    "metric": token_field,
                    "baseline": b_tok,
                    "current": c_tok,
                    "ratio": round(c_tok / b_tok, 2),
                    "change_pct": round((c_tok / b_tok - 1.0) * 100, 1),
                })

    return {
        "regressions": regressions,
        "improvements": improvements,
        "new_tasks": new_tasks,
        "removed_tasks": removed_tasks,
    }


def cmd_baseline(output: Path):
    """Record a new performance baseline."""
    run_benchmarks(output)

    if output != DEFAULT_BASELINE:
        DEFAULT_BASELINE.parent.mkdir(parents=True, exist_ok=True)
        import shutil
        shutil.copy2(output, DEFAULT_BASELINE)

    report = load_report(output)
    metrics = extract_metrics(report)
    summary = compute_summary(metrics)

    # Store the processed metrics alongside the raw report.
    perf_data = {
        "generated_at": report.get("generated_at", time.time()),
        "summary": summary,
        "tasks": metrics,
    }

    if output == DEFAULT_BASELINE:
        perf_path = DEFAULT_BASELINE.parent / "perf-metrics.json"
        with open(perf_path, "w") as f:
            json.dump(perf_data, f, indent=2)

    print(f"canary-perf: recorded baseline")
    print(f"  tasks:      {summary['task_count']}")
    print(f"  P50:        {summary['p50_ms']}ms")
    print(f"  P95:        {summary['p95_ms']}ms")
    print(f"  mean:       {summary['mean_ms']}ms")
    print(f"  baseline:   {output}")


def current_report(report_path: Path, reuse_report: bool) -> dict:
    if not reuse_report:
        run_benchmarks(report_path)
    elif not report_path.exists():
        print(f"canary-perf: shared report not found at {report_path}")
        sys.exit(1)
    return load_report(report_path)


def cmd_check(
    baseline_path: Path,
    report_path: Path,
    threshold: float,
    reuse_report: bool,
):
    """Check current performance against baseline."""
    if not baseline_path.exists():
        print(f"canary-perf: no baseline found at {baseline_path}")
        print("  Run 'scripts/canary-perf.py baseline' to create one.")
        sys.exit(1)

    baseline_report = load_report(baseline_path)
    current = current_report(report_path, reuse_report)

    baseline_metrics = extract_metrics(baseline_report)
    current_metrics = extract_metrics(current)

    diff = compare_perf(baseline_metrics, current_metrics, threshold)

    # Print summary.
    b_summary = compute_summary(baseline_metrics)
    c_summary = compute_summary(current_metrics)

    print(f"canary-perf: {c_summary['task_count']} tasks, threshold={int(threshold * 100)}%")
    print(f"  P50: {b_summary['p50_ms']}ms → {c_summary['p50_ms']}ms  "
          f"({_delta_str(b_summary['p50_ms'], c_summary['p50_ms'])})")
    print(f"  P95: {b_summary['p95_ms']}ms → {c_summary['p95_ms']}ms  "
          f"({_delta_str(b_summary['p95_ms'], c_summary['p95_ms'])})")
    print(f"  mean: {b_summary['mean_ms']}ms → {c_summary['mean_ms']}ms  "
          f"({_delta_str(b_summary['mean_ms'], c_summary['mean_ms'])})")

    if diff["regressions"]:
        print(f"\n  PERFORMANCE REGRESSIONS ({len(diff['regressions'])}):")
        for r in diff["regressions"]:
            print(f"    ✗ {r['task']} ({r['category']}): "
                  f"{r['metric']} {r['baseline']} → {r['current']} "
                  f"(+{r['change_pct']}%)")

    if diff["improvements"]:
        print(f"\n  PERFORMANCE IMPROVEMENTS ({len(diff['improvements'])}):")
        for r in diff["improvements"]:
            print(f"    ✓ {r['task']} ({r['category']}): "
                  f"{r['metric']} {r['baseline']} → {r['current']} "
                  f"(-{r['change_pct']}%)")

    if diff["new_tasks"]:
        print(f"\n  NEW TASKS ({len(diff['new_tasks'])}):")
        for t in diff["new_tasks"]:
            print(f"    + {t}")

    if diff["removed_tasks"]:
        print(f"\n  REMOVED TASKS ({len(diff['removed_tasks'])}):")
        for t in diff["removed_tasks"]:
            print(f"    - {t}")

    if diff["regressions"]:
        print(f"\ncanary-perf: FAILED — {len(diff['regressions'])} performance regression(s)")
        sys.exit(1)

    print("\ncanary-perf: OK — no performance regressions detected")


def cmd_show(baseline_path: Path, report_path: Path, reuse_report: bool):
    """Display a performance comparison table."""
    if not baseline_path.exists():
        print(f"canary-perf: no baseline found at {baseline_path}")
        sys.exit(1)

    baseline_report = load_report(baseline_path)
    current = current_report(report_path, reuse_report)

    baseline_metrics = extract_metrics(baseline_report)
    current_metrics = extract_metrics(current)

    # Table header.
    print(f"{'Task':<40} {'Category':<20} {'Base(ms)':>10} {'Curr(ms)':>10} {'Delta':>10}")
    print("-" * 90)

    all_tasks = sorted(set(baseline_metrics.keys()) | set(current_metrics.keys()))
    for task in all_tasks:
        b = baseline_metrics.get(task, {})
        c = current_metrics.get(task, {})
        b_dur = b.get("duration_ms", 0) if b else 0
        c_dur = c.get("duration_ms", 0) if c else 0
        category = (b or c).get("category", "")

        delta = _delta_str(b_dur, c_dur)
        print(f"{task:<40} {category:<20} {b_dur:>10} {c_dur:>10} {delta:>10}")


def _delta_str(baseline: float, current: float) -> str:
    """Format a delta string like '+12.3%' or '-5.1%'."""
    if baseline == 0:
        return "N/A"
    pct = (current - baseline) / baseline * 100
    sign = "+" if pct >= 0 else ""
    return f"{sign}{pct:.1f}%"


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(1)

    mode = sys.argv[1]

    args = sys.argv[2:]
    baseline_path = DEFAULT_BASELINE
    report_path = DEFAULT_REPORT
    output_path = DEFAULT_BASELINE
    threshold = float(os.environ.get("CANARY_PERF_THRESHOLD", DEFAULT_THRESHOLD))
    reuse_report = False

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
        elif args[i] == "--threshold" and i + 1 < len(args):
            threshold = float(args[i + 1])
            i += 2
        elif args[i] == "--reuse-report":
            reuse_report = True
            i += 1
        else:
            print(f"canary-perf: unknown argument: {args[i]}")
            sys.exit(1)

    if "CANARY_BASELINE" in os.environ:
        baseline_path = Path(os.environ["CANARY_BASELINE"])
    if "CANARY_REPORT" in os.environ:
        report_path = Path(os.environ["CANARY_REPORT"])

    if mode == "baseline":
        cmd_baseline(output_path)
    elif mode == "check":
        cmd_check(baseline_path, report_path, threshold, reuse_report)
    elif mode == "show":
        cmd_show(baseline_path, report_path, reuse_report)
    else:
        print(f"canary-perf: unknown mode: {mode}")
        print("  Use: baseline, check, or show")
        sys.exit(1)


if __name__ == "__main__":
    main()
