#!/usr/bin/env python3
"""Focused negative controls and generator round trips for SHA manifests."""

from __future__ import annotations

import runpy
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MODULE = runpy.run_path(str(ROOT / "script" / "sha256-manifest.py"))
PARSE = MODULE["parse_manifest_text"]


def expect_invalid(text: str) -> None:
    try:
        PARSE(text)
    except ValueError:
        return
    raise AssertionError(f"accepted invalid manifest: {text!r}")


def generated(script: str) -> str:
    completed = subprocess.run(
        [str(ROOT / "script" / script)],
        cwd=ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if completed.returncode != 0:
        raise AssertionError(completed.stderr)
    PARSE(completed.stdout)
    return completed.stdout


def main() -> int:
    digest = "a" * 64
    assert PARSE(f"\n # comment\n{digest}  ./file.json\n") == {"file.json": digest}
    for invalid in (
        f"{'A' * 64}  file.json\n",
        f"{digest} file.json\n",
        f"{digest}  ../file.json\n",
        f"{digest}  a/../file.json\n",
        f"{digest}  /file.json\n",
        f"{digest}  dir\\file.json\n",
        f"{digest}  file.json \n",
        f"{digest}  file.json\n{digest}  ./file.json\n",
        "malformed\n",
    ):
        expect_invalid(invalid)

    assert generated("gen-golden-manifest.sh") == (
        ROOT / "internal/conformance/testdata/goldens.sha256"
    ).read_text(encoding="utf-8")
    assert generated("gen-subtitle-selection-manifest.sh") == (
        ROOT / "internal/conformance/testdata/subtitle_selection/manifest.sha256"
    ).read_text(encoding="utf-8")

    with tempfile.TemporaryDirectory() as tmp:
        manifest = Path(tmp) / "bad.sha256"
        manifest.write_text("malformed\n", encoding="utf-8")
        completed = subprocess.run(
            [sys.executable, str(ROOT / "script/sha256-manifest.py"), "validate", str(manifest)],
            capture_output=True,
            text=True,
            check=False,
        )
        assert completed.returncode == 2
    print("sha256_manifest_test: PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
