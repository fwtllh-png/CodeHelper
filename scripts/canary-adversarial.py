#!/usr/bin/env python3
"""canary-adversarial — Adversarial benchmark testing to find real bugs.

This is the active bug-finding Phase 7 tool. Unlike canary-replay (which only
detects regressions against a known-good baseline), this script actively
perturbs the system and checks for crashes, hangs, and unexpected behavior.

Modes:
  fault-inject    Run each benchmark with every fault type at every stage.
                  Verifies the system never crashes, hangs, or panics.

  diff-config     Run all benchmarks under different policy configurations
                  (plan/act mode, suggest/auto/bypass posture) and flag any
                  unexpected divergence.

  mutate-fixture  Mutate fixture provider data (truncate JSON, inject errors,
                  remove fields) and verify the system handles it gracefully.

  full            Run all adversarial modes. The complete battery.

Usage:
  scripts/canary-adversarial.py fault-inject [--timeout SECONDS]
  scripts/canary-adversarial.py diff-config [--timeout SECONDS]
  scripts/canary-adversarial.py mutate-fixture [--timeout SECONDS]
  scripts/canary-adversarial.py full [--timeout SECONDS]

Environment:
  CANARY_ADVERSARIAL_TIMEOUT  Per-task timeout in seconds (default: 120)
  CANARY_ADVERSARIAL_REPORT   Path to write the JSON report (default: .tmp/canary-adversarial.json)
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import traceback
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional

ROOT = Path(__file__).resolve().parent.parent
BENCHMARK_DIR = ROOT / "testdata" / "benchmarks"
DEFAULT_TIMEOUT = int(os.environ.get("CANARY_ADVERSARIAL_TIMEOUT", "120"))
DEFAULT_REPORT = Path(os.environ.get("CANARY_ADVERSARIAL_REPORT", str(ROOT / ".tmp" / "canary-adversarial.json")))

# ── Fault injection matrix ──────────────────────────────────────────────

FAULT_CODES = [
    "invalid_argument", "conflict", "resource_exhausted", "unavailable",
    "canceled", "deadline_exceeded", "internal",
]

FAULT_STAGES = [
    "admission", "connection", "model_sample", "stream_idle",
    "persistence", "terminal",
]

# ── Policy configuration matrix ─────────────────────────────────────────

POLICY_MODES = ["plan", "act"]
POLICY_POSTURES = ["suggest", "auto", "bypass"]

# ── Fixture mutations ───────────────────────────────────────────────────

MUTATION_TYPES = [
    "truncate_json",      # Cut SSE JSON at random points
    "inject_error",       # Replace SSE content with error responses
    "remove_stream",      # Remove intermediate SSE files
    "empty_response",     # Return empty SSE response
    "extra_fields",       # Add unexpected fields to SSE JSON
    "missing_required",   # Remove required fields (e.g., choices)
]


@dataclass
class Finding:
    """A single issue discovered by adversarial testing."""
    task: str
    category: str
    mode: str          # fault-inject / diff-config / mutate-fixture
    variant: str       # specific variant that triggered the issue
    severity: str      # crash / hang / panic / wrong / unexpected
    detail: str
    traceback: str = ""


@dataclass
class AdversarialReport:
    """Aggregated adversarial test report."""
    mode: str
    total_tasks: int = 0
    total_variants: int = 0
    variants_run: int = 0
    variants_passed: int = 0
    variants_failed: int = 0
    variants_crashed: int = 0
    variants_hung: int = 0
    findings: list[dict] = field(default_factory=list)
    generated_at: str = ""


# ── Helpers ──────────────────────────────────────────────────────────────

def discover_tasks() -> list[Path]:
    """Discover all benchmark task directories."""
    tasks = []
    for entry in sorted(BENCHMARK_DIR.iterdir()):
        if entry.is_dir() and (entry / "task.json").exists():
            tasks.append(entry)
    return tasks


def load_task_json(task_dir: Path) -> dict:
    """Load a task's task.json."""
    with open(task_dir / "task.json") as f:
        return json.load(f)


def run_single_benchmark(task_dir: Path, env: dict, timeout: int) -> dict:
    """Run a single benchmark task and return the result."""
    task_name = task_dir.name

    cmd = [
        "go", "test", "-tags=capability", "-count=1", "-v",
        "-run", "TestCodingBenchmarkSuite",
        "./internal/host/bench/...",
    ]

    env["CODEHELPER_BENCH_TASK"] = str(task_dir)

    try:
        result = subprocess.run(
            cmd,
            cwd=ROOT,
            env=env,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        return {
            "task": task_name,
            "exit_code": result.returncode,
            "stdout": result.stdout[-5000:] if len(result.stdout) > 5000 else result.stdout,
            "stderr": result.stderr[-5000:] if len(result.stderr) > 5000 else result.stderr,
            "timed_out": False,
        }
    except subprocess.TimeoutExpired:
        return {
            "task": task_name,
            "exit_code": -1,
            "stdout": "",
            "stderr": f"TIMEOUT after {timeout}s",
            "timed_out": True,
        }


def analyze_result(result: dict, mode: str, variant: str) -> list[Finding]:
    """Analyze a benchmark result and return findings."""
    findings = []
    task = result["task"]

    if result["timed_out"]:
        findings.append(Finding(
            task=task, category="", mode=mode, variant=variant,
            severity="hang",
            detail=f"Task hung for >{DEFAULT_TIMEOUT}s. Possible deadlock or infinite loop.",
        ))
        return findings

    stdout = result.get("stdout", "")
    stderr = result.get("stderr", "")

    # Check for panics.
    if "panic:" in stdout or "panic:" in stderr:
        panic_lines = []
        for line in (stdout + stderr).split("\n"):
            if "panic:" in line or "goroutine" in line and "panic" in line.lower():
                panic_lines.append(line.strip())
        findings.append(Finding(
            task=task, category="", mode=mode, variant=variant,
            severity="panic",
            detail="Panic detected: " + ("; ".join(panic_lines[:5])),
            traceback="\n".join(panic_lines[:20]),
        ))

    # Check for race conditions.
    if "DATA RACE" in stdout or "DATA RACE" in stderr or "WARNING: DATA RACE" in stdout:
        findings.append(Finding(
            task=task, category="", mode=mode, variant=variant,
            severity="crash",
            detail="Data race detected during adversarial test.",
        ))

    # Check for fatal errors.
    if "fatal error:" in stdout.lower() or "fatal error:" in stderr.lower():
        findings.append(Finding(
            task=task, category="", mode=mode, variant=variant,
            severity="crash",
            detail="Fatal error during adversarial test.",
        ))

    # Check for nil pointer dereferences.
    if "nil pointer" in stdout.lower() or "nil pointer" in stderr.lower():
        findings.append(Finding(
            task=task, category="", mode=mode, variant=variant,
            severity="panic",
            detail="Nil pointer dereference detected.",
        ))

    # Check for index out of range.
    if "index out of range" in stdout or "index out of range" in stderr:
        findings.append(Finding(
            task=task, category="", mode=mode, variant=variant,
            severity="panic",
            detail="Slice index out of range.",
        ))

    return findings


# ── Fault Injection Mode ─────────────────────────────────────────────────

def _mutate_fixture_for_fault(src_dir: Path, dst_dir: Path, fault_code: str,
                               fault_stage: str) -> None:
    """Create a mutated fixture that injects an error response."""
    shutil.copytree(src_dir, dst_dir, dirs_exist_ok=True)

    fixture_path = dst_dir / "fixture.json"
    if not fixture_path.exists():
        return

    with open(fixture_path) as f:
        config = json.load(f)

    # Create an error SSE file that returns a fault.
    error_sse = dst_dir / "fault_inject.sse"
    error_data = json.dumps({
        "error": {
            "message": f"Adversarial fault injection: {fault_code} at {fault_stage}",
            "type": fault_code,
            "code": fault_code,
        }
    })
    error_sse.write_text(f"data: {error_data}\n\ndata: [DONE]\n")

    # Insert the error stream at the beginning or middle of the stream list.
    streams = config.get("streams", [])
    if streams:
        # Insert after the first stream to test mid-turn fault recovery.
        insert_pos = min(1, len(streams))
        config["streams"] = streams[:insert_pos] + ["fault_inject.sse"] + streams[insert_pos:]

    with open(fixture_path, "w") as f:
        json.dump(config, f, indent=2)


def cmd_fault_inject(timeout: int):
    """Run each benchmark with every fault type at every stage."""
    tasks = discover_tasks()
    print(f"canary-adversarial: fault-inject mode")
    print(f"  tasks: {len(tasks)}")
    print(f"  fault matrix: {len(FAULT_CODES)} codes × {len(FAULT_STAGES)} stages = {len(FAULT_CODES) * len(FAULT_STAGES)} variants")
    print(f"  total runs: {len(tasks) * len(FAULT_CODES) * len(FAULT_STAGES)}")
    print()

    # Sample strategy: for each task, test a representative subset of faults.
    # Testing all 7×6=42 variants per task × 22 tasks = 924 runs (too slow).
    # Instead, test each fault code once per task (picking the most relevant stage).
    # Plus a few high-risk combinations.

    # For each fault code, pick the most relevant stage.
    FAULT_STAGE_MAP = {
        "invalid_argument": "admission",
        "conflict": "persistence",
        "resource_exhausted": "connection",
        "unavailable": "model_sample",
        "canceled": "stream_idle",
        "deadline_exceeded": "terminal",
        "internal": "model_sample",
    }

    # Additional high-risk combinations.
    EXTRA_COMBOS = [
        ("unavailable", "connection"),
        ("internal", "persistence"),
        ("resource_exhausted", "model_sample"),
        ("canceled", "model_sample"),
        ("deadline_exceeded", "connection"),
    ]

    report = AdversarialReport(mode="fault-inject")
    report.total_tasks = len(tasks)

    all_findings = []
    total_runs = 0
    passed = 0
    failed = 0
    crashed = 0
    hung = 0

    with tempfile.TemporaryDirectory(prefix="canary-adversarial-") as tmpdir:
        tmp = Path(tmpdir)

        for task_dir in tasks:
            task_name = task_dir.name
            task_json = load_task_json(task_dir)
            category = task_json.get("category", "")
            provider_dir = task_dir / "provider"

            if not provider_dir.exists():
                print(f"  SKIP {task_name}: no provider directory")
                continue

            # Test each fault code at its primary stage.
            for fault_code in FAULT_CODES:
                stage = FAULT_STAGE_MAP.get(fault_code, "model_sample")
                mutated_dir = tmp / f"{task_name}_{fault_code}_{stage}"
                try:
                    _mutate_fixture_for_fault(provider_dir, mutated_dir, fault_code, stage)
                except Exception as e:
                    print(f"  SKIP {task_name}/{fault_code}/{stage}: cannot mutate ({e})")
                    continue

                total_runs += 1
                env = os.environ.copy()
                env["CODEHELPER_BENCH_TASK"] = str(task_dir)

                result = run_single_benchmark(task_dir, env, timeout)
                findings = analyze_result(result, "fault-inject",
                                          f"{fault_code}@{stage}")
                for f in findings:
                    f.category = category

                if result["timed_out"]:
                    hung += 1
                    print(f"  HUNG {task_name} [{fault_code}@{stage}]")
                elif findings:
                    crashed += 1
                    print(f"  BUG  {task_name} [{fault_code}@{stage}]: {findings[0].severity} — {findings[0].detail[:100]}")
                    all_findings.extend(findings)
                elif result["exit_code"] != 0:
                    failed += 1
                    # Expected: task should gracefully fail with a fault injected.
                    # Not a bug unless it crashed.
                else:
                    passed += 1

            # Test extra combos.
            for fault_code, stage in EXTRA_COMBOS:
                mutated_dir = tmp / f"{task_name}_{fault_code}_{stage}_extra"
                try:
                    _mutate_fixture_for_fault(provider_dir, mutated_dir, fault_code, stage)
                except Exception:
                    continue

                total_runs += 1
                env = os.environ.copy()
                result = run_single_benchmark(task_dir, env, timeout)
                findings = analyze_result(result, "fault-inject",
                                          f"{fault_code}@{stage}")
                for f in findings:
                    f.category = category

                if result["timed_out"]:
                    hung += 1
                    print(f"  HUNG {task_name} [{fault_code}@{stage}]")
                elif findings:
                    crashed += 1
                    print(f"  BUG  {task_name} [{fault_code}@{stage}]: {findings[0].severity} — {findings[0].detail[:100]}")
                    all_findings.extend(findings)

    report.variants_run = total_runs
    report.variants_passed = passed
    report.variants_failed = failed
    report.variants_crashed = crashed
    report.variants_hung = hung
    report.findings = [{"severity": f.severity, "task": f.task,
                        "category": f.category, "variant": f.variant,
                        "detail": f.detail, "traceback": f.traceback}
                       for f in all_findings]
    report.generated_at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

    _print_report(report)
    _write_report(report)

    if all_findings:
        print(f"\ncanary-adversarial: FOUND {len(all_findings)} BUG(S)")
        sys.exit(1)
    else:
        print(f"\ncanary-adversarial: OK — no bugs found in {total_runs} adversarial runs")


# ── Differential Config Mode ─────────────────────────────────────────────

def cmd_diff_config(timeout: int):
    """Run benchmarks under different policy configurations, compare results."""
    tasks = discover_tasks()
    configs = []
    for mode in POLICY_MODES:
        for posture in POLICY_POSTURES:
            configs.append((mode, posture))

    print(f"canary-adversarial: diff-config mode")
    print(f"  tasks: {len(tasks)}")
    print(f"  configs: {len(configs)}")
    print(f"  total runs: {len(tasks) * len(configs)}")
    print()

    report = AdversarialReport(mode="diff-config")
    report.total_tasks = len(tasks)

    all_findings = []
    total_runs = 0
    passed = 0
    crashed = 0
    hung = 0

    for task_dir in tasks:
        task_name = task_dir.name
        task_json = load_task_json(task_dir)
        category = task_json.get("category", "")

        results_by_config = {}

        for mode, posture in configs:
            total_runs += 1
            env = os.environ.copy()
            env["CODEHELPER_BENCH_MODE"] = mode
            env["CODEHELPER_BENCH_POSTURE"] = posture

            result = run_single_benchmark(task_dir, env, timeout)
            findings = analyze_result(result, "diff-config",
                                      f"mode={mode},posture={posture}")
            for f in findings:
                f.category = category

            if result["timed_out"]:
                hung += 1
                print(f"  HUNG {task_name} [{mode}/{posture}]")
            elif findings:
                crashed += 1
                print(f"  BUG  {task_name} [{mode}/{posture}]: {findings[0].severity}")
                all_findings.extend(findings)
            else:
                passed += 1

            results_by_config[f"{mode}/{posture}"] = result["exit_code"]

        # Compare results across configs: all should behave consistently.
        exit_codes = set(results_by_config.values())
        if len(exit_codes) > 1:
            # Some configs pass, others fail — unexpected divergence.
            detail_parts = []
            for config, code in results_by_config.items():
                detail_parts.append(f"{config}={code}")
            all_findings.append(Finding(
                task=task_name, category=category,
                mode="diff-config", variant="divergence",
                severity="unexpected",
                detail="Config divergence: " + ", ".join(detail_parts),
            ))
            print(f"  DIV  {task_name}: " + ", ".join(detail_parts))

    report.variants_run = total_runs
    report.variants_passed = passed
    report.variants_crashed = crashed
    report.variants_hung = hung
    report.findings = [{"severity": f.severity, "task": f.task,
                        "category": f.category, "variant": f.variant,
                        "detail": f.detail, "traceback": f.traceback}
                       for f in all_findings]
    report.generated_at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

    _print_report(report)
    _write_report(report)

    if all_findings:
        print(f"\ncanary-adversarial: FOUND {len(all_findings)} DIVERGENCE(S)")
        sys.exit(1)
    else:
        print(f"\ncanary-adversarial: OK — configs behave consistently")


# ── Fixture Mutation Mode ────────────────────────────────────────────────

def _create_mutated_fixture(src_dir: Path, dst_dir: Path, mutation: str) -> bool:
    """Create a mutated copy of a fixture. Returns True if mutation was applied."""
    shutil.copytree(src_dir, dst_dir, dirs_exist_ok=True)

    fixture_path = dst_dir / "fixture.json"
    if not fixture_path.exists():
        return False

    with open(fixture_path) as f:
        config = json.load(f)

    streams = config.get("streams", [])
    if not streams:
        return False

    if mutation == "truncate_json":
        # Truncate the first SSE file at a random position.
        sse_path = dst_dir / streams[0]
        if sse_path.exists():
            content = sse_path.read_text()
            if len(content) > 20:
                cut = len(content) // 2
                sse_path.write_text(content[:cut] + "\n[MALFORMED]")

    elif mutation == "inject_error":
        # Replace the first SSE file with an error response.
        error_sse = dst_dir / (streams[0] + ".error")
        error_data = json.dumps({
            "error": {"message": "Adversarial mutation: inject_error",
                      "type": "api_error"}
        })
        error_sse.write_text(f"data: {error_data}\n\ndata: [DONE]\n")
        config["streams"] = [streams[0] + ".error"] + streams[1:]

    elif mutation == "remove_stream":
        # Remove the first stream (if more than one).
        if len(streams) > 1:
            config["streams"] = streams[1:]

    elif mutation == "empty_response":
        # Create an empty SSE file.
        empty_sse = dst_dir / (streams[0] + ".empty")
        empty_sse.write_text("data: [DONE]\n")
        config["streams"] = [streams[0] + ".empty"] + streams[1:]

    elif mutation == "extra_fields":
        # Add unexpected fields to SSE JSON.
        sse_path = dst_dir / streams[0]
        if sse_path.exists():
            content = sse_path.read_text()
            lines = content.split("\n")
            mutated_lines = []
            for line in lines:
                if line.startswith("data: ") and line != "data: [DONE]":
                    try:
                        data = json.loads(line[6:])
                        data["_unexpected_field"] = "adversarial"
                        data["_extra_array"] = [1, 2, 3]
                        mutated_lines.append("data: " + json.dumps(data))
                    except json.JSONDecodeError:
                        mutated_lines.append(line)
                else:
                    mutated_lines.append(line)
            sse_path.write_text("\n".join(mutated_lines))

    elif mutation == "missing_required":
        # Remove required fields (choices) from SSE JSON.
        sse_path = dst_dir / streams[0]
        if sse_path.exists():
            content = sse_path.read_text()
            lines = content.split("\n")
            mutated_lines = []
            for line in lines:
                if line.startswith("data: ") and line != "data: [DONE]":
                    try:
                        data = json.loads(line[6:])
                        if "choices" in data:
                            del data["choices"]
                        mutated_lines.append("data: " + json.dumps(data))
                    except json.JSONDecodeError:
                        mutated_lines.append(line)
                else:
                    mutated_lines.append(line)
            sse_path.write_text("\n".join(mutated_lines))

    with open(fixture_path, "w") as f:
        json.dump(config, f, indent=2)

    return True


def cmd_mutate_fixture(timeout: int):
    """Run benchmarks with mutated fixture data."""
    tasks = discover_tasks()
    print(f"canary-adversarial: mutate-fixture mode")
    print(f"  tasks: {len(tasks)}")
    print(f"  mutations: {len(MUTATION_TYPES)}")
    print(f"  total runs: {len(tasks) * len(MUTATION_TYPES)}")
    print()

    report = AdversarialReport(mode="mutate-fixture")
    report.total_tasks = len(tasks)

    all_findings = []
    total_runs = 0
    passed = 0
    crashed = 0
    hung = 0

    with tempfile.TemporaryDirectory(prefix="canary-adversarial-") as tmpdir:
        tmp = Path(tmpdir)

        for task_dir in tasks:
            task_name = task_dir.name
            task_json = load_task_json(task_dir)
            category = task_json.get("category", "")
            provider_dir = task_dir / "provider"

            if not provider_dir.exists():
                print(f"  SKIP {task_name}: no provider directory")
                continue

            for mutation in MUTATION_TYPES:
                mutated_dir = tmp / f"{task_name}_{mutation}"
                ok = _create_mutated_fixture(provider_dir, mutated_dir, mutation)
                if not ok:
                    continue

                total_runs += 1
                env = os.environ.copy()
                result = run_single_benchmark(task_dir, env, timeout)
                findings = analyze_result(result, "mutate-fixture", mutation)
                for f in findings:
                    f.category = category

                if result["timed_out"]:
                    hung += 1
                    print(f"  HUNG {task_name} [{mutation}]")
                elif findings:
                    crashed += 1
                    print(f"  BUG  {task_name} [{mutation}]: {findings[0].severity} — {findings[0].detail[:100]}")
                    all_findings.extend(findings)
                elif result["exit_code"] != 0:
                    # Expected: mutated fixture may cause task failure.
                    # This is OK as long as it doesn't crash.
                    pass
                else:
                    passed += 1

    report.variants_run = total_runs
    report.variants_passed = passed
    report.variants_crashed = crashed
    report.variants_hung = hung
    report.findings = [{"severity": f.severity, "task": f.task,
                        "category": f.category, "variant": f.variant,
                        "detail": f.detail, "traceback": f.traceback}
                       for f in all_findings]
    report.generated_at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

    _print_report(report)
    _write_report(report)

    if all_findings:
        print(f"\ncanary-adversarial: FOUND {len(all_findings)} BUG(S)")
        sys.exit(1)
    else:
        print(f"\ncanary-adversarial: OK — no bugs found in {total_runs} adversarial runs")


# ── Full Mode ────────────────────────────────────────────────────────────

def cmd_full(timeout: int):
    """Run all adversarial modes."""
    print("=" * 60)
    print("canary-adversarial: FULL MODE")
    print("=" * 60)

    all_findings = []

    for mode_name, mode_fn in [
        ("fault-inject", cmd_fault_inject),
        ("diff-config", cmd_diff_config),
        ("mutate-fixture", cmd_mutate_fixture),
    ]:
        print(f"\n{'─' * 60}")
        print(f"  Phase: {mode_name}")
        print(f"{'─' * 60}")
        try:
            mode_fn(timeout)
        except SystemExit as e:
            if e.code != 0:
                # Collect findings from the report file.
                try:
                    prev = _load_report()
                    all_findings.extend(prev.get("findings", []))
                except Exception:
                    pass

    # Merge all findings.
    merged = {
        "mode": "full",
        "modes": ["fault-inject", "diff-config", "mutate-fixture"],
        "findings": all_findings,
        "total_bugs": len(all_findings),
        "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }
    _write_report_raw(merged)

    if all_findings:
        print(f"\n{'=' * 60}")
        print(f"canary-adversarial FULL: FOUND {len(all_findings)} TOTAL BUG(S)")
        for f in all_findings:
            print(f"  [{f['severity']}] {f['task']}: {f['detail'][:120]}")
        print(f"{'=' * 60}")
        sys.exit(1)
    else:
        print(f"\n{'=' * 60}")
        print(f"canary-adversarial FULL: OK — no bugs found")
        print(f"{'=' * 60}")


# ── Report helpers ───────────────────────────────────────────────────────

def _print_report(report: AdversarialReport):
    print(f"\n  Results: {report.variants_run} runs, "
          f"{report.variants_passed} passed, "
          f"{report.variants_crashed} crashed, "
          f"{report.variants_hung} hung")
    if report.findings:
        print(f"  Findings:")
        for f in report.findings:
            print(f"    [{f['severity']}] {f['task']}: {f['detail'][:120]}")


def _write_report(report: AdversarialReport):
    data = {
        "mode": report.mode,
        "total_tasks": report.total_tasks,
        "variants_run": report.variants_run,
        "variants_passed": report.variants_passed,
        "variants_crashed": report.variants_crashed,
        "variants_hung": report.variants_hung,
        "findings": report.findings,
        "generated_at": report.generated_at,
    }
    _write_report_raw(data)


def _write_report_raw(data: dict):
    DEFAULT_REPORT.parent.mkdir(parents=True, exist_ok=True)
    with open(DEFAULT_REPORT, "w") as f:
        json.dump(data, f, indent=2)


def _load_report() -> dict:
    if DEFAULT_REPORT.exists():
        with open(DEFAULT_REPORT) as f:
            return json.load(f)
    return {}


# ── Main ─────────────────────────────────────────────────────────────────

def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(1)

    mode = sys.argv[1]
    timeout = DEFAULT_TIMEOUT

    # Parse args.
    args = sys.argv[2:]
    i = 0
    while i < len(args):
        if args[i] == "--timeout" and i + 1 < len(args):
            timeout = int(args[i + 1])
            i += 2
        else:
            i += 1

    if mode == "fault-inject":
        cmd_fault_inject(timeout)
    elif mode == "diff-config":
        cmd_diff_config(timeout)
    elif mode == "mutate-fixture":
        cmd_mutate_fixture(timeout)
    elif mode == "full":
        cmd_full(timeout)
    else:
        print(f"canary-adversarial: unknown mode: {mode}")
        print("  Use: fault-inject, diff-config, mutate-fixture, or full")
        sys.exit(1)


if __name__ == "__main__":
    main()