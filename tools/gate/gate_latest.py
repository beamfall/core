#!/usr/bin/env python3
"""Run one latest-wins gate per worktree and one gate per Git repository.

Generic descendants must remain in the worker process group. The canonical gate
opts into nested-session cleanup and runs this coordinator beneath an independent
parent-loss guardian. If the lock-holding coordinator is SIGKILLed, that guardian
retires every tagged descendant before any inherited lock can become available.
"""

from __future__ import annotations

from contextlib import ExitStack
import fcntl
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time


SUPERSEDED_EXIT = 75
POLL_SECONDS = 0.05
ROOT = Path(__file__).resolve().parents[2]
COORDINATOR_SUPERVISED_FLAG = "GATE_LATEST_COORDINATOR_SUPERVISED"
CAPACITY_LOCK_FD = "BEAMFALL_GATE_CAPACITY_FD"
PROCESS_SUPERVISOR_SIGNAL_GRACE = (
    "BEAMFALL_PROCESS_SUPERVISOR_SIGNAL_GRACE_SECONDS"
)


def git_common_dir() -> Path:
    common_dir = subprocess.run(
        [
            "git",
            "rev-parse",
            "--path-format=absolute",
            "--git-common-dir",
        ],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()
    return Path(common_dir).resolve(strict=True)


def read_generation(path: Path) -> int:
    try:
        raw = path.read_text(encoding="utf-8").strip()
    except FileNotFoundError:
        return 0
    generation = int(raw)
    if generation < 0:
        raise ValueError(f"negative gate generation in {path}")
    return generation


def write_generation(path: Path, generation: int) -> None:
    fd, temporary = tempfile.mkstemp(prefix="generation.", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            stream.write(f"{generation}\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


def default_gocache_dir() -> Path:
    # GATE-GOCACHE-PERSIST-1: repo-root .gocache is the reserved, gitignored
    # convention (see .gitignore: "Local Go build cache (GOCACHE=.gocache;
    # never commit)"). GATE_LATEST_GOCACHE_DIR overrides it, mirroring
    # GATE_LATEST_STATE_DIR's override for hermetic tests.
    return Path(os.environ.get("GATE_LATEST_GOCACHE_DIR", str(ROOT / ".gocache")))


def allocate_generation(control_lock: Path, generation_path: Path) -> int:
    with control_lock.open("a+b") as stream:
        fcntl.flock(stream, fcntl.LOCK_EX)
        generation = read_generation(generation_path) + 1
        write_generation(generation_path, generation)
        return generation


def acquire_if_latest(stream, generation: int, generation_path: Path, stopped) -> bool:
    while not stopped():
        if read_generation(generation_path) != generation:
            return False
        try:
            fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
            return read_generation(generation_path) == generation
        except BlockingIOError:
            time.sleep(POLL_SECONDS)
    return False


def group_exists(process_group: int) -> bool:
    try:
        os.killpg(process_group, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        # Darwin reports EPERM when the unreaped group leader is already a
        # zombie and no signalable group member remains.
        return False


def signal_group(process_group: int, signum: int) -> None:
    try:
        os.killpg(process_group, signum)
    except ProcessLookupError:
        pass


def stop_worker(
    process: subprocess.Popen[bytes],
    first_signal: int,
    grace: float,
    worker_reaps_descendants: bool = False,
) -> int:
    process_group = process.pid
    signal_group(process_group, first_signal)
    if not worker_reaps_descendants:
        deadline = time.monotonic() + grace
        while group_exists(process_group) and time.monotonic() < deadline:
            time.sleep(min(POLL_SECONDS, max(0.0, deadline - time.monotonic())))
        if group_exists(process_group):
            signal_group(process_group, signal.SIGKILL)
    returncode = process.wait()
    while group_exists(process_group):
        time.sleep(POLL_SECONDS)
    return returncode


def finish_worker(
    process: subprocess.Popen[bytes], grace: float, worker_reaps_descendants: bool
) -> None:
    if group_exists(process.pid):
        stop_worker(process, signal.SIGTERM, grace, worker_reaps_descendants)
    else:
        process.wait()


def exit_status(returncode: int) -> int:
    if returncode >= 0:
        return returncode
    return 128 - returncode


def supervise_canonical_coordinator(command: list[str]) -> None:
    """Re-exec canonical coordinators beneath the parent-loss guardian."""
    if os.environ.get("GATE_WORKER_REAPS_DESCENDANTS") != "1":
        return
    if os.environ.get(COORDINATOR_SUPERVISED_FLAG) == "1":
        return
    supervisor = Path(
        os.environ.get(
            "GATE_PROCESS_SUPERVISOR",
            ROOT / "script" / "test-process-supervisor.py",
        )
    ).resolve()
    if not supervisor.is_file():
        raise OSError(f"gate process supervisor missing: {supervisor}")
    environment = os.environ.copy()
    os.execvpe(
        sys.executable,
        [
            sys.executable,
            str(supervisor),
            COORDINATOR_SUPERVISED_FLAG,
            sys.executable,
            str(Path(__file__).resolve()),
            "--",
            *command,
        ],
        environment,
    )


def supervise(command: list[str]) -> int:
    state_dir = Path(
        os.environ.get("GATE_LATEST_STATE_DIR", ROOT / ".agent-evidence" / "gate-latest")
    )
    state_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    generation_path = state_dir / "generation"
    generation = allocate_generation(state_dir / "control.lock", generation_path)
    grace = float(os.environ.get("GATE_CANCEL_GRACE_SECONDS", "2"))
    if grace < 0:
        raise ValueError("GATE_CANCEL_GRACE_SECONDS must be non-negative")
    worker_reaps_descendants = (
        os.environ.get("GATE_WORKER_REAPS_DESCENDANTS") == "1"
    )

    received_signal = 0

    def remember_signal(signum, _frame) -> None:
        nonlocal received_signal
        if not received_signal:
            received_signal = signum

    handled = (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)
    previous_handlers = {signum: signal.signal(signum, remember_signal) for signum in handled}
    try:
        capacity_fd = os.open(
            git_common_dir() / "beamfall-gate.capacity.lock",
            os.O_CLOEXEC | os.O_CREAT | os.O_RDWR,
            0o600,
        )
        with ExitStack() as locks:
            capacity_lock = locks.enter_context(os.fdopen(capacity_fd, "a+b"))
            if not acquire_if_latest(
                capacity_lock,
                generation,
                generation_path,
                lambda: bool(received_signal),
            ):
                if received_signal:
                    return 128 + received_signal
                print(
                    f"gate: run {generation} superseded before start by run "
                    f"{read_generation(generation_path)}",
                    file=sys.stderr,
                )
                return SUPERSEDED_EXIT

            run_lock = locks.enter_context((state_dir / "run.lock").open("a+b"))
            if not acquire_if_latest(
                run_lock,
                generation,
                generation_path,
                lambda: bool(received_signal),
            ):
                if received_signal:
                    return 128 + received_signal
                print(
                    f"gate: run {generation} superseded before start by run "
                    f"{read_generation(generation_path)}",
                    file=sys.stderr,
                )
                return SUPERSEDED_EXIT

            environment = os.environ.copy()
            environment.pop("GATE_WORKER_REAPS_DESCENDANTS", None)
            environment.pop(COORDINATOR_SUPERVISED_FLAG, None)
            environment.pop(PROCESS_SUPERVISOR_SIGNAL_GRACE, None)
            environment["GATE_RUN_ID"] = str(generation)
            environment[CAPACITY_LOCK_FD] = str(capacity_lock.fileno())
            process = None
            try:
                if "GOCACHE" not in os.environ:
                    # Environment presence is the coordinator boundary: even an
                    # explicit empty value is forwarded byte-for-byte. Default
                    # to a persistent, gitignored cache dir (reused, never
                    # deleted here) instead of a fresh-then-discarded tempdir,
                    # so warm `go build`/`go test` reuse survives across gate
                    # runs. Safe to share across concurrent repositories: the
                    # Go build/test cache is designed for concurrent access
                    # (internally lock-file guarded). This makes every gate
                    # `go test` call's own result cache load-bearing, which is
                    # why every such call under tools/ and script/ must carry
                    # -count=1 (audited by tools/gate/go-test-count1-check.sh)
                    # so a warm cache can never silently replay a stale PASS.
                    default_cache = default_gocache_dir()
                    default_cache.mkdir(parents=True, exist_ok=True)
                    environment["GOCACHE"] = str(default_cache.resolve())
                process = subprocess.Popen(
                    command,
                    cwd=ROOT,
                    env=environment,
                    pass_fds=(capacity_lock.fileno(), run_lock.fileno()),
                    start_new_session=True,
                )
                while True:
                    if received_signal:
                        stop_worker(
                            process,
                            received_signal,
                            grace,
                            worker_reaps_descendants,
                        )
                        return 128 + received_signal

                    desired = read_generation(generation_path)
                    if desired != generation:
                        print(
                            f"gate: run {generation} superseded by run {desired}; cancelling",
                            file=sys.stderr,
                        )
                        stop_worker(
                            process,
                            signal.SIGTERM,
                            grace,
                            worker_reaps_descendants,
                        )
                        return SUPERSEDED_EXIT

                    returncode = process.poll()
                    if returncode is not None:
                        if read_generation(generation_path) != generation:
                            return SUPERSEDED_EXIT
                        return exit_status(returncode)
                    time.sleep(POLL_SECONDS)
            finally:
                if process is not None:
                    finish_worker(process, grace, worker_reaps_descendants)
    finally:
        for signum, previous in previous_handlers.items():
            signal.signal(signum, previous)


def main() -> int:
    command = sys.argv[1:]
    if command[:1] == ["--"]:
        command = command[1:]
    if not command:
        print("usage: gate_latest.py -- COMMAND [ARG ...]", file=sys.stderr)
        return 64
    try:
        supervise_canonical_coordinator(command)
        return supervise(command)
    except (OSError, subprocess.SubprocessError, ValueError) as error:
        if os.environ.get("GATE_LATEST_DEBUG"):
            raise
        print(f"gate coordinator: {error}", file=sys.stderr)
        return 70


if __name__ == "__main__":
    raise SystemExit(main())
