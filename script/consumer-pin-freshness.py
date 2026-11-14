#!/usr/bin/env python3
"""Producer-side freshness check for consumer-vendored Core contract artifacts.

A Core wire-contract change carries more than one re-vendor obligation. Each
consumer repo keeps its own copy of a Core producer artifact and asserts it with
its own drift test, so a Core change that lands without refreshing every copy
turns into a red gate in an unrelated repo, on a diff that repo did not author.

This check runs on the PRODUCER side, before the Core change lands: it compares
every registered consumer-vendored artifact against what Core carries now and
names each stale one, its consumer repo, and the command that refreshes it.

Registry: script/consumer-pin-obligations.json
Consumer repos and their obligation IDs are enumerated from workspace-repos.json.
The obligation registry contains artifact metadata and assertion probes only;
no consumer repo name is hardcoded in the checker or obligation registry.

Policy (mirrors BEAMFALL_REQUIRE_CORE on the consumer side, with the roles
inverted -- here the producer is present and a consumer may be absent):
  * stale artifact          -> hard failure, always
  * consumer ahead          -> advisory (CORE-1442): the consumer's copy is a
                               pending core publication on another ref, this
                               branch leaves the producer artifact unchanged,
                               and the window is younger than
                               BEAMFALL_PIN_AHEAD_MAX_DAYS (default 3); else stale
  * registry/producer error -> hard failure, always
  * consumer repo absent    -> reported SKIPPED, never counted as a pass;
                               advisory by default, hard failure under
                               BEAMFALL_REQUIRE_CONSUMERS=1

By default the check only enforces when the working change actually touches a
contract source (see contractSourcePrefixes in the registry); --all forces it.
If the changed set cannot be determined the check runs anyway -- fail closed.
The diff base defaults to origin/main when that ref exists (CORE-1299): lanes
land by PR, so a shared checkout's local main lags origin/main and diffing
against it attributes every change main gained since to the lane. Without an
origin/main ref the check falls back to local main and says so.

Exit codes: 0 fresh / not triggered / advisory skip, 1 stale or error, 2 usage.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import runpy
import subprocess
import sys
import time
from dataclasses import dataclass, field
from pathlib import Path

FRESH = "FRESH"
STALE = "STALE"
AHEAD = "AHEAD"
SKIPPED = "SKIPPED"
ERROR = "ERROR"

# CORE-1442: how long a consumers-first window may stay open. A consumer whose copy
# matches a pending core publication on another ref is reported AHEAD (advisory)
# only while that publication is younger than this; after that it is STALE again,
# so a stalled core pin turns the gate red instead of being waived forever.
AHEAD_MAX_DAYS_DEFAULT = 3

WORKSPACE_MANIFEST = "workspace-repos.json"

_SHA256_MANIFEST = runpy.run_path(
    str(Path(__file__).resolve().with_name("sha256-manifest.py"))
)


@dataclass
class Result:
    """Verdict for one obligation, plus the lines that justify it."""

    obligation_id: str
    consumer_repo: str
    artifact: str
    asserted_by: str
    refresh: str
    status: str
    findings: list[str] = field(default_factory=list)


def core_root() -> Path:
    """Repo root of this producer checkout (script/ lives directly under it)."""
    return Path(__file__).resolve().parent.parent


def workspace_root(start: Path) -> Path | None:
    """Directory holding the workspace manifest, or None when there is none.

    A standalone core checkout (tarball, temp clone) has no workspace above it.
    That is the strongest form of "every consumer is absent", so it takes the
    absent path -- advisory, reported, never a pass -- not a hard error.
    """
    override = os.environ.get("BEAMFALL_WORKSPACE_ROOT")
    if override:
        resolved = Path(override).resolve()
        return resolved if (resolved / WORKSPACE_MANIFEST).is_file() else None
    for candidate in [start, *start.parents]:
        if (candidate / WORKSPACE_MANIFEST).is_file():
            return candidate
    # A linked worktree outside the workspace (corvint-tasks `gate run` strips
    # BEAMFALL_WORKSPACE_ROOT, CORE-1473) still shares the main checkout's .git,
    # so look above that checkout too, as secret-scan.py does.
    main_checkout = git_main_checkout(start)
    if main_checkout is not None:
        for candidate in [main_checkout, *main_checkout.parents]:
            if (candidate / WORKSPACE_MANIFEST).is_file():
                return candidate
    return None


def git_main_checkout(start: Path) -> Path | None:
    """Main checkout owning start's git common dir, or None outside git."""
    result = subprocess.run(
        ["git", "-C", str(start), "rev-parse", "--path-format=absolute", "--git-common-dir"],
        check=False,
        capture_output=True,
        text=True,
    )
    common = Path(result.stdout.strip())
    if result.returncode != 0 or common.name != ".git":
        return None
    return common.parent


def manifest_repositories(root: Path) -> list[dict]:
    """Repository entries in the workspace manifest's authoritative order."""
    manifest = json.loads((root / WORKSPACE_MANIFEST).read_text(encoding="utf-8"))
    return manifest.get("repositories", [])


def sha256_of(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def parse_sha256sum_lines(text: str) -> dict[str, str]:
    """Strict canonical SHA-256 manifest lines."""
    return _SHA256_MANIFEST["parse_manifest_text"](text)


def parse_json_map(text: str) -> dict[str, str]:
    """`{"<relative path>": "<hex>"}`."""
    return {key: str(value).lower() for key, value in json.loads(text).items()}


DIGEST_PARSERS = {
    "sha256sum-lines": parse_sha256sum_lines,
    "json-map": parse_json_map,
}


def changed_paths(repo: Path, base: str) -> set[str] | None:
    """Repo-relative paths this change touches, or None if undeterminable."""
    merge_base = run_git(repo, ["merge-base", base, "HEAD"])
    if merge_base is None:
        return None
    committed = run_git(repo, ["diff", "--name-only", f"{merge_base}...HEAD"])
    working = run_git(repo, ["status", "--porcelain", "--untracked-files=all"])
    if committed is None or working is None:
        return None
    paths = set(committed.splitlines())
    paths.update(line[3:].strip() for line in working.splitlines() if len(line) > 3)
    return {path for path in paths if path}


def default_base(repo: Path) -> str | None:
    """Diff base when none was requested: origin/main, else local main.

    Local main in a shared checkout is only as fresh as the last manual
    fast-forward, so it is a fallback, announced on stderr, never the first
    choice. None (neither ref exists) leaves the changed set undeterminable,
    which runs the check -- fail closed.
    """
    for ref, note in (
        ("refs/remotes/origin/main", None),
        (
            "refs/heads/main",
            "no origin/main ref; diffing against local main, which may lag "
            "origin/main and over-trigger this check",
        ),
    ):
        if run_git(repo, ["rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}"]):
            if note:
                print(f"consumer-pin-freshness: {note}", file=sys.stderr)
            return ref
    print(
        "consumer-pin-freshness: neither origin/main nor main exists; "
        "cannot scope the diff, checking every obligation",
        file=sys.stderr,
    )
    return None


def run_git(repo: Path, args: list[str]) -> str | None:
    completed = subprocess.run(
        ["git", "-C", str(repo), *args],
        capture_output=True,
        text=True,
        check=False,
    )
    return completed.stdout.strip() if completed.returncode == 0 else None


def touches_contract_source(paths: set[str], prefixes: list[str]) -> bool:
    return any(path.startswith(prefix) for path in paths for prefix in prefixes)


def git_blob_id(data: bytes) -> str:
    """The object id git gives these bytes as a blob."""
    return hashlib.sha1(b"blob %d\0" % len(data) + data).hexdigest()


@dataclass
class AheadContext:
    """What the consumer-ahead classification needs to know about the producer."""

    producer: Path
    merge_base: str | None
    now: float
    max_days: float


def producer_changed(ctx: AheadContext, rel: str, current: bytes) -> bool:
    """True when this branch changes the producer artifact (or that is unprovable)."""
    if ctx.merge_base is None:
        return True
    at_base = run_git(ctx.producer, ["rev-parse", "--verify", "--quiet", f"{ctx.merge_base}:{rel}"])
    return at_base is None or at_base != git_blob_id(current)


def pending_publication(ctx: AheadContext, rel: str, data: bytes) -> tuple[str, str, float] | None:
    """(ref, commit, age_days) of a core publication of these bytes that is not on HEAD.

    None when the bytes are already in HEAD's history of the artifact (the consumer
    is behind) or appear on no branch or remote-tracking ref (the consumer copy is
    simply wrong). Stashes and other refs are not publications.
    """
    blob = git_blob_id(data)
    head = run_git(ctx.producer, ["log", "--format=", "--raw", "--no-abbrev", "HEAD", "--", rel])
    if head is None:
        return None
    if any(len(line.split()) >= 4 and line.split()[3] == blob for line in head.splitlines()):
        return None
    log = run_git(
        ctx.producer,
        ["log", "--branches", "--remotes", "--format=commit %H %ct", "--raw", "--no-abbrev", "--", rel],
    )
    if log is None:
        return None
    commit = when = None
    for line in log.splitlines():
        if line.startswith("commit "):
            _, commit, when = line.split()
        elif commit and len(line.split()) >= 4 and line.split()[3] == blob:
            refs = run_git(
                ctx.producer,
                ["for-each-ref", "--contains", commit, "--format=%(refname)", "refs/remotes", "refs/heads"],
            )
            if not refs:
                return None
            return refs.splitlines()[0], commit, (ctx.now - int(when)) / 86400
    return None


def classify_drift(
    ctx: AheadContext | None, rel: str, current: bytes, vendored: bytes
) -> tuple[str, str]:
    """(status, detail) for one drifted copy: AHEAD only for a young pending publication."""
    if ctx is None or producer_changed(ctx, rel, current):
        return STALE, ""
    pending = pending_publication(ctx, rel, vendored)
    if pending is None:
        return STALE, ""
    ref, commit, age = pending
    if age > ctx.max_days:
        return STALE, (
            f"; it matches the pending core publication on {ref} ({commit[:12]}), but that "
            f"consumers-first window has been open {age:.1f} days (> {ctx.max_days:g}): "
            "land the core pin"
        )
    return AHEAD, (
        f"; consumer ahead: it matches the pending core publication on {ref} "
        f"({commit[:12]}, {age:.1f} days old) and this branch leaves {rel} unchanged"
    )


def check_byte_copies(
    obligation: dict, producer: Path, consumer: Path, ctx: AheadContext | None = None
) -> tuple[list[str], list[str]]:
    """(blocking, advisory) findings, one per producer/consumer pair that differs."""
    findings: list[str] = []
    advisories: list[str] = []
    for pair in obligation.get("byteCopies", []):
        producer_file = producer / pair["producer"]
        consumer_file = consumer / pair["consumer"]
        if not producer_file.is_file():
            findings.append(f"producer artifact absent: {pair['producer']}")
            continue
        if not consumer_file.is_file():
            findings.append(f"vendored copy absent: {pair['consumer']}")
            continue
        current, vendored = producer_file.read_bytes(), consumer_file.read_bytes()
        if current != vendored:
            status, detail = classify_drift(ctx, pair["producer"], current, vendored)
            line = f"byte drift: {pair['consumer']} no longer matches {pair['producer']}{detail}"
            (advisories if status == AHEAD else findings).append(line)
    return findings, advisories


def check_digest_manifest(obligation: dict, producer: Path, consumer: Path) -> list[str]:
    """One finding per recorded digest that no longer matches the producer bytes."""
    spec = obligation.get("digestManifest")
    if not spec:
        return []
    manifest_file = consumer / spec["consumer"]
    if not manifest_file.is_file():
        return [f"vendored digest manifest absent: {spec['consumer']}"]
    parser = DIGEST_PARSERS.get(spec["format"])
    if parser is None:
        return [f"unknown digest manifest format: {spec['format']}"]
    try:
        recorded = parser(manifest_file.read_text(encoding="utf-8"))
    except ValueError as exc:
        return [f"unparseable digest manifest {spec['consumer']}: {exc}"]

    findings = digest_key_findings(spec, recorded)
    producer_dir = producer / spec["producerDir"]
    for key in spec["keys"]:
        producer_file = producer_dir / key
        if not producer_file.is_file():
            findings.append(f"producer artifact absent: {spec['producerDir']}/{key}")
            continue
        if key not in recorded:
            continue
        actual = sha256_of(producer_file)
        if actual != recorded[key]:
            findings.append(
                f"digest drift: {spec['consumer']} records {recorded[key][:12]}… "
                f"for {key}, core now produces {actual[:12]}…"
            )
    return findings


def digest_key_findings(spec: dict, recorded: dict[str, str]) -> list[str]:
    """Key-set divergence between the registry and the vendored manifest."""
    declared = set(spec["keys"])
    present = set(recorded)
    findings = [
        f"digest manifest {spec['consumer']} does not record {key}"
        for key in sorted(declared - present)
    ]
    findings += [
        f"digest manifest {spec['consumer']} records unregistered entry {key}"
        for key in sorted(present - declared)
    ]
    return findings


def result_for(
    obligation: dict, consumer_repo: str, status: str, findings: list[str] | None = None
) -> Result:
    """Build a result while keeping artifact metadata independent of repo identity."""
    return Result(
        obligation_id=obligation["id"],
        consumer_repo=consumer_repo,
        artifact=obligation["artifact"],
        asserted_by=obligation["assertedBy"],
        refresh=obligation["refresh"],
        status=status,
        findings=findings or [],
    )


def unverifiable(obligation: dict, consumer_repo: str, reason: str) -> Result:
    """Obligation that could not be checked at all -- skipped, never a pass."""
    return result_for(obligation, consumer_repo, SKIPPED, [reason])


def evaluate(
    obligation: dict,
    producer: Path,
    consumer_repo: str,
    consumer: Path,
    ctx: AheadContext | None = None,
) -> Result:
    """Verdict for one obligation against the producer tree as it stands now.

    A consumer ahead of this branch (CORE-1442) is advisory only when every drifted
    byte copy is ahead and no digest manifest disagrees for any other reason.
    """
    result = result_for(obligation, consumer_repo, FRESH)
    findings, advisories = check_byte_copies(obligation, producer, consumer, ctx)
    digest = check_digest_manifest(obligation, producer, consumer)
    if advisories and not findings:
        ahead_keys = ahead_digest_keys(obligation, consumer, advisories)
        digest = [line for line in digest if not any(f" for {key}," in line for key in ahead_keys)]
    findings += digest
    if findings:
        result.status = STALE
        result.findings = findings + advisories
    elif advisories:
        result.status = AHEAD
        result.findings = advisories
    return result


def ahead_digest_keys(obligation: dict, consumer: Path, advisories: list[str]) -> set[str]:
    """Digest-manifest keys whose recorded digest is that of an ahead byte copy."""
    spec = obligation.get("digestManifest")
    if not spec:
        return set()
    manifest_file = consumer / spec["consumer"]
    parser = DIGEST_PARSERS.get(spec["format"])
    if parser is None or not manifest_file.is_file():
        return set()
    try:
        recorded = parser(manifest_file.read_text(encoding="utf-8"))
    except ValueError:
        return set()
    prefix = spec["producerDir"].rstrip("/") + "/"
    keys: set[str] = set()
    for pair in obligation.get("byteCopies", []):
        key = pair["producer"].removeprefix(prefix)
        ahead = any(line.startswith(f"byte drift: {pair['consumer']} ") for line in advisories)
        vendored = consumer / pair["consumer"]
        if ahead and key in recorded and vendored.is_file() and sha256_of(vendored) == recorded[key]:
            keys.add(key)
    return keys


def workspace_results(
    registry: dict, producer: Path, workspace: Path, ctx: AheadContext | None = None
) -> list[Result]:
    """Resolve obligation owners by iterating workspace-repos.json entries."""
    obligations = registry["obligations"]
    obligations_by_id = {obligation["id"]: obligation for obligation in obligations}
    entries = manifest_repositories(workspace)
    known = {entry["name"] for entry in entries}
    assignments: dict[str, list[str]] = {
        obligation_id: [] for obligation_id in obligations_by_id
    }
    results: list[Result] = []

    for entry in entries:
        assigned_ids = entry.get("coreContractObligations", [])
        consumer = workspace / entry["name"]
        for obligation_id in assigned_ids:
            obligation = obligations_by_id.get(obligation_id)
            if obligation is None:
                results.append(
                    Result(
                        obligation_id=obligation_id,
                        consumer_repo=entry["name"],
                        artifact=WORKSPACE_MANIFEST,
                        asserted_by=WORKSPACE_MANIFEST,
                        refresh="remove or correct the unknown obligation ID in "
                        f"{WORKSPACE_MANIFEST}",
                        status=ERROR,
                        findings=[
                            f"{WORKSPACE_MANIFEST} assigns unknown Core contract "
                            f"obligation {obligation_id}"
                        ],
                    )
                )
                continue
            assignments[obligation_id].append(entry["name"])
            if not consumer.is_dir():
                results.append(
                    unverifiable(
                        obligation,
                        entry["name"],
                        f"{entry['name']} is not checked out at {consumer}; this "
                        "obligation was NOT verified",
                    )
                )
                continue
            probe = consumer / obligation["consumerProbe"]
            if not probe.is_file():
                results.append(
                    result_for(
                        obligation,
                        entry["name"],
                        ERROR,
                        [f"consumer assertion absent: {obligation['consumerProbe']}"],
                    )
                )
                continue
            results.append(evaluate(obligation, producer, entry["name"], consumer, ctx))

    for child in workspace.iterdir():
        if not child.is_dir() or child.name in known:
            continue
        for obligation in obligations:
            if (child / obligation["consumerProbe"]).is_file():
                results.append(
                    result_for(
                        obligation,
                        child.name,
                        ERROR,
                        [
                            f"{WORKSPACE_MANIFEST} does not list {child.name}; "
                            f"found consumer assertion {obligation['consumerProbe']}"
                        ],
                    )
                )

    for obligation_id, owners in assignments.items():
        obligation = obligations_by_id[obligation_id]
        if not owners:
            results.append(
                result_for(
                    obligation,
                    WORKSPACE_MANIFEST,
                    ERROR,
                    [
                        f"no {WORKSPACE_MANIFEST} repo declares Core contract "
                        f"obligation {obligation_id}"
                    ],
                )
            )
            continue
        if len(owners) == 1:
            continue
        names = ", ".join(owners)
        results.append(
            result_for(
                obligation,
                names,
                ERROR,
                [
                    f"multiple {WORKSPACE_MANIFEST} repos declare Core contract "
                    f"obligation {obligation_id}"
                ],
            )
        )
    return results


def emit(results: list[Result]) -> None:
    for result in results:
        print(f"  [{result.status:7}] {result.consumer_repo}: {result.artifact}")
        print(f"            asserted by {result.asserted_by}")
        for finding in result.findings:
            print(f"            - {finding}")
        if result.status in (STALE, SKIPPED, ERROR):
            print(f"            refresh: {result.refresh}")
        if result.status == AHEAD:
            print(
                "            advisory: land the matching core publication; the gate turns "
                "red if the window outlives BEAMFALL_PIN_AHEAD_MAX_DAYS"
            )


def verdict(results: list[Result], require_consumers: bool) -> int:
    """Aggregate exit code; skips never count as a pass."""
    blocking = [r for r in results if r.status in (STALE, ERROR)]
    skipped = [r for r in results if r.status == SKIPPED]
    if blocking:
        print(
            f"consumer-pin-freshness: FAIL — {len(blocking)} consumer-vendored "
            "artifact(s) are stale against the current core contract sources. "
            "Refresh them in the same landing window as this core change."
        )
        return 1
    if skipped and require_consumers:
        print(
            f"consumer-pin-freshness: FAIL — {len(skipped)} obligation(s) could "
            "not be verified and BEAMFALL_REQUIRE_CONSUMERS=1 is set."
        )
        return 1
    if skipped:
        print(
            f"consumer-pin-freshness: INCOMPLETE — {len(skipped)} obligation(s) "
            "skipped (consumer repo absent), "
            f"{len(results) - len(skipped)} verified fresh. Skipped is not a pass; "
            "set BEAMFALL_REQUIRE_CONSUMERS=1 to make absence a hard failure."
        )
        return 0
    ahead = [r for r in results if r.status == AHEAD]
    if ahead:
        print(
            f"consumer-pin-freshness: PASS with {len(ahead)} ADVISORY — "
            f"{', '.join(sorted({r.consumer_repo for r in ahead}))} vendor(s) a pending core "
            "publication this branch does not change; "
            f"{len(results) - len(ahead)} obligation(s) fresh."
        )
        return 0
    print(f"consumer-pin-freshness: PASS — all {len(results)} obligation(s) fresh.")
    return 0


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="consumer-pin-freshness",
        description="Fail a core wire-contract change while any consumer-vendored "
        "snapshot still matches the old contract.",
    )
    parser.add_argument(
        "--all",
        action="store_true",
        help="check every obligation regardless of whether the diff touches a "
        "contract source",
    )
    parser.add_argument(
        "--base",
        default=os.environ.get("BEAMFALL_PIN_FRESHNESS_BASE") or None,
        help="base ref for the contract-source diff guard "
        "(default: origin/main, falling back to local main)",
    )
    parser.add_argument(
        "--obligations",
        default=None,
        help="path to the obligation registry "
        "(default: script/consumer-pin-obligations.json)",
    )
    parser.add_argument(
        "--changed-paths",
        default=None,
        help="file of newline-separated repo-relative paths to use as the changed "
        "set instead of asking git; used by the negative controls to exercise the "
        "diff guard in both directions",
    )
    return parser.parse_args(argv)


def resolve_changed(args: argparse.Namespace, producer: Path) -> set[str] | None:
    if args.changed_paths:
        text = Path(args.changed_paths).read_text(encoding="utf-8")
        return {line.strip() for line in text.splitlines() if line.strip()}
    base = args.base or default_base(producer)
    return changed_paths(producer, base) if base else None


def ahead_context(args: argparse.Namespace, producer: Path) -> AheadContext | None:
    """Context for the consumer-ahead classification; None disables it (fail closed)."""
    raw = os.environ.get("BEAMFALL_PIN_AHEAD_MAX_DAYS", str(AHEAD_MAX_DAYS_DEFAULT))
    try:
        max_days = float(raw)
        if not math.isfinite(max_days):
            raise ValueError(raw)
    except ValueError:
        print(f"consumer-pin-freshness: ignoring malformed BEAMFALL_PIN_AHEAD_MAX_DAYS={raw!r}", file=sys.stderr)
        max_days = float(AHEAD_MAX_DAYS_DEFAULT)
    if max_days <= 0:
        return None
    base = args.base or default_base(producer)
    merge_base = run_git(producer, ["merge-base", base, "HEAD"]) if base else None
    now = float(os.environ.get("BEAMFALL_PIN_AHEAD_NOW") or time.time())
    return AheadContext(producer=producer, merge_base=merge_base, now=now, max_days=max_days)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    producer = core_root()
    default_registry = producer / "script" / "consumer-pin-obligations.json"
    registry_path = Path(args.obligations) if args.obligations else default_registry
    registry = json.loads(registry_path.read_text(encoding="utf-8"))

    if not args.all:
        paths = resolve_changed(args, producer)
        prefixes = registry["contractSourcePrefixes"]
        if paths is not None and not touches_contract_source(paths, prefixes):
            print(
                "consumer-pin-freshness: not triggered — this change touches no "
                f"core contract source ({', '.join(prefixes)}). Use --all to force."
            )
            return 0

    workspace = workspace_root(producer)
    print(f"consumer-pin-freshness: producer={producer} workspace={workspace}")
    if workspace is None:
        reason = (
            f"no {WORKSPACE_MANIFEST} above {producer}: this is a standalone core "
            "checkout with no consumer repos beside it, so NOTHING was verified. "
            "Set BEAMFALL_WORKSPACE_ROOT to the workspace directory."
        )
        results = [
            unverifiable(obligation, "workspace consumers", reason)
            for obligation in registry["obligations"]
        ]
    else:
        results = workspace_results(registry, producer, workspace, ahead_context(args, producer))
    emit(results)
    return verdict(results, os.environ.get("BEAMFALL_REQUIRE_CONSUMERS") == "1")


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
