#!/usr/bin/env python3
"""Content-closure hashing and green receipts for cached gate steps."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile
from typing import Iterable


SCHEMA_VERSION = 2
HASH_RE = re.compile(r"[0-9a-f]{64}\Z")
HEAD_RE = re.compile(r"[0-9a-f]{40}(?:[0-9a-f]{24})?\Z")
TIMESTAMP_RE = re.compile(r"[0-9]{8}T[0-9]{6}Z\Z")

CACHE_HELPER = Path("tools/gate/slow_orchestration_cache.py")
RACE_SCRIPT = Path("script/race-test.sh")
RACE_TARGETS = Path("tools/gate/targets.mk")
GO_INPUT_FIELDS = (
    "GoFiles",
    "CgoFiles",
    "CFiles",
    "CXXFiles",
    "MFiles",
    "HFiles",
    "FFiles",
    "SFiles",
    "SwigFiles",
    "SwigCXXFiles",
    "SysoFiles",
    "TestGoFiles",
    "XTestGoFiles",
    "EmbedFiles",
    "TestEmbedFiles",
    "XTestEmbedFiles",
)


@dataclass(frozen=True)
class ClosureSpec:
    name: str
    step_name: str
    hash_domain: bytes


SPECS = {
    "race-short": ClosureSpec(
        "race-short",
        "race-short",
        b"beamfall-gate-closure-race-short-v1\0",
    ),
}


class ClosureError(RuntimeError):
    """The repository input closure cannot be proven complete."""


def repository_root(raw: str) -> Path:
    root = Path(raw).resolve(strict=True)
    if not root.is_dir():
        raise ClosureError(f"repository root is not a directory: {root}")
    return root


def read_regular(root: Path, relative: Path) -> bytes:
    candidate = root / relative
    try:
        metadata = candidate.lstat()
    except OSError as exc:
        raise ClosureError(f"cannot stat closure input {relative}: {exc}") from exc
    if not stat.S_ISREG(metadata.st_mode):
        raise ClosureError(f"closure input is not a regular file: {relative}")
    try:
        return candidate.read_bytes()
    except OSError as exc:
        raise ClosureError(f"cannot read closure input {relative}: {exc}") from exc


def decoded_regular(root: Path, relative: Path) -> str:
    try:
        return read_regular(root, relative).decode("utf-8")
    except UnicodeDecodeError as exc:
        raise ClosureError(f"closure input is not valid UTF-8: {relative}") from exc


def walked_files(root: Path, relative_dir: Path) -> Iterable[Path]:
    directory = root / relative_dir
    try:
        metadata = directory.lstat()
    except OSError as exc:
        raise ClosureError(f"cannot stat closure directory {relative_dir}: {exc}") from exc
    if not stat.S_ISDIR(metadata.st_mode):
        raise ClosureError(f"closure directory is not a directory: {relative_dir}")

    def walk_error(error: OSError) -> None:
        raise error

    try:
        for current, directories, files in os.walk(
            directory, topdown=True, followlinks=False, onerror=walk_error
        ):
            current_path = Path(current)
            for name in directories:
                child = current_path / name
                if child.is_symlink():
                    raise ClosureError(
                        f"symlinked closure directory is ambiguous: {child.relative_to(root)}"
                    )
            for name in files:
                yield (current_path / name).relative_to(root)
    except OSError as exc:
        raise ClosureError(f"cannot enumerate closure directory {relative_dir}: {exc}") from exc


def command_output(root: Path, *args: str) -> str:
    try:
        result = subprocess.run(
            args,
            cwd=root,
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
    except (OSError, subprocess.CalledProcessError) as exc:
        detail = getattr(exc, "stderr", "").strip()
        suffix = f": {detail}" if detail else ""
        raise ClosureError(f"{' '.join(args)} failed{suffix}") from exc
    return result.stdout.strip()


def git_output(root: Path, *args: str) -> str:
    return command_output(root, "git", "-C", str(root), *args)


def race_excluded_packages(root: Path) -> set[str]:
    script = decoded_regular(root, RACE_SCRIPT)
    match = re.search(
        r"^RACE_SHORT_EXCLUDED_PKGS=\(\n(?P<body>.*?)^\)\n",
        script,
        re.MULTILINE | re.DOTALL,
    )
    if match is None:
        raise ClosureError("cannot locate RACE_SHORT_EXCLUDED_PKGS in race-test.sh")
    packages = {line.strip() for line in match.group("body").splitlines() if line.strip()}
    if not packages or any(not package.startswith("github.com/beamfall/core/") for package in packages):
        raise ClosureError("race-short exclusions contain an unclassified package")
    return packages


def race_packages(root: Path) -> tuple[str, ...]:
    discovered = command_output(root, "go", "list", "./...").splitlines()
    excluded = race_excluded_packages(root)
    packages = tuple(package for package in discovered if package not in excluded)
    if not packages or len(packages) != len(set(packages)):
        raise ClosureError("race-short package roots are empty or duplicated")
    if not excluded.issubset(discovered):
        missing = ", ".join(sorted(excluded.difference(discovered)))
        raise ClosureError(f"race-short exclusions are not wired packages: {missing}")
    return packages


def decode_json_stream(raw: str) -> Iterable[dict[str, object]]:
    decoder = json.JSONDecoder()
    offset = 0
    while offset < len(raw):
        while offset < len(raw) and raw[offset].isspace():
            offset += 1
        if offset == len(raw):
            return
        try:
            value, offset = decoder.raw_decode(raw, offset)
        except json.JSONDecodeError as exc:
            raise ClosureError("go list returned malformed JSON") from exc
        if not isinstance(value, dict):
            raise ClosureError("go list returned a non-object package record")
        yield value


def race_go_inputs(root: Path, packages: tuple[str, ...]) -> set[Path]:
    raw = command_output(root, "go", "list", "-deps", "-test", "-json", *packages)
    entries: set[Path] = set()
    for record in decode_json_stream(raw):
        directory_raw = record.get("Dir")
        if not isinstance(directory_raw, str):
            continue
        directory = Path(directory_raw).resolve()
        try:
            relative_dir = directory.relative_to(root)
        except ValueError:
            continue
        for field in GO_INPUT_FIELDS:
            names = record.get(field, [])
            if not isinstance(names, list) or any(not isinstance(name, str) for name in names):
                raise ClosureError(f"go list returned invalid {field} for {relative_dir}")
            for name in names:
                if Path(name).is_absolute():
                    continue
                relative = relative_dir / name
                if relative.is_absolute() or ".." in relative.parts:
                    raise ClosureError(f"go list input escapes the repository: {relative}")
                entries.add(relative)
    if not entries:
        raise ClosureError("go list returned no repository Go inputs for race-short")
    return entries


def race_closure_files(root: Path) -> tuple[Path, ...]:
    packages = race_packages(root)
    entries = race_go_inputs(root, packages)
    entries.update(
        {
            Path("go.mod"),
            Path("go.sum"),
            Path("Makefile"),
            RACE_SCRIPT,
            RACE_TARGETS,
            CACHE_HELPER,
        }
    )
    go_work = root / "go.work"
    if go_work.exists() or go_work.is_symlink():
        entries.add(Path("go.work"))
    entries.update(walked_files(root, Path("internal/racefixture")))
    entries.update(path.relative_to(root) for path in (root / "tools/gate").glob("race_prove*"))
    for relative in entries:
        read_regular(root, relative)
    return tuple(sorted(entries, key=lambda item: os.fsencode(str(item))))


def closure_files(root: Path, spec: ClosureSpec) -> tuple[Path, ...]:
    return race_closure_files(root)


def update_field(digest: "hashlib._Hash", value: bytes) -> None:
    digest.update(len(value).to_bytes(8, "big"))
    digest.update(value)


def closure_hash(root: Path, spec: ClosureSpec) -> str:
    digest = hashlib.sha256(spec.hash_domain)
    for relative in closure_files(root, spec):
        candidate = root / relative
        metadata = candidate.lstat()
        update_field(digest, b"file")
        update_field(digest, os.fsencode(str(relative)))
        update_field(digest, f"{stat.S_IMODE(metadata.st_mode):04o}".encode("ascii"))
        update_field(digest, read_regular(root, relative))
    return digest.hexdigest()


def stable_closure_hash(root: Path, spec: ClosureSpec) -> str:
    first = closure_hash(root, spec)
    second = closure_hash(root, spec)
    if first != second:
        raise ClosureError("closure changed while it was being hashed")
    return first


def cache_directory(root: Path, spec: ClosureSpec) -> Path:
    override = os.environ.get("GATE_STEP_CACHE_DIR")
    if override is None:
        override = os.environ.get("GATE_SLOW_ORCHESTRATION_CACHE_DIR")
    if override:
        base = Path(override).absolute()
    else:
        common = Path(git_output(root, "rev-parse", "--path-format=absolute", "--git-common-dir"))
        base = common.resolve(strict=True).parent / ".agent-evidence/slow-orchestration-cache"
    return base / spec.name


def receipt_path(root: Path, spec: ClosureSpec, digest: str) -> Path:
    if HASH_RE.fullmatch(digest) is None:
        raise ClosureError("invalid closure hash")
    return cache_directory(root, spec) / f"{digest}.json"


def valid_timestamp(value: object) -> bool:
    if not isinstance(value, str) or TIMESTAMP_RE.fullmatch(value) is None:
        return False
    try:
        dt.datetime.strptime(value, "%Y%m%dT%H%M%SZ")
    except ValueError:
        return False
    return True


def receipt_proof(root: Path, spec: ClosureSpec) -> dict[str, object]:
    return {"packages": list(race_packages(root))}


def lookup_receipt(root: Path, spec: ClosureSpec, digest: str) -> dict[str, object]:
    if HASH_RE.fullmatch(digest) is None:
        raise ClosureError("invalid closure hash")
    directory = cache_directory(root, spec)
    entry = directory / f"{digest}.json"
    try:
        directory_metadata = directory.lstat()
        directory_mode = stat.S_IMODE(directory_metadata.st_mode)
        if not stat.S_ISDIR(directory_metadata.st_mode) or directory_mode & 0o077:
            raise ClosureError("cache directory is not a private real directory")
        metadata = entry.lstat()
        mode = stat.S_IMODE(metadata.st_mode)
        if not stat.S_ISREG(metadata.st_mode) or not mode & 0o400 or mode & 0o077:
            raise ClosureError("cache receipt is not a private regular file")
        if metadata.st_size > 64 * 1024:
            raise ClosureError("cache receipt is unexpectedly large")
        payload = json.loads(entry.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise ClosureError(f"cache receipt is unreadable: {entry}") from exc

    expected_keys = {
        "schema_version",
        "closure_name",
        "closure_hash",
        "head_sha",
        "timestamp_utc",
        "receipt",
    }
    expected_receipt = {
        "status": "pass",
        "step": spec.step_name,
        **receipt_proof(root, spec),
    }
    head = payload.get("head_sha") if isinstance(payload, dict) else None
    if (
        not isinstance(payload, dict)
        or set(payload) != expected_keys
        or payload.get("schema_version") != SCHEMA_VERSION
        or payload.get("closure_name") != spec.name
        or payload.get("closure_hash") != digest
        or not isinstance(head, str)
        or HEAD_RE.fullmatch(head) is None
        or not valid_timestamp(payload.get("timestamp_utc"))
        or payload.get("receipt") != expected_receipt
    ):
        raise ClosureError("cache receipt does not prove this closure")
    git_output(root, "cat-file", "-e", f"{head}^{{commit}}")
    return payload


def lookup(root: Path, spec: ClosureSpec, digest: str) -> str:
    return str(lookup_receipt(root, spec, digest)["head_sha"])


def record(root: Path, spec: ClosureSpec, digest: str, head: str) -> None:
    if HASH_RE.fullmatch(digest) is None:
        raise ClosureError("invalid closure hash")
    if HEAD_RE.fullmatch(head) is None:
        raise ClosureError("invalid head SHA")
    git_output(root, "cat-file", "-e", f"{head}^{{commit}}")
    directory = cache_directory(root, spec)
    try:
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        if directory.is_symlink() or not directory.is_dir():
            raise ClosureError("cache path is not a real directory")
        directory.chmod(0o700)
        entry = receipt_path(root, spec, digest)
        payload = {
            "schema_version": SCHEMA_VERSION,
            "closure_name": spec.name,
            "closure_hash": digest,
            "head_sha": head,
            "timestamp_utc": dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ"),
            "receipt": {
                "status": "pass",
                "step": spec.step_name,
                **receipt_proof(root, spec),
            },
        }
        encoded = (json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n").encode()
        descriptor, temporary = tempfile.mkstemp(prefix=f".{digest}.", dir=directory)
        try:
            os.fchmod(descriptor, 0o600)
            with os.fdopen(descriptor, "wb") as output:
                descriptor = -1
                output.write(encoded)
                output.flush()
                os.fsync(output.fileno())
            os.replace(temporary, entry)
            directory_descriptor = os.open(directory, os.O_RDONLY)
            try:
                os.fsync(directory_descriptor)
            finally:
                os.close(directory_descriptor)
        finally:
            if descriptor >= 0:
                os.close(descriptor)
            try:
                os.unlink(temporary)
            except FileNotFoundError:
                pass
    except OSError as exc:
        raise ClosureError(f"cannot write cache receipt in {directory}: {exc}") from exc


def release_mode() -> bool:
    return os.environ.get("GATE_MODE") == "release" or os.environ.get("GITHUB_WORKFLOW") == "release-gate"


def run_fresh(command: list[str]) -> int:
    try:
        return subprocess.run(command, check=False).returncode
    except OSError as exc:
        print(f"gate-step cache: cannot execute {command[0]}: {exc}", file=sys.stderr)
        return 127


def run_cached(root: Path, spec: ClosureSpec, command: list[str]) -> int:
    if release_mode() or os.environ.get("EXPLAIN") == "1":
        return run_fresh(command)
    try:
        digest = stable_closure_hash(root, spec)
        head = git_output(root, "rev-parse", "--verify", "HEAD^{commit}")
    except ClosureError:
        return run_fresh(command)
    try:
        payload = lookup_receipt(root, spec, digest)
        verified_digest = stable_closure_hash(root, spec)
        verified_head = git_output(root, "rev-parse", "--verify", "HEAD^{commit}")
        if verified_digest == digest and verified_head == head:
            print(
                f"PASS {spec.step_name} (cached: {digest} from {payload['head_sha']}; "
                f"green: {payload['timestamp_utc']})"
            )
            return 0
    except ClosureError:
        pass

    status = run_fresh(command)
    if status != 0:
        return status
    try:
        verified_digest = stable_closure_hash(root, spec)
        verified_head = git_output(root, "rev-parse", "--verify", "HEAD^{commit}")
        if verified_digest != digest or verified_head != head:
            raise ClosureError("inputs changed during the fresh run; refusing a mixed-input green")
        record(root, spec, digest, head)
    except ClosureError as exc:
        print(f"gate-step cache: {exc}", file=sys.stderr)
        return 1
    return 0


def add_common_args(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--root", required=True)
    parser.add_argument("--cache", required=True, choices=tuple(SPECS))


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    for command in ("manifest", "hash"):
        child = subparsers.add_parser(command)
        add_common_args(child)
    lookup_parser = subparsers.add_parser("lookup")
    add_common_args(lookup_parser)
    lookup_parser.add_argument("closure_hash")
    record_parser = subparsers.add_parser("record")
    add_common_args(record_parser)
    record_parser.add_argument("closure_hash")
    record_parser.add_argument("head_sha")
    run_parser = subparsers.add_parser("run")
    add_common_args(run_parser)
    run_parser.add_argument("run_command", nargs=argparse.REMAINDER)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        root = repository_root(args.root)
        spec = SPECS[args.cache]
        if args.command == "manifest":
            for relative in closure_files(root, spec):
                print(relative)
        elif args.command == "hash":
            print(stable_closure_hash(root, spec))
        elif args.command == "lookup":
            print(lookup(root, spec, args.closure_hash))
        elif args.command == "record":
            record(root, spec, args.closure_hash, args.head_sha)
        else:
            command = args.run_command
            if command[:1] == ["--"]:
                command = command[1:]
            if not command:
                raise ClosureError("run requires a command after --")
            return run_cached(root, spec, command)
    except ClosureError as exc:
        print(f"gate-step cache: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
