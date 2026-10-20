#!/usr/bin/env python3
"""Strict parser, validator, and canonical generator for SHA-256 manifests."""

from __future__ import annotations

import argparse
import hashlib
import posixpath
import re
import sys
from pathlib import Path, PurePosixPath

_DATA_LINE = re.compile(r"^([0-9a-f]{64})  ([^\r\n]+)$")


def normalize_manifest_path(raw: str) -> str:
    """Return a confined POSIX relative path or raise ValueError."""
    if not raw or raw != raw.strip():
        raise ValueError("path must be non-empty and have no surrounding whitespace")
    if "\\" in raw:
        raise ValueError(f"unsafe path separator in {raw!r}")
    if raw.startswith("/"):
        raise ValueError(f"absolute path is not allowed: {raw!r}")

    while raw.startswith("./"):
        raw = raw[2:]
    if ".." in PurePosixPath(raw).parts:
        raise ValueError(f"path traversal is not allowed: {raw!r}")
    normalized = posixpath.normpath(raw)
    if normalized in ("", ".", "..") or normalized.startswith("../"):
        raise ValueError(f"path escapes the manifest root: {raw!r}")
    if PurePosixPath(normalized).is_absolute():
        raise ValueError(f"absolute path is not allowed: {raw!r}")
    return normalized


def parse_manifest_text(text: str) -> dict[str, str]:
    """Parse strict ``<lowercase hex>  <relative path>`` manifest text."""
    entries: dict[str, str] = {}
    for number, line in enumerate(text.splitlines(), start=1):
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        match = _DATA_LINE.fullmatch(line)
        if match is None:
            raise ValueError(f"line {number}: expected 64 lowercase hex digits, two spaces, and a path")
        digest, raw_path = match.groups()
        try:
            path = normalize_manifest_path(raw_path)
        except ValueError as exc:
            raise ValueError(f"line {number}: {exc}") from exc
        if path in entries:
            raise ValueError(f"line {number}: duplicate normalized path {path!r}")
        entries[path] = digest
    return entries


def parse_manifest(path: Path) -> dict[str, str]:
    return parse_manifest_text(path.read_text(encoding="utf-8"))


def render_manifest(root: Path, paths: list[str], comments: list[str] | None = None) -> str:
    """Hash named files below root and return stable, validated manifest text."""
    normalized: dict[str, Path] = {}
    resolved_root = root.resolve()
    for raw_path in paths:
        path = normalize_manifest_path(raw_path)
        if path in normalized:
            raise ValueError(f"duplicate normalized path {path!r}")
        candidate = (resolved_root / Path(*PurePosixPath(path).parts)).resolve()
        try:
            candidate.relative_to(resolved_root)
        except ValueError as exc:
            raise ValueError(f"path escapes the manifest root: {raw_path!r}") from exc
        if not candidate.is_file():
            raise ValueError(f"manifest input is not a file: {raw_path!r}")
        normalized[path] = candidate

    lines = [f"# {comment}" for comment in comments or []]
    lines.extend(
        f"{hashlib.sha256(normalized[path].read_bytes()).hexdigest()}  {path}"
        for path in sorted(normalized)
    )
    output = "\n".join(lines) + "\n"
    parse_manifest_text(output)
    return output


def _paths_from_args(root: Path, names: list[str], patterns: list[str]) -> list[str]:
    paths = list(names)
    for pattern in patterns:
        paths.extend(
            path.relative_to(root).as_posix()
            for path in root.glob(pattern)
            if path.is_file()
        )
    if not paths:
        raise ValueError("no manifest inputs selected")
    return paths


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    validate = subparsers.add_parser("validate")
    validate.add_argument("manifest", type=Path)
    generate = subparsers.add_parser("generate")
    generate.add_argument("--root", required=True, type=Path)
    generate.add_argument("--glob", action="append", default=[])
    generate.add_argument("--comment", action="append", default=[])
    generate.add_argument("paths", nargs="*")
    args = parser.parse_args(argv)

    try:
        if args.command == "validate":
            parse_manifest(args.manifest)
        else:
            paths = _paths_from_args(args.root, args.paths, args.glob)
            sys.stdout.write(render_manifest(args.root, paths, args.comment))
    except (OSError, ValueError) as exc:
        parser.error(str(exc))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
