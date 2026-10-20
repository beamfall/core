#!/usr/bin/env python3
"""Fail when committed generated developer API docs differ from fresh output."""

from __future__ import annotations

import filecmp
from pathlib import Path
import subprocess
import sys
import tempfile


OUTPUTS = ("reference.md", "llms.txt", "llms-full.txt")


def check(root: Path) -> list[str]:
    with tempfile.TemporaryDirectory(prefix="beamfall-api-docs-") as directory:
        output_root = Path(directory)
        subprocess.run(
            [
                str(root / "script/gen-api-docs.sh"),
                "--source-root",
                str(root),
                "--output-root",
                str(output_root),
            ],
            cwd=root,
            check=True,
            stdout=subprocess.DEVNULL,
        )
        generated = output_root / "docs/api"
        actual_names = {path.name for path in generated.iterdir() if path.is_file()}
        expected_names = set(OUTPUTS)
        errors = [f"unexpected generated output: {name}" for name in sorted(actual_names - expected_names)]
        for name in OUTPUTS:
            committed = root / "docs/api" / name
            fresh = generated / name
            if not committed.is_file():
                errors.append(f"missing committed API doc: docs/api/{name}")
            elif not fresh.is_file():
                errors.append(f"missing generated API doc: docs/api/{name}")
            elif not filecmp.cmp(committed, fresh, shallow=False):
                errors.append(f"generated API doc drift: docs/api/{name}")
        return errors


def main() -> int:
    root = Path(__file__).resolve().parent.parent
    errors = check(root)
    if errors:
        print("\n".join(errors), file=sys.stderr)
        print("rerun script/gen-api-docs.sh", file=sys.stderr)
        return 1
    print("api docs: fresh")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
