#!/usr/bin/env python3
"""GA hardware HDR tone-mapping launch gate (ADR-0075 Amendment 2026-07-26 D6).

The 2026-07-25 GA floor requires BOTH a passing VideoToolbox hardware HDR
tone-mapping row AND a passing row from one of VAAPI / QSV / CUDA-NVENC, for
every transfer class being claimed. This validator is what makes that floor
enforceable rather than aspirational.

Everything here fails CLOSED. There is no "unknown => pass" branch, and the gate
never mutates or regenerates the evidence it validates.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path

SCHEMA_VERSION = 1

# The absolute constants are an inviolable lower bound on BOTH dimensions
# (internal/ffmpeg/tonemap.go). Calibration may only ever RAISE a floor.
MIN_HIGHLIGHT_DROP = 32
MIN_HIGHLIGHT = 64

SERVER_BACKENDS = ("vaapi", "qsv", "nvenc")
VIDEOTOOLBOX = "videotoolbox"
# GA_PRIMARY_SERVER_BACKEND is unset: no backend is presumed, and no backend's
# absence is excused. The disjunction must be satisfied by a real passing row.
DEFAULT_CLAIMED_CLASSES = ("pq", "hlg")

PASSTHROUGH_REQUIRED = "failed-as-required"
ORIGINS = ("inherited", "calibrated")

# The closed failure vocabulary (D7): the seven existing ToneMapFailure values
# plus `unavailable`, which reuses the HWAccelStatus spelling.
FAILURES = (
    "",
    "binary-missing",
    "chain-unrunnable",
    "timeout",
    "no-frame",
    "inconclusive",
    "no-transform",
    "crushed",
    "unavailable",
)

# D5's certification scope: exactly the paths that can change what the probe
# MEASURES. Orchestration that merely invokes it is excluded on purpose —
# including `tools/gate/**` or `Makefile` would invalidate every row almost
# continuously and force a two-host physical re-probe for unrelated work.
SCOPE_DIR_PREFIXES = ("internal/ffmpeg/",)
SCOPE_EXACT_PATHS = (
    "internal/playback/playback.go",
    "tools/gate/ffmpeg-tonemap-conforming.sh",
    "Dockerfile",
)
# Test files are excluded by the set's own criterion: they cannot change what
# the probe measures. testdata/ffmpeg-pin-filters.txt deliberately STAYS in —
# it is data, not a test file, and a change to it reflects a change in what the
# pinned build accepts, which absolutely can move a verdict.
SCOPE_EXCLUDE_SUFFIX = "_test.go"

SHA256_RE = re.compile(r"^[0-9a-f]{64}$")


class Red(Exception):
    """A gate refusal. Every path out of this module that is not GREEN is one."""


def in_scope(path: str) -> bool:
    if path.endswith(SCOPE_EXCLUDE_SUFFIX):
        return False
    if path in SCOPE_EXACT_PATHS:
        return True
    return any(path.startswith(prefix) for prefix in SCOPE_DIR_PREFIXES)


def certification_scope_digest(root: Path, rev: str) -> str:
    """sha256 over the `git ls-tree -r <rev>` lines for exactly the D5 path set,
    sorted by path, one line each, newline-separated."""
    try:
        listing = subprocess.run(
            ["git", "-C", str(root), "ls-tree", "-r", rev],
            capture_output=True, text=True, check=True,
        ).stdout
    except (OSError, subprocess.CalledProcessError) as err:
        raise Red(f"cannot read the tree at {rev}: {err}") from err

    selected = []
    for line in listing.splitlines():
        if "\t" not in line:
            continue
        path = line.split("\t", 1)[1]
        if in_scope(path):
            selected.append((path, line))
    if not selected:
        raise Red(f"the certification scope matched no files at {rev}; the digest would be vacuous")
    selected.sort(key=lambda entry: entry[0])
    body = "\n".join(line for _, line in selected) + "\n"
    return hashlib.sha256(body.encode("utf-8")).hexdigest()


def locked_ffmpeg_identities(root: Path) -> set[str]:
    """The Linux pin identities: the artifact SHA-256s ffmpeg.lock authenticates."""
    lock = root / "internal" / "ffmpeg" / "ffmpeg.lock"
    try:
        text = lock.read_text(encoding="utf-8")
    except OSError as err:
        raise Red(f"cannot read {lock}: {err}") from err
    return set(re.findall(r"^FFMPEG_LINUX_\w+_SHA256=([0-9a-f]{64})$", text, re.M))


def load_evidence(path: Path) -> dict:
    if not path.is_file():
        raise Red(
            f"no evidence file at {path}. The GA hardware HDR tone-mapping floor is unproven, which "
            "is RED — missing evidence is never an implicit pass."
        )
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as err:
        raise Red(f"{path} is not parseable evidence: {err}") from err
    if not isinstance(payload, dict):
        raise Red(f"{path} is not a JSON object")
    if payload.get("schema_version") != SCHEMA_VERSION:
        raise Red(
            f"{path} declares schema_version={payload.get('schema_version')!r}, want {SCHEMA_VERSION}"
        )
    rows = payload.get("rows")
    if not isinstance(rows, list):
        raise Red(f"{path} has no `rows` list")
    return payload


REQUIRED_KEYS = (
    "host_os", "host_arch", "backend", "transfer_class", "status", "failure",
    "ffmpeg_pin_identity", "ffmpeg_version", "chain_builder_revision", "driver_hint",
    "baseline_highlight", "tonemapped_highlight", "software_ref_highlight",
    "thresholds", "passthrough_control", "probed_at", "approver", "approved_at",
)


def validate_row(row: object, index: int, locked: set[str]) -> dict:
    """Reject a row that cannot be evidence. "Required" means the key is PRESENT,
    so a row that quietly omits a field is rejected rather than defaulted."""
    where = f"rows[{index}]"
    if not isinstance(row, dict):
        raise Red(f"{where} is not an object")
    for key in REQUIRED_KEYS:
        if key not in row:
            raise Red(f"{where} omits required key {key!r}")

    if row["failure"] not in FAILURES:
        raise Red(f"{where} records failure {row['failure']!r}, which is outside the closed vocabulary")

    thresholds = row["thresholds"]
    if not isinstance(thresholds, dict):
        raise Red(f"{where}.thresholds is not an object")
    for key in ("min_highlight_drop", "min_highlight", "origin"):
        if key not in thresholds:
            raise Red(f"{where}.thresholds omits required key {key!r}")
    if thresholds["origin"] not in ORIGINS:
        raise Red(f"{where}.thresholds.origin is {thresholds['origin']!r}, want one of {ORIGINS}")
    if not _is_number(thresholds["min_highlight_drop"]) or not _is_number(thresholds["min_highlight"]):
        raise Red(f"{where}.thresholds floors are not numbers")
    # Calibration tightens; it never loosens. A floor below the absolute
    # constant would be LOOSER than the uncalibrated regime it specialises.
    if thresholds["min_highlight_drop"] < MIN_HIGHLIGHT_DROP:
        raise Red(
            f"{where}.thresholds.min_highlight_drop={thresholds['min_highlight_drop']} is below the "
            f"absolute constant {MIN_HIGHLIGHT_DROP}"
        )
    if thresholds["min_highlight"] < MIN_HIGHLIGHT:
        raise Red(
            f"{where}.thresholds.min_highlight={thresholds['min_highlight']} is below the absolute "
            f"constant {MIN_HIGHLIGHT}"
        )

    if row["backend"] != "":
        validate_hardware_row(row, where, thresholds, locked)
    return row


def validate_hardware_row(row: dict, where: str, thresholds: dict, locked: set[str]) -> None:
    """The uncalibrated-backend-evidence definition, applied. Any of these makes
    the row invalid rather than merely unpersuasive."""
    # The pass-through control is mandatory for EVERY hardware row, calibrated
    # or inherited, and `not-applicable` no longer exists in this vocabulary.
    if row["passthrough_control"] != PASSTHROUGH_REQUIRED:
        raise Red(
            f"{where}.passthrough_control={row['passthrough_control']!r}, want {PASSTHROUGH_REQUIRED!r}: "
            "the control is what proves the measured drop came from the tone-map filter rather than "
            "from the device round-trip around it"
        )

    reference = row["software_ref_highlight"]
    if thresholds["origin"] == "calibrated":
        # A calibrated row with no valid software reference leg behind it IS
        # uncalibrated-backend evidence.
        if not _is_number(reference):
            raise Red(
                f"{where} records origin=calibrated with software_ref_highlight={reference!r}: a "
                "calibration with no software reference leg is uncalibrated-backend evidence"
            )
        if reference < MIN_HIGHLIGHT:
            raise Red(
                f"{where} calibrated against a software reference of {reference} which does not itself "
                f"pass the absolute threshold {MIN_HIGHLIGHT}; the run is invalid and produces no row"
            )
    elif reference is not None:
        # `null` is the recorded value on an inherited row, not a missing key.
        raise Red(
            f"{where} records origin=inherited with software_ref_highlight={reference!r}; an inherited "
            "row measures no software reference leg and must record null"
        )

    identity = row["ffmpeg_pin_identity"]
    if not isinstance(identity, str) or not SHA256_RE.match(identity):
        raise Red(f"{where}.ffmpeg_pin_identity is not a sha256 hex digest")
    if row["host_os"] == "linux" and identity not in locked:
        raise Red(
            f"{where} is a linux row whose ffmpeg_pin_identity is not an artifact ffmpeg.lock "
            "authenticates, so it was not produced by the pinned build"
        )


def _is_number(value: object) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def enforce_calibration_ratchet(rows: list[dict]) -> None:
    """A calibrated floor may be RAISED by re-calibration and may never be
    lowered — not below the absolute constants, and not below a previously
    recorded calibrated value for the same backend and class."""
    highest: dict[tuple[str, str], tuple[float, float]] = {}
    for row in rows:
        if row["thresholds"]["origin"] != "calibrated":
            continue
        key = (row["backend"], row["transfer_class"])
        drop = row["thresholds"]["min_highlight_drop"]
        highlight = row["thresholds"]["min_highlight"]
        previous = highest.get(key)
        if previous is not None:
            highest[key] = (max(previous[0], drop), max(previous[1], highlight))
        else:
            highest[key] = (drop, highlight)
    for row in rows:
        if row["thresholds"]["origin"] != "calibrated":
            continue
        key = (row["backend"], row["transfer_class"])
        drop, highlight = highest[key]
        if row["thresholds"]["min_highlight_drop"] < drop or row["thresholds"]["min_highlight"] < highlight:
            raise Red(
                f"{key[0]}/{key[1]} records a calibrated floor below one already recorded for the same "
                "backend and class; calibration tightens, it never loosens"
            )


def surviving_rows(payload: dict, digest: str, locked: set[str]) -> list[dict]:
    """Validate every row, then discard the stale ones."""
    rows = [validate_row(row, index, locked) for index, row in enumerate(payload["rows"])]
    enforce_calibration_ratchet(rows)
    if payload.get("certification_scope_digest") != digest:
        # Staleness is a mismatch of the digest or the pin identity. There is no
        # age dimension and no grace.
        return []
    return rows


def evaluate(payload: dict, digest: str, locked: set[str], claimed: tuple[str, ...]) -> None:
    rows = surviving_rows(payload, digest, locked)
    if not rows:
        recorded = payload.get("certification_scope_digest")
        if recorded != digest:
            raise Red(
                f"every row is stale: the evidence records certification_scope_digest {recorded!r}, but "
                f"the release under gate is {digest}. Re-probe on real hardware and re-approve."
            )
        raise Red(
            "the evidence carries no rows, so the GA hardware HDR tone-mapping floor is unproven. "
            "This is the expected state until GA_EVIDENCE_APPROVER is assigned and the two-host "
            "physical probe has been run and approved (see artifacts/vidparity-hdr-tonemap/README.md)."
        )

    approved = [
        row for row in rows
        if row["status"] == "working" and isinstance(row["approver"], str) and row["approver"].strip()
    ]
    unapproved = len(rows) - len(approved)

    for transfer_class in claimed:
        for_class = [row for row in approved if row["transfer_class"] == transfer_class]
        backends = {row["backend"] for row in for_class}
        if VIDEOTOOLBOX not in backends:
            raise Red(
                f"transfer class {transfer_class!r} has no approved, passing VideoToolbox row "
                f"(approved backends: {sorted(backends) or 'none'}; {unapproved} row(s) unapproved or "
                "not working). GA_EVIDENCE_APPROVER is unassigned until a named approver signs a real "
                "hardware run."
            )
        if not backends.intersection(SERVER_BACKENDS):
            raise Red(
                f"transfer class {transfer_class!r} has no approved, passing server-backend row from "
                f"{SERVER_BACKENDS} (approved backends: {sorted(backends)}). Single-half evidence never "
                "satisfies the floor."
            )


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=".", help="repository root")
    parser.add_argument("--evidence", default=None, help="path to evidence.json")
    parser.add_argument("--rev", default="HEAD", help="revision the digest is computed over")
    parser.add_argument(
        "--claim", default=",".join(DEFAULT_CLAIMED_CLASSES),
        help="comma-separated transfer classes the release claims hardware tone-mapping for",
    )
    parser.add_argument(
        "--print-digest", action="store_true",
        help="print the certification-scope digest and exit (never mutates evidence)",
    )
    args = parser.parse_args(argv)

    root = Path(args.root).resolve()
    evidence = Path(args.evidence) if args.evidence else root / "artifacts" / "vidparity-hdr-tonemap" / "evidence.json"
    claimed = tuple(part for part in args.claim.split(",") if part)

    try:
        digest = certification_scope_digest(root, args.rev)
        if args.print_digest:
            print(digest)
            return 0
        if not claimed:
            raise Red("--claim named no transfer class, so this gate would assert nothing")
        evaluate(load_evidence(evidence), digest, locked_ffmpeg_identities(root), claimed)
    except Red as refusal:
        print(f"vidparity-hdr-tonemap-gate: RED — {refusal}", file=sys.stderr)
        return 1

    print(
        "vidparity-hdr-tonemap-gate: OK — approved VideoToolbox + server-backend rows for "
        f"{', '.join(claimed)} at scope digest {digest[:12]}"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
