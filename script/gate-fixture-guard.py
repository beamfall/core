#!/usr/bin/env python3
"""Run a gate command while proving fixture-writer controls stayed disabled."""

from __future__ import annotations

import hashlib
import os
from pathlib import Path
import signal
import subprocess
import sys
import time


WRITER_ENV = (
    "BEAMFALL_UPDATE_AUDIOFRAME_GOLDENS",
    "BEAMFALL_UPDATE_GOLDEN",
    "BEAMFALL_UPDATE_VISUAL_DIRECTOR_PACK",
    "BEAMFALL_UPDATE_WRAP_VECTORS",
)
SCOPES = (
    "internal/conformance/testdata",
    "tools/visualsync/contract/testdata",
)


def tree_digest(root: Path, relative: str) -> str:
    scope = root / relative
    digest = hashlib.sha256()
    if not scope.exists():
        digest.update(b"missing\0")
        return digest.hexdigest()
    paths = [scope] if scope.is_file() else sorted(scope.rglob("*"))
    for path in paths:
        rel = path.relative_to(root).as_posix().encode()
        if path.is_symlink():
            digest.update(b"L\0" + rel + b"\0" + os.readlink(path).encode() + b"\0")
        elif path.is_file():
            digest.update(b"F\0" + rel + b"\0")
            with path.open("rb") as stream:
                for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                    digest.update(chunk)
            digest.update(b"\0")
        elif path.is_dir():
            digest.update(b"D\0" + rel + b"\0")
    return digest.hexdigest()


def snapshot(root: Path, scopes: tuple[str, ...] = SCOPES) -> dict[str, str]:
    return {scope: tree_digest(root, scope) for scope in scopes}


def group_alive(group_id: int) -> bool:
    try:
        os.killpg(group_id, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def signal_group(group_id: int, signum: int) -> None:
    try:
        os.killpg(group_id, signum)
    except (ProcessLookupError, PermissionError):
        pass


def cleanup_group(group_id: int) -> tuple[bool, bool]:
    """Return (had_residual, fully_reaped) after bounded group cleanup."""
    if not group_alive(group_id):
        return False, True
    signal_group(group_id, signal.SIGTERM)
    signal_group(group_id, signal.SIGCONT)
    for _ in range(20):
        if not group_alive(group_id):
            return True, True
        time.sleep(0.025)
    signal_group(group_id, signal.SIGKILL)
    for _ in range(80):
        if not group_alive(group_id):
            return True, True
        time.sleep(0.025)
    return True, False


def run_guarded(root: Path, scopes: tuple[str, ...], command: list[str], env: dict[str, str]) -> int:
    before = snapshot(root, scopes)
    child = subprocess.Popen(command, cwd=root, env=env, start_new_session=True)
    received = 0
    received_at = 0.0

    def forward(signum: int, _frame: object) -> None:
        nonlocal received, received_at
        received = signum
        if not received_at:
            received_at = time.monotonic()
        signal_group(child.pid, signum)

    old_handlers = {
        sig: signal.signal(sig, forward) for sig in (signal.SIGINT, signal.SIGTERM)
    }
    cleanup_ok = True
    had_residual = False
    try:
        while child.poll() is None:
            if received_at and time.monotonic() - received_at >= 1.0:
                signal_group(child.pid, signal.SIGKILL)
            time.sleep(0.025)
        status = child.wait()
    finally:
        had_residual, cleanup_ok = cleanup_group(child.pid)
        for sig, handler in old_handlers.items():
            signal.signal(sig, handler)
        after = snapshot(root, scopes)
    changed = [scope for scope in scopes if before[scope] != after[scope]]
    if changed:
        print(
            "gate fixture guard: fixture tree changed during gate: " + ", ".join(changed),
            file=sys.stderr,
        )
        return 1
    if not cleanup_ok:
        print("gate fixture guard: could not reap the gate process group", file=sys.stderr)
        return 1
    if had_residual and status == 0:
        print("gate fixture guard: reaped residual gate descendants", file=sys.stderr)
        return 1
    if received:
        return 128 + received
    return status


def main() -> int:
    if sys.argv[1:2] != ["--"] or len(sys.argv) < 3:
        print(f"usage: {sys.argv[0]} -- COMMAND [ARG...]", file=sys.stderr)
        return 2
    enabled = [name for name in WRITER_ENV if os.environ.get(name)]
    if enabled:
        print(
            "gate fixture guard: fixture writers must be unset: " + ", ".join(enabled),
            file=sys.stderr,
        )
        return 2
    root = Path(__file__).resolve().parent.parent
    env = os.environ.copy()
    env["BEAMFALL_GATE_FIXTURE_GUARDED"] = "1"
    return run_guarded(root, SCOPES, sys.argv[2:], env)


if __name__ == "__main__":
    raise SystemExit(main())
