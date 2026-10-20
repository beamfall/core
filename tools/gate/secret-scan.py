#!/usr/bin/env python3
"""Pattern-based secret scan for Core and named sibling repositories."""

from __future__ import annotations

import argparse
from collections.abc import Iterator
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys


SCRIPT = Path(__file__).resolve()
CORE_ROOT = SCRIPT.parents[2]
ALLOWLIST = SCRIPT.with_name("secret-scan-allowlist.txt")


def workspace_root() -> Path:
    result = subprocess.run(
        [
            "git",
            "-C",
            str(CORE_ROOT),
            "rev-parse",
            "--path-format=absolute",
            "--git-common-dir",
        ],
        check=False,
        capture_output=True,
        text=True,
    )
    common = Path(result.stdout.strip())
    if result.returncode == 0 and common.name == ".git":
        return common.parent.parent
    return CORE_ROOT.parent


PATTERNS = (
    (
        "private-key",
        re.compile(rb"-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----"),
        0,
    ),
    (
        "private-key-pgp",
        re.compile(b"-----BEGIN PGP " + b"PRIVATE KEY BLOCK-----"),
        0,
    ),
    ("provider-aws", re.compile(rb"(?<![A-Z0-9])(?:AKIA|ASIA)[A-Z0-9]{16}(?![A-Z0-9])"), 0),
    ("provider-github", re.compile(rb"\bgh[pousr]_[A-Za-z0-9]{20,255}\b"), 0),
    ("provider-github-fine-grained", re.compile(rb"\bgithub_pat_[A-Za-z0-9_]{20,255}\b"), 0),
    ("provider-gitlab", re.compile(rb"\bglpat-[A-Za-z0-9_-]{20,255}\b"), 0),
    ("provider-slack", re.compile(rb"\bxox[baprs]-[A-Za-z0-9-]{10,255}\b"), 0),
    ("provider-google", re.compile(rb"\bAIza[0-9A-Za-z_-]{30,255}\b"), 0),
    ("provider-stripe", re.compile(rb"\bsk_(?:live|test)_[A-Za-z0-9]{16,255}\b"), 0),
    ("provider-npm", re.compile(rb"\bnpm_[A-Za-z0-9]{20,255}\b"), 0),
    (
        "provider-pypi",
        re.compile(rb"\bpypi-AgEIcHlwaS5vcmc[A-Za-z0-9_-]{20,255}\b"),
        0,
    ),
    (
        "provider-sendgrid",
        re.compile(rb"\bSG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{32,}\b"),
        0,
    ),
    ("provider-twilio", re.compile(rb"\bSK[0-9a-fA-F]{32}\b"), 0),
    (
        "credential-auth-header",
        re.compile(rb"(?i)\bBearer[ \t]+(?P<secret>[A-Za-z0-9][A-Za-z0-9_.~+/-]{15,})"),
        1,
    ),
    (
        "credential-url",
        re.compile(
            rb"(?i)\bhttps?://(?P<secret>[^/@\s:]{2,}:[^/@\s]{4,})@"
        ),
        1,
    ),
    (
        "credential-assignment",
        re.compile(
            rb"""(?ix)
            (?<![A-Za-z0-9_])
            ["']?
            (?:[A-Za-z0-9_-]*
               (?:api[_-]?key|access[_-]?token|auth[_-]?token|bearer[_-]?token|
                  client[_-]?secret|password|passwd|secret|token)
               |[A-Za-z0-9_-]*secret[_-]?access[_-]?key)
            ["']?
            [ \t]*[:=][ \t]*
            ["'](?P<secret>[^"' \t\r\n]{15,})["']
            """
        ),
        1,
    ),
    (
        "credential-env",
        re.compile(
            rb"""(?imx)
            ^(?:export[ \t]+)?
            [A-Z][A-Z0-9_]*
            (?:API_KEY|ACCESS_TOKEN|AUTH_TOKEN|CLIENT_SECRET|PASSWORD|PASSWD|SECRET|TOKEN)
            [A-Z0-9_]*
            [ \t]*=[ \t]*["']?
            (?P<secret>[^"' \t\r\n#]{15,})
            ["']?[ \t]*(?:[#;].*)?$
            """
        ),
        1,
    ),
    (
        "credential-jwt",
        re.compile(
            rb"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b"
        ),
        0,
    ),
)

TEXT_ONLY_RULES = {
    "credential-assignment",
    "credential-env",
    "credential-url",
}

NON_ALLOWLISTABLE_RULES = {
    "private-key",
    "private-key-pgp",
}

CORPUS_DIRECTORY_MARKER = "/corpus/"
CORPUS_MANIFEST_NAME = "corpus.manifest.json"
CORPUS_VERSION_NAME = "corpus.version"
CORPUS_VERSION_PATTERN = re.compile(r"[0-9a-f]{64}")
WIRE_CONTRACT_BINDING = "Tests/BeamfallKitTests/Fixtures/core-wire-client.generated.json"
WIRE_CONTRACT_VERSION_PATTERN = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="scan Core and sibling repositories for credential-shaped strings"
    )
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--allow-missing-repos", action="store_true")
    mode.add_argument("--require-repos", action="store_true")
    parser.add_argument(
        "--target",
        action="append",
        default=[],
        metavar="LABEL:PATH",
        help="replace the default target matrix; may be repeated",
    )
    return parser.parse_args()


def default_targets() -> list[tuple[str, Path]]:
    workspace = Path(os.environ.get("BEAMFALL_WORKSPACE_ROOT", workspace_root()))
    return [
        ("beamfall", CORE_ROOT),
        ("beamfall-apple", workspace / "beamfall-apple"),
        ("beamfall-android", workspace / "beamfall-android"),
    ]


def parse_targets(values: list[str]) -> list[tuple[str, Path]]:
    if not values:
        targets = default_targets()
    else:
        targets = []
        for value in values:
            label, separator, raw_path = value.partition(":")
            if not separator or not label or not raw_path:
                raise ValueError(f"malformed target: {value!r}; expected LABEL:PATH")
            targets.append((label, Path(raw_path)))
    labels = [label for label, _ in targets]
    if len(labels) != len(set(labels)):
        raise ValueError("target labels must be unique")
    return [(label, root.resolve()) for label, root in targets]


def load_allowlist() -> set[tuple[str, str, str, str]]:
    entries = set()
    try:
        lines = ALLOWLIST.read_text(encoding="utf-8").splitlines()
    except OSError as error:
        raise ValueError(f"cannot read allowlist {ALLOWLIST}: {error}") from error
    for line_number, line in enumerate(lines, 1):
        if not line or line.startswith("#"):
            continue
        fields = line.split("\t")
        if len(fields) != 4:
            raise ValueError(f"{ALLOWLIST}:{line_number}: expected four tab-separated fields")
        label, path, rule, fingerprint = fields
        if not label or not path or not rule or not re.fullmatch(r"[0-9a-f]{64}", fingerprint):
            raise ValueError(f"{ALLOWLIST}:{line_number}: malformed allowlist entry")
        entries.add((label, path, rule, fingerprint))
    return entries


def tracked_entries(root: Path) -> list[tuple[str, str]]:
    result = subprocess.run(
        ["git", "-C", str(root), "ls-files", "-z", "--cached", "--stage"],
        check=False,
        capture_output=True,
    )
    if result.returncode != 0:
        message = result.stderr.decode("utf-8", "replace").strip()
        raise RuntimeError(f"cannot enumerate tracked files: {message or 'git ls-files failed'}")
    entries = []
    for raw in result.stdout.split(b"\0"):
        if not raw:
            continue
        metadata, separator, raw_path = raw.partition(b"\t")
        fields = metadata.split()
        if not separator or len(fields) != 3:
            raise RuntimeError("cannot parse git ls-files --stage output")
        _, raw_object_id, raw_stage = fields
        relative = raw_path.decode("utf-8", "surrogateescape")
        if raw_stage != b"0":
            raise RuntimeError(f"cannot scan unmerged index entry: {relative}")
        entries.append((relative, raw_object_id.decode("ascii")))
    return entries


def corpus_prefix(entries: list[tuple[str, str]]) -> str | None:
    corpus_prefixes = {
        relative.partition(CORPUS_DIRECTORY_MARKER)[0] + CORPUS_DIRECTORY_MARKER
        for relative, _ in entries
        if CORPUS_DIRECTORY_MARKER in relative
    }
    if len(corpus_prefixes) != 1:
        return None
    return corpus_prefixes.pop()


def corpus_identity(
    root: Path, entries: list[tuple[str, str]]
) -> tuple[str | None, str | None]:
    """Return the indexed fixture-byte hash and an optional unverified declaration."""
    corpus_root = corpus_prefix(entries)
    if corpus_root is None:
        return None, None
    corpus_entries = [
        entry for entry in entries if entry[0].startswith(corpus_root)
    ]
    try:
        corpus_files = {
            relative.removeprefix(corpus_root): data
            for relative, data, _ in index_file_bytes(root, corpus_entries)
        }
    except RuntimeError:
        return None, None

    raw_declared = corpus_files.pop(CORPUS_VERSION_NAME, None)
    corpus_files.pop(CORPUS_MANIFEST_NAME, None)
    declared = None
    if raw_declared is not None:
        try:
            candidate = raw_declared.decode("utf-8").strip()
        except UnicodeError:
            candidate = ""
        if CORPUS_VERSION_PATTERN.fullmatch(candidate):
            declared = candidate
    if not corpus_files:
        return None, declared

    digest = hashlib.sha256()
    for name in sorted(corpus_files):
        digest.update(name.encode("utf-8"))
        digest.update(b"\n")
        digest.update(corpus_files[name])
    return digest.hexdigest(), declared


def wire_contract_version(
    root: Path, entries: list[tuple[str, str]]
) -> str | None:
    binding_entries = [
        entry for entry in entries if entry[0] == WIRE_CONTRACT_BINDING
    ]
    if len(binding_entries) != 1:
        return None
    try:
        _, data, _ = next(index_file_bytes(root, binding_entries))
        document = json.loads(data)
    except (RuntimeError, StopIteration, UnicodeError, json.JSONDecodeError):
        return None
    if not isinstance(document, dict):
        return None
    version = document.get("artifactVersion")
    if not isinstance(version, str):
        return None
    if WIRE_CONTRACT_VERSION_PATTERN.fullmatch(version) is None:
        return None
    return version


def sibling_corpus_diagnostic(
    label: str,
    root: Path,
    entries: list[tuple[str, str]],
    indexed_finding_paths: set[str],
) -> str:
    recomputed, declared = corpus_identity(root, entries)
    if recomputed is None:
        return (
            f"secret-scan: sibling {label} corpus identity is unresolved; credential "
            "findings remain blocking on their own terms"
        )

    prefix = corpus_prefix(entries)
    corpus_findings = sorted(
        relative
        for relative in indexed_finding_paths
        if prefix is not None and relative.startswith(prefix)
    )
    if not corpus_findings:
        return (
            f"secret-scan: sibling {label} credential findings remain blocking on their "
            "own terms; the corpus identity does not apply to the reported files"
        )

    if declared is None:
        identity = (
            f"corpus fixture bytes sha256={recomputed} (no declared corpus.version)"
        )
    elif declared == recomputed:
        identity = (
            f"corpus identity sha256={recomputed} "
            "(recomputed from fixture bytes; declared corpus.version agrees)"
        )
    else:
        paths = ", ".join(corpus_findings)
        contract_version = wire_contract_version(root, entries) or "unresolved"
        return (
            f"secret-scan: sibling {label} corpus fixture bytes sha256={recomputed}; "
            f"wire-contract version={contract_version}; "
            f"declared corpus.version={declared} disagrees with recomputed hash; "
            f"findings remain blocking; for verified inert indexed fixture values in {paths}, "
            "the matching corpus resync needs allowlist entries in "
            "tools/gate/secret-scan-allowlist.txt; those entries must be digest-bound"
        )
    return (
        f"secret-scan: sibling {label} {identity}; credential findings remain blocking "
        "on their own terms; the identity does not evidence a corpus resync"
    )


def worktree_divergences(root: Path) -> list[str]:
    result = subprocess.run(
        [
            "git",
            "-C",
            str(root),
            "diff",
            "--no-ext-diff",
            "--no-renames",
            "--name-only",
            "-z",
            "--",
        ],
        check=False,
        capture_output=True,
    )
    if result.returncode != 0:
        message = result.stderr.decode("utf-8", "replace").strip()
        raise RuntimeError(
            f"cannot compare index and working tree: {message or 'git diff failed'}"
        )
    return [
        raw.decode("utf-8", "surrogateescape")
        for raw in result.stdout.split(b"\0")
        if raw
    ]


def divergent_worktree_file_bytes(
    root: Path, paths: list[str]
) -> Iterator[tuple[str, bytes, bool]]:
    for relative in paths:
        path = root / relative
        try:
            if path.is_symlink():
                data = os.fsencode(os.readlink(path))
            else:
                data = path.read_bytes()
        except (FileNotFoundError, IsADirectoryError):
            continue
        except OSError as error:
            raise RuntimeError(
                f"cannot read tracked working-tree file {relative}: {error}"
            ) from error
        yield relative, data, b"\0" in data


def untracked_worktree_file_bytes(root: Path) -> Iterator[tuple[str, bytes, bool]]:
    result = subprocess.run(
        ["git", "-C", str(root), "ls-files", "-z", "--others", "--exclude-standard"],
        check=False,
        capture_output=True,
    )
    if result.returncode != 0:
        message = result.stderr.decode("utf-8", "replace").strip()
        raise RuntimeError(
            f"cannot enumerate untracked files: {message or 'git ls-files failed'}"
        )
    for raw in result.stdout.split(b"\0"):
        if not raw:
            continue
        relative = raw.decode("utf-8", "surrogateescape")
        try:
            data = (root / relative).read_bytes()
        except OSError as error:
            raise RuntimeError(f"cannot read untracked file {relative}: {error}") from error
        yield relative, data, b"\0" in data


def git_common_dir(root: Path) -> Path:
    result = subprocess.run(
        [
            "git",
            "-C",
            str(root),
            "rev-parse",
            "--path-format=absolute",
            "--git-common-dir",
        ],
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        message = result.stderr.strip()
        raise RuntimeError(message or "git rev-parse --git-common-dir failed")
    return Path(result.stdout.strip()).resolve()


def validate_distinct_repositories(targets: list[tuple[str, Path]]) -> None:
    roots: dict[Path, str] = {}
    for label, root in targets:
        previous = roots.get(root)
        if previous is not None:
            raise ValueError(
                f"required target labels {previous!r} and {label!r} resolve to "
                f"the same repository root: {root}"
            )
        roots[root] = label

    identities: list[tuple[str, Path, Path]] = []
    for label, root in targets:
        try:
            common_dir = git_common_dir(root)
        except RuntimeError as error:
            raise ValueError(
                f"cannot identify required target {label!r} at {root}: {error}"
            ) from error
        for previous_label, previous_root, previous_common_dir in identities:
            if common_dir.samefile(previous_common_dir):
                raise ValueError(
                    f"required target labels {previous_label!r} ({previous_root}) and "
                    f"{label!r} ({root}) share one Git repository identity; common Git "
                    f"directories resolve to {previous_common_dir} and {common_dir}"
                )
        identities.append((label, root, common_dir))


def index_file_bytes(
    root: Path, entries: list[tuple[str, str]]
) -> Iterator[tuple[str, bytes, bool]]:
    if not entries:
        return
    process = subprocess.Popen(
        ["git", "-C", str(root), "cat-file", "--batch"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if process.stdin is None or process.stdout is None or process.stderr is None:
        process.kill()
        process.wait()
        raise RuntimeError("cannot read staged blobs: git cat-file pipes unavailable")
    try:
        for relative, expected_object_id in entries:
            try:
                process.stdin.write(f"{expected_object_id}\n".encode("ascii"))
                process.stdin.flush()
            except OSError as error:
                raise RuntimeError(
                    f"cannot read staged blob {relative}: git cat-file input failed"
                ) from error
            header = process.stdout.readline().removesuffix(b"\n").split()
            if len(header) != 3:
                raise RuntimeError(f"cannot read staged blob {relative}: malformed header")
            raw_object_id, object_type, raw_size = header
            if raw_object_id.decode("ascii") != expected_object_id or object_type != b"blob":
                raise RuntimeError(
                    f"cannot read staged blob {relative}: index object is not a blob"
                )
            try:
                size = int(raw_size)
            except ValueError as error:
                raise RuntimeError(
                    f"cannot read staged blob {relative}: malformed size"
                ) from error
            data = process.stdout.read(size)
            if len(data) != size or process.stdout.read(1) != b"\n":
                raise RuntimeError(f"cannot read staged blob {relative}: truncated content")
            yield relative, data, b"\0" in data
        process.stdin.close()
        if process.stdout.read(1):
            raise RuntimeError("cannot read staged blobs: unexpected trailing output")
        message = process.stderr.read().decode("utf-8", "replace").strip()
        if process.wait() != 0:
            raise RuntimeError(
                f"cannot read staged blobs: {message or 'git cat-file failed'}"
            )
    finally:
        if process.poll() is None:
            process.terminate()
            process.wait()


def fingerprint(secret: bytes) -> str:
    return hashlib.sha256(secret).hexdigest()


def findings(
    label: str,
    relative: str,
    data: bytes,
    binary: bool,
    allowlist: set[tuple[str, str, str, str]],
) -> list[tuple[int, str]]:
    detected = []
    seen_spans = set()
    for rule, pattern, secret_group in PATTERNS:
        if binary and rule in TEXT_ONLY_RULES:
            continue
        for match in pattern.finditer(data):
            span = match.span(secret_group)
            if span in seen_spans:
                continue
            seen_spans.add(span)
            secret = match.group(secret_group)
            digest = fingerprint(secret)
            allowlisted = (label, relative, rule, digest) in allowlist
            if rule not in NON_ALLOWLISTABLE_RULES and allowlisted:
                continue
            line = data.count(b"\n", 0, match.start()) + 1
            detected.append((line, rule))
    return detected


def scan_target(
    label: str,
    root: Path,
    required: bool,
    allowlist: set[tuple[str, str, str, str]],
) -> tuple[bool, bool]:
    if not root.is_dir():
        if not required:
            print(f"secret-scan: SKIP {label} (missing {root})")
            return True, True
        print(f"secret-scan: FAIL {label} missing required repo at {root}", file=sys.stderr)
        return False, False
    scanned = 0
    count = 0
    indexed_finding_paths: set[str] = set()
    try:
        entries = tracked_entries(root)
        for relative, data, binary in index_file_bytes(root, entries):
            scanned += 1
            for line, rule in findings(label, relative, data, binary, allowlist):
                print(
                    f"secret-scan: finding {label}:{relative}:{line} rule={rule} value=[REDACTED]",
                    file=sys.stderr,
                )
                count += 1
                indexed_finding_paths.add(relative)
        for relative, data, binary in divergent_worktree_file_bytes(
            root, worktree_divergences(root)
        ):
            for line, rule in findings(label, relative, data, binary, allowlist):
                print(
                    f"secret-scan: finding {label}:{relative}:{line} rule={rule} value=[REDACTED]",
                    file=sys.stderr,
                )
                count += 1
        for relative, data, binary in untracked_worktree_file_bytes(root):
            scanned += 1
            for line, rule in findings(label, relative, data, binary, allowlist):
                print(
                    f"secret-scan: finding {label}:{relative}:{line} rule={rule} value=[REDACTED]",
                    file=sys.stderr,
                )
                count += 1
    except RuntimeError as error:
        print(f"secret-scan: FAIL {label} {error}", file=sys.stderr)
        return False, False
    if scanned == 0:
        print(f"secret-scan: FAIL {label} contains no scannable tracked files", file=sys.stderr)
        return False, False
    if count:
        print(f"secret-scan: FAIL {label} findings={count}", file=sys.stderr)
        if root != CORE_ROOT:
            print(
                sibling_corpus_diagnostic(label, root, entries, indexed_finding_paths),
                file=sys.stderr,
            )
        return False, False
    print(f"secret-scan: PASS {label} files={scanned}")
    return True, False


def main() -> int:
    args = parse_args()
    try:
        targets = parse_targets(args.target)
        allowlist = load_allowlist()
    except ValueError as error:
        print(f"secret-scan: {error}", file=sys.stderr)
        return 2
    require_all = args.require_repos
    if require_all:
        missing = [(label, root) for label, root in targets if not root.is_dir()]
        for label, root in missing:
            print(
                f"secret-scan: FAIL {label} missing required repo at {root}",
                file=sys.stderr,
            )
        if missing:
            return 1
        try:
            validate_distinct_repositories(targets)
        except ValueError as error:
            print(f"secret-scan: FAIL {error}", file=sys.stderr)
            return 1
    passed = True
    skipped = 0
    for label, root in targets:
        required = require_all or label == "beamfall"
        target_passed, target_skipped = scan_target(label, root, required, allowlist)
        passed = passed and target_passed
        skipped += int(target_skipped)
    if not passed:
        return 1
    if require_all:
        print(f"secret-scan: PASS complete required-tree coverage targets={len(targets)}")
    else:
        print(
            "secret-scan: PASS best-effort Core + present siblings "
            f"targets={len(targets) - skipped} skipped={skipped}; not all-tree coverage"
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
