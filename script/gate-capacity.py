#!/usr/bin/env python3
"""Hold one canonical gate per Git repository."""

from __future__ import annotations

import errno
import fcntl
import os
from pathlib import Path
import signal
import subprocess
import sys
import time


POLL_SECONDS = 0.05
CAPACITY_LOCK_FD = "BEAMFALL_GATE_CAPACITY_FD"


def git_common_dir(cwd: Path | None = None) -> Path:
    common = subprocess.run(
        [
            "git",
            "rev-parse",
            "--path-format=absolute",
            "--git-common-dir",
        ],
        check=True,
        capture_output=True,
        cwd=cwd or Path.cwd(),
        text=True,
    ).stdout.strip()
    return Path(common).resolve(strict=True)


def held(cwd: Path) -> int:
    lock = os.open(
        git_common_dir(cwd) / "beamfall-gate.capacity.lock",
        os.O_CLOEXEC | os.O_CREAT | os.O_RDWR,
        0o600,
    )
    try:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return 0
        return 1
    finally:
        os.close(lock)


def inherited_lock(lock_path: Path) -> int | None:
    raw_fd = os.environ.get(CAPACITY_LOCK_FD)
    if raw_fd is None:
        return None
    if not raw_fd.isdecimal():
        raise OSError(errno.EBADF, f"invalid {CAPACITY_LOCK_FD}: {raw_fd!r}")
    lock = int(raw_fd)
    inherited = os.fstat(lock)
    try:
        canonical = lock_path.stat()
    except FileNotFoundError:
        # This repo's lock file does not exist yet, so the inherited FD cannot
        # be it — it is an outer gate for another repository.
        return None
    if (inherited.st_dev, inherited.st_ino) != (canonical.st_dev, canonical.st_ino):
        # An FD naming a different lock file is an OUTER gate for another
        # repository (a nested gate), not corruption: acquire this repo's own
        # lock instead of treating the foreign FD as ours.
        return None
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError as error:
        raise OSError(errno.EBUSY, f"{CAPACITY_LOCK_FD} does not own {lock_path}") from error
    return lock


def run(command: list[str]) -> int:
    received_signal = 0

    def remember_signal(signum: int, _frame: object) -> None:
        nonlocal received_signal
        if not received_signal:
            received_signal = signum

    handled = (signal.SIGHUP, signal.SIGINT, signal.SIGTERM)
    for signum in handled:
        signal.signal(signum, remember_signal)
    signal.pthread_sigmask(signal.SIG_UNBLOCK, handled)
    if received_signal:
        return 128 + received_signal
    lock_path = git_common_dir() / "beamfall-gate.capacity.lock"
    lock = inherited_lock(lock_path)
    owns_inherited_lock = lock is not None
    if lock is None:
        lock = os.open(
            lock_path,
            os.O_CLOEXEC | os.O_CREAT | os.O_RDWR,
            0o600,
        )
    try:
        if not owns_inherited_lock:
            while not received_signal:
                try:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    break
                except BlockingIOError:
                    time.sleep(POLL_SECONDS)
        if received_signal:
            return 128 + received_signal
        os.set_inheritable(lock, True)
        os.environ[CAPACITY_LOCK_FD] = str(lock)
        exec_mask = signal.pthread_sigmask(signal.SIG_BLOCK, handled)
        for signum in handled:
            signal.signal(signum, signal.SIG_DFL)
        if not received_signal:
            pending = signal.sigpending()
            received_signal = next(
                (signum for signum in handled if signum in pending), 0
            )
        if received_signal:
            return 128 + received_signal
        # Capacity cancellation owns these signals. Default dispositions plus
        # the wrapper's unblocked mask make any later interruption terminate
        # before the command starts instead of carrying ignored/pending state
        # across exec.
        signal.pthread_sigmask(signal.SIG_SETMASK, exec_mask)
        os.execvpe(command[0], command, os.environ)
    finally:
        os.close(lock)
    return 70


def main() -> int:
    command = sys.argv[1:]
    if command[:1] == ["--held"]:
        if len(command) != 2:
            print(f"usage: {sys.argv[0]} --held WORKTREE", file=sys.stderr)
            return 64
        try:
            return held(Path(command[1]))
        except (OSError, subprocess.SubprocessError) as error:
            print(f"gate capacity probe: {error}", file=sys.stderr)
            return 70
    if command[:1] == ["--"]:
        command = command[1:]
    if not command:
        print(f"usage: {sys.argv[0]} -- COMMAND [ARG ...]", file=sys.stderr)
        return 64
    try:
        return run(command)
    except (OSError, subprocess.SubprocessError) as error:
        print(f"gate capacity: {error}", file=sys.stderr)
        return 70


if __name__ == "__main__":
    raise SystemExit(main())
