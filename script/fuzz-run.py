#!/usr/bin/env python3
from __future__ import annotations

import json
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor


ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("fuzz_one", ROOT / "script/fuzz-one.py")
assert SPEC and SPEC.loader
FUZZ_ONE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(FUZZ_ONE)


def positive_int(name: str, default: str) -> int:
    raw = os.environ.get(name, default)
    if not raw.isdecimal() or int(raw) < 1:
        raise SystemExit(f"fuzz-run: {name} must be a positive integer")
    return int(raw)


def main() -> int:
    if sys.argv[1:] not in (["seed"], ["active"]):
        print(f"usage: {sys.argv[0]} seed|active", file=sys.stderr)
        return 2
    root = ROOT
    output = subprocess.check_output(
        ["go", "run", "./tools/fuzzmanifest", "-mode", "matrix"], cwd=root, text=True
    )
    targets = json.loads(output)["include"]
    jobs = positive_int("FUZZ_JOBS", "2")
    active = sys.argv[1] == "active"
    fuzz_time = os.environ.get("FUZZ_TIME", "10s")
    native = FUZZ_ONE.native_platform(root)

    def run(item: dict[str, object]) -> None:
        print("fuzz " + str(item["pkg"]) + " " + str(item["target"]), flush=True)
        FUZZ_ONE.run_one(
            root,
            item["pkg"],
            item["target"],
            item.get("tags", []),
            item.get("goos", []),
            item.get("goarch", []),
            fuzz_time,
            active=active,
            native=native,
        )

    with ThreadPoolExecutor(max_workers=jobs) as pool:
        futures = [pool.submit(run, item) for item in targets]
        for future in futures:
            future.result()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
