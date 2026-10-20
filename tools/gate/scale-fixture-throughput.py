#!/usr/bin/env python3
"""Validate a cached DSCALE-1 corpus and its raw throughput timings."""

from __future__ import annotations

import argparse
import json
import pathlib
import sys
from typing import Any


OPERATIONS = {
    "scan": ("file_count", "scan_nanoseconds"),
    "search": ("file_count", "search_nanoseconds"),
    "thumbnail": ("thumbnail_probes", "thumbnail_nanoseconds"),
}


def load_json(path: pathlib.Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError(f"cannot read {path}: {error}") from error
    if not isinstance(value, dict):
        raise ValueError(f"{path}: top-level value must be an object")
    return value


def require_equal(label: str, expected: Any, actual: Any) -> None:
    if actual != expected:
        raise ValueError(f"{label}: got {actual!r}, want {expected!r}")


def evaluate(
    recipe_path: pathlib.Path, corpus_path: pathlib.Path, timings_path: pathlib.Path
) -> list[str]:
    recipe = load_json(recipe_path)
    manifest = load_json(corpus_path / "manifest.json")
    timings = load_json(timings_path)
    if not (corpus_path / "library").is_dir():
        raise ValueError(f"cached corpus has no library directory: {corpus_path}")

    for key in ("generator_version", "seed", "tier", "profile"):
        expected = recipe.get(key)
        require_equal(f"manifest {key}", expected, manifest.get(key))
        require_equal(f"timings {key}", expected, timings.get(key))
    require_equal("timings file_count", manifest.get("file_count"), timings.get("file_count"))

    thresholds = recipe.get("minimum_throughput_per_second")
    if not isinstance(thresholds, dict):
        raise ValueError("recipe minimum_throughput_per_second must be an object")

    failures: list[str] = []
    for operation, (count_key, duration_key) in OPERATIONS.items():
        count = timings.get(count_key)
        duration = timings.get(duration_key)
        floor = thresholds.get(operation)
        if not isinstance(count, int) or count < 0:
            raise ValueError(f"timings {count_key} must be a non-negative integer")
        if not isinstance(duration, int) or duration <= 0:
            raise ValueError(f"timings {duration_key} must be a positive integer")
        if not isinstance(floor, (int, float)) or floor <= 0:
            raise ValueError(f"recipe {operation} floor must be positive")
        throughput = count * 1_000_000_000 / duration
        print(f"{operation}: {throughput:.1f}/s (floor {floor:.1f}/s)")
        if throughput < floor:
            failures.append(
                f"{operation} throughput {throughput:.1f}/s is below {floor:.1f}/s"
            )
    return failures


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--recipe", required=True, type=pathlib.Path)
    parser.add_argument("--corpus", required=True, type=pathlib.Path)
    parser.add_argument("--timings", required=True, type=pathlib.Path)
    args = parser.parse_args()
    try:
        failures = evaluate(args.recipe, args.corpus, args.timings)
    except ValueError as error:
        print(f"scale-fixture-throughput: ERROR: {error}", file=sys.stderr)
        return 2
    if failures:
        for failure in failures:
            print(f"scale-fixture-throughput: FAIL: {failure}", file=sys.stderr)
        return 1
    print("scale-fixture-throughput: PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
