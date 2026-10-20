#!/usr/bin/env python3
"""Validate native UI-catalog evidence packets for release review."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any


REQUIRED_SURFACES = {
    "apple": ("iphone", "ipad", "tvos", "macos"),
    "android": ("androidtv", "androidmobile"),
}


def load_rows(path: Path) -> list[dict[str, Any]]:
    with path.open("r", encoding="utf-8") as fh:
        data = json.load(fh)
    if isinstance(data, dict):
        data = data.get("entries", [])
    if not isinstance(data, list) or not all(isinstance(row, dict) for row in data):
        raise ValueError(f"{path} must contain a JSON array or an object with entries")
    return data


def artifact_exists(root: Path, rel_path: Any) -> bool:
    if not isinstance(rel_path, str) or not rel_path:
        return False
    path = Path(rel_path)
    if path.is_absolute() or ".." in path.parts:
        return False
    return (root / path).is_file()


def validate_catalog(label: str, root: Path, required_surfaces: tuple[str, ...]) -> list[str]:
    errors: list[str] = []
    manifest = root / "manifest.json"
    if not manifest.is_file():
        return [f"{label}: missing manifest {manifest}"]
    try:
        rows = load_rows(manifest)
    except (OSError, ValueError, json.JSONDecodeError) as err:
        return [f"{label}: {err}"]

    captured_by_surface = {surface: 0 for surface in required_surfaces}
    for index, row in enumerate(rows):
        surface = str(row.get("surface", ""))
        if row.get("status") != "captured":
            continue
        if surface in captured_by_surface:
            captured_by_surface[surface] += 1
        for key in ("screenshot", "hierarchy", "baseline"):
            if not artifact_exists(root, row.get(key)):
                errors.append(f"{label}: row {index} {row.get('id', '<missing-id>')} missing {key}: {row.get(key)!r}")

    for surface, count in captured_by_surface.items():
        if count == 0:
            errors.append(f"{label}: no captured rows for required surface {surface}")
    return errors


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--apple-catalog", type=Path, default=Path("../beamfall-apple/ui-catalog"))
    parser.add_argument("--android-catalog", type=Path, default=Path("../beamfall-android/ui-catalog"))
    parser.add_argument(
        "--allow-missing-repos",
        action="store_true",
        help=(
            "When a sibling catalog repo directory does not exist, print a documented blocker to "
            "stderr and skip it rather than failing. Only errors on artifact gaps in repos that "
            "ARE present. Use when sibling repos are not checked out (e.g. in core-only CI)."
        ),
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(list(sys.argv[1:] if argv is None else argv))
    errors: list[str] = []
    blockers: list[str] = []

    for label, catalog_path, surfaces in [
        ("apple", args.apple_catalog, REQUIRED_SURFACES["apple"]),
        ("android", args.android_catalog, REQUIRED_SURFACES["android"]),
    ]:
        if not catalog_path.is_dir():
            if args.allow_missing_repos:
                blockers.append(f"BLOCKER: {label} catalog repo not present at {catalog_path} — native evidence NOT verified")
                continue
            else:
                errors.append(f"{label}: catalog repo directory not found: {catalog_path}")
                continue
        errors.extend(validate_catalog(label, catalog_path, surfaces))

    for blocker in blockers:
        print(f"native evidence check: {blocker}", file=sys.stderr)
    if errors:
        for error in errors:
            print(f"native evidence check: {error}", file=sys.stderr)
        return 1
    if blockers:
        print("native evidence check: PARTIAL (some repos missing, see blockers above)")
    else:
        print("native evidence check: OK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
