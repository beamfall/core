#!/usr/bin/env python3
"""APPLE-7 iOS UI-catalog accessibility release-gate validator."""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable


KNOWN_A11Y = {"default", "large-text", "reduce-motion", "high-contrast", "text-expansion", "voiceover"}


@dataclass(frozen=True)
class Requirement:
    surface: str
    screen: str
    state: str
    a11y: str
    assertion_ids: tuple[str, ...] = ()

    @property
    def is_visual(self) -> bool:
        return self.a11y != "voiceover"

    def key(self) -> tuple[str, str, str, str]:
        return (self.surface, self.screen, self.state, self.a11y)

    def display_key(self) -> str:
        return "/".join(self.key())


def visual(surface: str, screen: str, state: str) -> tuple[Requirement, ...]:
    return tuple(Requirement(surface, screen, state, a11y) for a11y in ("large-text", "reduce-motion", "high-contrast"))


APPLE7_REQUIREMENTS: tuple[Requirement, ...] = (
    *visual("iphone", "browse", "populated"),
    *visual("iphone", "player", "transport-overlay"),
    *visual("ipad", "manage.browse", "populated"),
    Requirement(
        "iphone",
        "browse",
        "populated",
        "voiceover",
        ("sections-list-header-semantics", "cards-stable-accessibility-labels", "decorative-artwork-hidden"),
    ),
    Requirement(
        "iphone",
        "player",
        "transport-overlay",
        "voiceover",
        (
            "transport-scrubber-adjustable",
            "transport-scrubber-spoken-position",
            "transport-menu-labels",
            "track-menu-labels",
            "track-menu-focus-order",
            "skip-actions-announce",
            "route-changes-announce",
            "terminal-overlays-combined",
            "up-next-countdown-announced",
            "overlays-do-not-trap-voiceover",
        ),
    ),
    Requirement(
        "ipad",
        "manage.browse",
        "populated",
        "voiceover",
        (
            "manager-navigation-headers",
            "manager-section-headers",
            "manager-trash-header",
            "manager-artwork-picker-labels",
            "manager-library-health-headers",
            "manager-console-headers",
            "app-store-acquisition-vocabulary-absent",
        ),
    ),
)


def load_json(path: Path) -> Any:
    with path.open("r", encoding="utf-8") as fh:
        return json.load(fh)


def as_rows(data: Any, path: Path) -> list[dict[str, Any]]:
    if isinstance(data, list):
        rows = data
    elif isinstance(data, dict):
        rows = data.get("entries") or data.get("rows") or data.get("evidence") or data.get("voiceover") or []
    else:
        raise ValueError(f"{path} must contain a JSON array or object with rows")
    if not isinstance(rows, list) or not all(isinstance(row, dict) for row in rows):
        raise ValueError(f"{path} rows must be JSON objects")
    return rows


def artifact_exists(repo_root: Path, rel_path: Any) -> bool:
    if not isinstance(rel_path, str) or not rel_path:
        return False
    path = Path(rel_path)
    if path.is_absolute() or ".." in path.parts:
        return False
    return (repo_root / path).is_file()


def row_key(row: dict[str, Any]) -> tuple[str, str, str, str]:
    return (
        str(row.get("surface", "")),
        str(row.get("screen", "")),
        str(row.get("state", "")),
        str(row.get("a11y", row.get("variant", ""))),
    )


def evidence_assertion_ids(row: dict[str, Any]) -> set[str]:
    ids: set[str] = set()
    for scalar_key in ("assertionId", "assertion_id", "assertion", "id"):
        value = row.get(scalar_key)
        if isinstance(value, str) and value:
            ids.add(value)
    for list_key in ("assertionIds", "assertion_ids", "assertions"):
        value = row.get(list_key)
        if isinstance(value, list):
            ids.update(item for item in value if isinstance(item, str) and item)
    return ids


def evidence_passed(row: dict[str, Any]) -> bool:
    value = row.get("result", row.get("status", row.get("outcome")))
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        return value.lower() == "pass"
    return False


def passed_evidence_ids(evidence_rows: Iterable[dict[str, Any]], requirement: Requirement) -> set[str]:
    passed: set[str] = set()
    for row in evidence_rows:
        if row_key(row) != requirement.key() or not evidence_passed(row):
            continue
        passed.update(evidence_assertion_ids(row))
    return passed


def validate(*, manifest_path: Path, repo_root: Path, voiceover_evidence_path: Path) -> list[str]:
    errors: list[str] = []
    manifest_rows = as_rows(load_json(manifest_path), manifest_path)
    evidence_rows = as_rows(load_json(voiceover_evidence_path), voiceover_evidence_path)

    by_key: dict[tuple[str, str, str, str], dict[str, Any]] = {}
    for index, row in enumerate(manifest_rows):
        a11y = row.get("a11y")
        if a11y not in KNOWN_A11Y:
            errors.append(f"manifest row {index} has unknown a11y value {a11y!r}")
        key = row_key(row)
        if key in by_key:
            errors.append(f"duplicate manifest tuple {'/'.join(key)}")
        by_key[key] = row

    for index, row in enumerate(evidence_rows):
        a11y = row.get("a11y", row.get("variant"))
        if a11y not in KNOWN_A11Y:
            errors.append(f"voiceover evidence row {index} has unknown a11y value {a11y!r}")

    for requirement in APPLE7_REQUIREMENTS:
        row = by_key.get(requirement.key())
        if row is None:
            errors.append(f"missing required APPLE-7 tuple {requirement.display_key()}")
            continue
        if row.get("status") != "captured":
            errors.append(f"{requirement.display_key()} must have status captured, got {row.get('status')!r}")
        if not artifact_exists(repo_root, row.get("hierarchy")):
            errors.append(f"{requirement.display_key()} hierarchy missing: {row.get('hierarchy')!r}")
        if requirement.is_visual:
            if not artifact_exists(repo_root, row.get("screenshot")):
                errors.append(f"{requirement.display_key()} screenshot missing: {row.get('screenshot')!r}")
            if not artifact_exists(repo_root, row.get("baseline")):
                errors.append(f"{requirement.display_key()} baseline missing: {row.get('baseline')!r}")
        else:
            passed = passed_evidence_ids(evidence_rows, requirement)
            for assertion_id in requirement.assertion_ids:
                if assertion_id not in passed:
                    errors.append(f"{requirement.display_key()} missing VoiceOver assertion {assertion_id!r} with result pass")

    return errors


def run_self_test() -> int:
    return subprocess.call([sys.executable, str(Path(__file__).with_name("apple_a11y_check_test.py"))])


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, required=False)
    parser.add_argument("--repo-root", type=Path, required=False)
    parser.add_argument("--voiceover-evidence", type=Path, required=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument(
        "--allow-missing-repos",
        action="store_true",
        help=(
            "When the Apple catalog repo root directory does not exist, print a documented blocker "
            "to stderr and return 0 rather than failing. Only errors on artifact gaps in repos that "
            "ARE present. Use when sibling repos are not checked out (e.g. in core-only CI)."
        ),
    )
    args = parser.parse_args(argv)
    if args.self_test:
        return args
    if not args.allow_missing_repos:
        missing = [name for name in ("manifest", "repo_root", "voiceover_evidence") if getattr(args, name) is None]
        if missing:
            parser.error("required unless --self-test or --allow-missing-repos: " + ", ".join("--" + name.replace("_", "-") for name in missing))
    return args


def main(argv: list[str] | None = None) -> int:
    args = parse_args(list(sys.argv[1:] if argv is None else argv))
    if args.self_test:
        return run_self_test()

    # When --allow-missing-repos is set, check whether the repo root actually exists
    # before attempting to open any paths. This allows core-only CI to skip the Apple
    # a11y gate gracefully rather than failing on a missing sibling checkout.
    if args.allow_missing_repos:
        # Determine the repo root: prefer --repo-root if given, fall back to parent of --manifest.
        repo_root = args.repo_root
        if repo_root is None and args.manifest is not None:
            repo_root = args.manifest.parent.parent  # ui-catalog/manifest.json → ui-catalog → repo
        if repo_root is None or not repo_root.is_dir():
            print(
                f"APPLE-7 a11y check: BLOCKER: Apple catalog repo not present at "
                f"{repo_root} — APPLE-7 a11y gate NOT verified",
                file=sys.stderr,
            )
            print("APPLE-7 a11y check: PARTIAL (repo missing, see blocker above)")
            return 0

    errors = validate(
        manifest_path=args.manifest,
        repo_root=args.repo_root,
        voiceover_evidence_path=args.voiceover_evidence,
    )
    if errors:
        for error in errors:
            print(f"APPLE-7 a11y check: {error}", file=sys.stderr)
        return 1
    print("APPLE-7 a11y check: OK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
