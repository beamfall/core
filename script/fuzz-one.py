#!/usr/bin/env python3
"""Run one manifest-vetted fuzz target without shell interpolation."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
from typing import Sequence


SAFE_PACKAGE = re.compile(r"^\./[A-Za-z0-9_./-]+$")
SAFE_TARGET = re.compile(r"^Fuzz[A-Za-z0-9_]+$")
SAFE_TAG = re.compile(r"^[A-Za-z0-9_]+$")
SAFE_DURATION = re.compile(r"^[1-9][0-9]*(?:ns|us|ms|s|m|h)$")
KNOWN_GOOS = frozenset("aix android darwin dragonfly freebsd illumos ios js linux netbsd openbsd plan9 solaris wasip1 windows".split())
KNOWN_GOARCH = frozenset("386 amd64 arm arm64 loong64 mips mips64 mips64le mipsle ppc64 ppc64le riscv64 s390x wasm".split())


def checked_values(name: str, values: object, allowed: frozenset[str] | None = None) -> list[str]:
    if not isinstance(values, list) or any(not isinstance(value, str) for value in values):
        raise ValueError(f"invalid fuzz {name}")
    if len(values) != len(set(values)):
        raise ValueError(f"duplicate fuzz {name}")
    if allowed is not None and any(value not in allowed for value in values):
        raise ValueError(f"invalid fuzz {name}")
    return values


def validated(
    package: str,
    target: str,
    tags: object,
    goos: object,
    goarch: object,
    duration: str,
) -> tuple[list[str], list[str], list[str]]:
    if not SAFE_PACKAGE.fullmatch(package) or ".." in Path(package).parts:
        raise ValueError("invalid fuzz package")
    if not SAFE_TARGET.fullmatch(target):
        raise ValueError("invalid fuzz target")
    checked_tags = checked_values("tags", tags)
    if any(not SAFE_TAG.fullmatch(tag) or tag in KNOWN_GOOS or tag in KNOWN_GOARCH for tag in checked_tags):
        raise ValueError("invalid fuzz tags")
    checked_goos = checked_values("GOOS selectors", goos, KNOWN_GOOS)
    checked_goarch = checked_values("GOARCH selectors", goarch, KNOWN_GOARCH)
    if not SAFE_DURATION.fullmatch(duration):
        raise ValueError("invalid fuzz duration")
    return checked_tags, checked_goos, checked_goarch


def native_platform(root: Path) -> tuple[str, str]:
    values = subprocess.check_output(["go", "env", "GOHOSTOS", "GOHOSTARCH"], cwd=root, text=True).splitlines()
    if len(values) != 2 or values[0] not in KNOWN_GOOS or values[1] not in KNOWN_GOARCH:
        raise ValueError("could not determine native Go platform")
    return values[0], values[1]


def run_one(
    root: Path,
    package: str,
    target: str,
    tags: object,
    goos: object,
    goarch: object,
    duration: str,
    *,
    active: bool,
    native: tuple[str, str] | None = None,
) -> bool:
    checked_tags, checked_goos, checked_goarch = validated(package, target, tags, goos, goarch, duration)
    native_goos, native_goarch = native or native_platform(root)
    if (checked_goos and native_goos not in checked_goos) or (checked_goarch and native_goarch not in checked_goarch):
        print(f"fuzz skip {package} {target}: native platform {native_goos}/{native_goarch}", flush=True)
        return False
    command = ["go", "test", "-count=1"]
    if checked_tags:
        command.extend(["-tags", ",".join(checked_tags)])
    if active:
        command.extend(["-run=^$", "-fuzz=" + target, "-fuzztime=" + duration])
    else:
        command.append("-run=^" + target + "$")
    command.append(package + "/")
    subprocess.run(command, cwd=root, check=True)
    return True


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--duration", default="5m")
    args = parser.parse_args(argv)
    try:
        tags = json.loads(os.environ.get("FUZZ_TAGS_JSON", "[]"))
        goos = json.loads(os.environ.get("FUZZ_GOOS_JSON", "[]"))
        goarch = json.loads(os.environ.get("FUZZ_GOARCH_JSON", "[]"))
        run_one(
            Path(__file__).resolve().parent.parent,
            os.environ.get("FUZZ_PACKAGE", ""),
            os.environ.get("FUZZ_TARGET", ""),
            tags,
            goos,
            goarch,
            args.duration,
            active=True,
        )
    except (json.JSONDecodeError, ValueError) as error:
        parser.error(str(error))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
