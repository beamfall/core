#!/usr/bin/env python3
"""Run the canonical, time-bounded Gremlins mutation policy."""

from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import time
from typing import Any, Optional


COUNTER_MAP = {
    "mutants_killed": "measured_killed",
    "mutants_lived": "measured_lived",
    "mutants_not_covered": "measured_not_covered",
}


class MutationError(RuntimeError):
    pass


def positive_seconds(name: str, default: str) -> float:
    raw = os.environ.get(name, default)
    try:
        value = float(raw)
    except ValueError as error:
        raise MutationError(f"{name} must be a positive number") from error
    if value <= 0:
        raise MutationError(f"{name} must be a positive number")
    return value


def terminate_tree(process: subprocess.Popen[Any]) -> None:
    if process.poll() is not None:
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        process.wait(timeout=0.5)
        return
    except subprocess.TimeoutExpired:
        pass
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait()


def run_owned(command: list[str], timeout: float) -> tuple[int, str, bool]:
    process = subprocess.Popen(
        command,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        start_new_session=True,
    )
    previous: dict[int, Any] = {}

    def forward(signum: int, _frame: object) -> None:
        try:
            os.killpg(process.pid, signum)
        except ProcessLookupError:
            pass

    for sig in (signal.SIGINT, signal.SIGTERM):
        previous[sig] = signal.signal(sig, forward)
    try:
        try:
            output, _ = process.communicate(timeout=timeout)
            return process.returncode, output, False
        except subprocess.TimeoutExpired:
            terminate_tree(process)
            output, _ = process.communicate()
            return process.returncode, output, True
    finally:
        for sig, handler in previous.items():
            signal.signal(sig, handler)
        terminate_tree(process)


def read_report(path: Path, package: str, budget: dict[str, Any]) -> dict[str, int]:
    if not path.is_file() or path.stat().st_size == 0:
        raise MutationError(f"gremlins produced no output for {package}")
    try:
        report = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise MutationError(f"gremlins produced malformed output for {package}: {error}") from error
    measured: dict[str, int] = {}
    for report_field, policy_field in COUNTER_MAP.items():
        value = report.get(report_field)
        if type(value) is not int or value < 0:
            raise MutationError(f"{package} {report_field} must be a nonnegative integer")
        if value != budget[policy_field]:
            raise MutationError(
                f"{package} {report_field}={value} does not match {policy_field}={budget[policy_field]}"
            )
        measured[report_field] = value
    if measured["mutants_lived"] > budget["max_surviving"]:
        raise MutationError(
            f"{package} mutants_lived={measured['mutants_lived']} exceeds max_surviving={budget['max_surviving']}"
        )
    return measured


def write_status(path: Path, payload: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    temporary.replace(path)


def load_policy_module(root: Path) -> Any:
    spec = importlib.util.spec_from_file_location("mutation_policy", root / "script/mutation-policy.py")
    if spec is None or spec.loader is None:
        raise MutationError("cannot load mutation policy validator")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def source_universe(root: Path, package: str) -> list[str]:
    return sorted(
        path.name
        for path in (root / package[2:]).glob("*.go")
        if not path.name.endswith("_test.go")
    )


def select_targets(packages: dict[str, Any], mode: str) -> list[str]:
    if mode == "named":
        return list(packages)
    package = mode if mode.startswith("./") else "./internal/" + mode
    if package not in packages:
        raise MutationError(f"{package} is not in docs/testing/mutation-budget.json")
    return [package]


def gremlins_command(report_path: Path, package: str, exclusions: list[dict[str, str]]) -> list[str]:
    command = ["gremlins", "unleash", f"--output={report_path}"]
    for exclusion in exclusions:
        command.append("--exclude-files=^" + re.escape(exclusion["path"]) + "$")
    command.append(package)
    return command


def main() -> int:
    root = Path(__file__).resolve().parent.parent
    evidence = Path(os.environ.get("MUTATION_EVIDENCE_DIR", root / ".agent-evidence/mutation-run"))
    status_path = evidence / "status.json"
    started = time.monotonic()
    completed: list[dict[str, Any]] = []
    interrupted: Optional[str] = None
    targets: list[str] = []
    try:
        policy_module = load_policy_module(root)
        policy = policy_module.parse_policy(
            (root / policy_module.POLICY).read_text(encoding="utf-8"), current=True
        )
        policy_module.source_inventory(root, policy)
        tool_version = policy["tool_version"]
        try:
            version_output = subprocess.check_output(["gremlins", "--version"], text=True, stderr=subprocess.STDOUT)
        except (OSError, subprocess.CalledProcessError) as error:
            raise MutationError(f"cannot execute gremlins --version: {error}") from error
        versions = re.findall(r"(?<![0-9])v?(\d+\.\d+\.\d+)(?![0-9])", version_output)
        if versions != [tool_version]:
            raise MutationError(
                f"gremlins version mismatch: policy={tool_version} output={version_output.strip()!r}"
            )
        targets = select_targets(policy["packages"], sys.argv[1] if len(sys.argv) > 1 else "named")
        package_cap = positive_seconds("MUTATION_PKG_CAP_SEC", "600")
        run_cap = positive_seconds("MUTATION_RUN_CAP_SEC", "3300")
        if package_cap >= 3600 or run_cap >= 3600:
            raise MutationError("mutation deadlines must be shorter than the 60-minute workflow deadline")
        with tempfile.TemporaryDirectory(prefix="beamfall-mutation-") as directory:
            reports = Path(directory)
            for package in targets:
                remaining = run_cap - (time.monotonic() - started)
                if remaining <= 0:
                    if not completed:
                        raise MutationError("mutation invocation timed out before any valid package report")
                    interrupted = package
                    break
                report_path = reports / (package.replace("/", "_").replace(".", "_") + ".json")
                exclusions = policy["source_exclusions"][package]
                command = gremlins_command(report_path, package, exclusions)
                before = time.monotonic()
                return_code, output, timed_out = run_owned(command, min(package_cap, remaining))
                elapsed = time.monotonic() - before
                if output:
                    print(output, end="" if output.endswith("\n") else "\n")
                if timed_out:
                    if not completed:
                        raise MutationError(f"{package} timed out before any valid package report")
                    interrupted = package
                    break
                # gremlins exits non-zero when its own thresholds fail; the budget is
                # enforced from the report, so a threshold exit with a valid report
                # must not abort the run. No/malformed report still raises.
                if return_code != 0 and not report_path.is_file():
                    raise MutationError(f"gremlins failed for {package} with exit {return_code}")
                counters = read_report(report_path, package, policy["packages"][package])
                completed.append(
                    {
                        "package": package,
                        "elapsed_seconds": round(elapsed, 6),
                        "counters": counters,
                        "source_universe": source_universe(root, package),
                        "excluded_sources": [entry["path"] for entry in exclusions],
                    }
                )
                test_delay = float(os.environ.get("MUTATION_TEST_INTER_PACKAGE_DELAY_SEC", "0"))
                if test_delay > 0:
                    time.sleep(test_delay)
        status = "TIMEBOXED" if interrupted is not None else "COMPLETE"
        payload = {
            "status": status,
            "tool": "gremlins",
            "tool_version": tool_version,
            "completed": completed,
            "interrupted_package": interrupted,
            "remaining_packages": targets[len(completed) :],
            "elapsed_seconds": round(time.monotonic() - started, 6),
        }
        write_status(status_path, payload)
        print(status)
        return 0
    except (MutationError, OSError, ValueError) as error:
        write_status(
            status_path,
            {
                "status": "FAILED",
                "completed": completed,
                "interrupted_package": interrupted,
                "remaining_packages": targets[len(completed) :],
                "error": str(error),
                "elapsed_seconds": round(time.monotonic() - started, 6),
            },
        )
        print(f"mutation-test: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
