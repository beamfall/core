#!/usr/bin/env python3
"""Run one latest-wins gate per worktree and one gate per Git repository.

Generic descendants must remain in the worker process group. The canonical gate
opts into nested-session cleanup and runs this coordinator beneath an independent
parent-loss guardian. If the lock-holding coordinator is SIGKILLed, that guardian
retires every tagged descendant before any inherited lock can become available.

The cheap preflight lane (GATE_PREFLIGHT=1, `make gate-preflight`) never waits
on the full-gate capacity lock (CORE-1321): queued behind ~37-minute gates it
cost a whole slot for a seconds-scale check. It takes its own repository-wide
preflight lock and keeps its latest-wins generation apart from full gates, so a
preflight never supersedes a full gate in the same worktree. Every lock wait is
announced on stderr with the recorded holder, then again periodically.

A full gate first queues for the machine-wide gate slot (script/gate-slot.py,
CORE-1470), which every repository's gate shares, and holds it until its worker
and the worker's descendants are gone. A gate already running under the slot's
holder does not queue again, and a preflight never queues.
"""

from __future__ import annotations

from contextlib import ExitStack
import errno
import fcntl
import importlib.util
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time


SUPERSEDED_EXIT = 75
POLL_SECONDS = 0.05
WAIT_NOTICE_SECONDS = 30.0
ROOT = Path(__file__).resolve().parents[2]
COORDINATOR_SUPERVISED_FLAG = "GATE_LATEST_COORDINATOR_SUPERVISED"
CAPACITY_LOCK_FD = "BEAMFALL_GATE_CAPACITY_FD"
MACHINE_SLOT = ROOT / "script" / "gate-slot.py"
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


def process_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def lock_holder(stream) -> str:
    """Describe the holder a coordinator recorded in the lock file, if any.

    Only this coordinator records itself; a holder that does not (for example
    `script/gate-capacity.py`) or a record left by a SIGKILLed coordinator is
    reported as unrecorded rather than named wrongly.
    """
    try:
        stream.seek(0)
        record = stream.read(512).decode("utf-8", errors="replace").strip()
    except OSError:
        record = ""
    pid_field = record.split(" ", 2)[1:2] if record.startswith("pid ") else []
    if pid_field and pid_field[0].isdecimal() and process_alive(int(pid_field[0])):
        return record
    return "a holder that recorded no pid (script/gate-capacity.py or an external lock)"


def record_holder(stream, generation: int) -> None:
    # The lock file is opened "a+b": after truncation the append lands at 0.
    try:
        stream.truncate(0)
        stream.write(f"pid {os.getpid()} gate run {generation} in {ROOT}\n".encode())
        stream.flush()
    except OSError:
        pass


def clear_holder(stream) -> None:
    try:
        stream.truncate(0)
    except (OSError, ValueError):
        pass


def acquire_if_latest(
    stream,
    generation: int,
    generation_path: Path,
    stopped,
    label: str = "",
) -> bool:
    started = time.monotonic()
    next_notice = 0.0
    while not stopped():
        if read_generation(generation_path) != generation:
            return False
        try:
            fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            waited = time.monotonic() - started
            if label and waited >= next_notice:
                print(
                    f"gate: run {generation} waiting for {label} held by "
                    f"{lock_holder(stream)} ({waited:.0f}s so far)",
                    file=sys.stderr,
                    flush=True,
                )
                next_notice = waited + WAIT_NOTICE_SECONDS
            time.sleep(POLL_SECONDS)
            continue
        if read_generation(generation_path) != generation:
            return False
        if label:
            record_holder(stream, generation)
            if next_notice:
                print(
                    f"gate: run {generation} acquired {label} after "
                    f"{time.monotonic() - started:.0f}s",
                    file=sys.stderr,
                    flush=True,
                )
        return True
    return False


def machine_slot_module():
    """script/gate-slot.py as a module, when this checkout carries it."""
    if not MACHINE_SLOT.is_file():
        return None
    spec = importlib.util.spec_from_file_location("gate_slot", MACHINE_SLOT)
    if spec is None or spec.loader is None:
        return None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def take_machine_slot(
    locks: ExitStack,
    preflight: bool,
    capacity_inherited: bool,
    command: list[str],
    generation: int,
    generation_path: Path,
    stopped,
):
    """Queue for the machine-wide gate slot ahead of repository capacity.

    One full gate runs on a machine at a time across every repository
    (CORE-1470). A preflight never queues; it only says who holds the slot.
    Neither does a gate whose ancestor already took repository capacity
    without the slot: waiting for the slot while holding capacity would
    deadlock with a gate that holds the slot and waits for that capacity.
    Returns (False, None) when the run was cancelled or superseded while
    queued, otherwise (True, slot), where slot is None unless this run now
    holds the slot itself. The slot is advisory plumbing around the gate, not
    part of its verdict: a store that cannot be used is reported and the gate
    runs without it.
    """
    try:
        gate_slot = machine_slot_module()
        if gate_slot is None or gate_slot.reentrant() is not None:
            return True, None
        if preflight:
            entries, _notes = gate_slot.snapshot()
            if entries:
                print(
                    f"gate: the machine gate slot is held by {gate_slot.describe(entries[0])}; "
                    "this preflight does not wait for it",
                    file=sys.stderr,
                    flush=True,
                )
            return True, None
        if capacity_inherited:
            print(
                "gate: repository capacity was taken before the machine gate slot; "
                "running without waiting for the slot",
                file=sys.stderr,
                flush=True,
            )
            return True, None
        slot = gate_slot.Slot(command, cwd=ROOT)
        slot.join()
        locks.callback(slot.leave)
        started = time.monotonic()
        last_notice, noticed_at = None, 0.0
        while not stopped():
            if read_generation(generation_path) != generation:
                return False, None
            state = slot.poll()
            for note in state.notes:
                print(f"gate: {note}", file=sys.stderr, flush=True)
            if state.held:
                if last_notice is not None:
                    print(
                        f"gate: run {generation} took the machine gate slot after "
                        f"{time.monotonic() - started:.0f}s",
                        file=sys.stderr,
                        flush=True,
                    )
                return True, slot
            notice = (state.holder["path"], state.position, state.waiting)
            now = time.monotonic()
            if notice != last_notice or now - noticed_at >= WAIT_NOTICE_SECONDS:
                print(
                    f"gate: run {generation} {gate_slot.waiting_line(state)}",
                    file=sys.stderr,
                    flush=True,
                )
                last_notice, noticed_at = notice, now
            time.sleep(gate_slot.POLL_SECONDS)
        return False, None
    except (ImportError, OSError, SyntaxError) as error:
        print(
            f"gate: machine gate slot unusable ({error}); running without the machine slot",
            file=sys.stderr,
            flush=True,
        )
        return True, None


def inherited_capacity_lock(lock_path: Path) -> int | None:
    """Return a private duplicate of an ancestor-owned capacity lock, if any.

    `script/gate-capacity.py` is the one capacity authority: whichever process
    first acquires the common-dir lock names its FD in CAPACITY_LOCK_FD, and
    every nested gate layer adopts that ownership instead of opening a new
    description, which would wait forever on its own ancestor (CORE-0392).
    An FD naming another repository's lock (a nested gate for a different
    repository) or a closed FD is not ours; an FD naming this lock without
    owning it is refused rather than trusted.
    """
    raw_fd = os.environ.get(CAPACITY_LOCK_FD)
    if raw_fd is None:
        return None
    if not raw_fd.isdecimal():
        raise OSError(errno.EBADF, f"invalid {CAPACITY_LOCK_FD}: {raw_fd!r}")
    lock = int(raw_fd)
    try:
        inherited = os.fstat(lock)
    except OSError as error:
        if error.errno != errno.EBADF:
            raise
        return None
    try:
        canonical = lock_path.stat()
    except FileNotFoundError:
        return None
    if (inherited.st_dev, inherited.st_ino) != (canonical.st_dev, canonical.st_ino):
        return None
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError as error:
        raise OSError(
            errno.EBUSY, f"{CAPACITY_LOCK_FD} does not own {lock_path}"
        ) from error
    # The duplicate shares the ancestor's open file description, so the lock
    # it already holds is ours too, and closing the duplicate never releases it.
    return os.dup(lock)


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
    preflight = os.environ.get("GATE_PREFLIGHT") == "1"
    state_dir = Path(
        os.environ.get("GATE_LATEST_STATE_DIR", ROOT / ".agent-evidence" / "gate-latest")
    )
    if preflight:
        state_dir = state_dir / "preflight"
    state_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    generation_path = state_dir / "generation"
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

    # Handlers go in before the generation is published: once a run is
    # visible as queued, a cancellation must be remembered, not lost to a
    # default handler.
    handled = (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)
    previous_handlers = {signum: signal.signal(signum, remember_signal) for signum in handled}
    try:
        generation = allocate_generation(state_dir / "control.lock", generation_path)
        try:
            common_dir = git_common_dir()
        except subprocess.CalledProcessError:
            # A group-delivered cancellation also kills the git child; report
            # the cancellation, not a coordinator failure.
            if received_signal:
                return 128 + received_signal
            raise
        if preflight:
            capacity_path = common_dir / "beamfall-gate.preflight.lock"
            capacity_label = f"the gate-preflight lock ({capacity_path})"
            capacity_fd = None
        else:
            capacity_path = common_dir / "beamfall-gate.capacity.lock"
            capacity_label = f"gate capacity ({capacity_path})"
            capacity_fd = inherited_capacity_lock(capacity_path)
        capacity_inherited = capacity_fd is not None
        if capacity_fd is None:
            capacity_fd = os.open(
                capacity_path,
                os.O_CLOEXEC | os.O_CREAT | os.O_RDWR,
                0o600,
            )
        with ExitStack() as locks:
            capacity_lock = locks.enter_context(os.fdopen(capacity_fd, "a+b"))
            proceed, machine_slot = take_machine_slot(
                locks,
                preflight,
                capacity_inherited,
                command,
                generation,
                generation_path,
                lambda: bool(received_signal),
            )
            if not proceed:
                if received_signal:
                    return 128 + received_signal
                print(
                    f"gate: run {generation} superseded before start by run "
                    f"{read_generation(generation_path)}",
                    file=sys.stderr,
                )
                return SUPERSEDED_EXIT
            if not acquire_if_latest(
                capacity_lock,
                generation,
                generation_path,
                lambda: bool(received_signal),
                capacity_label,
            ):
                if received_signal:
                    return 128 + received_signal
                print(
                    f"gate: run {generation} superseded before start by run "
                    f"{read_generation(generation_path)}",
                    file=sys.stderr,
                )
                return SUPERSEDED_EXIT
            locks.callback(clear_holder, capacity_lock)

            run_lock = locks.enter_context((state_dir / "run.lock").open("a+b"))
            if not acquire_if_latest(
                run_lock,
                generation,
                generation_path,
                lambda: bool(received_signal),
                f"this worktree's run lock ({state_dir / 'run.lock'})",
            ):
                if received_signal:
                    return 128 + received_signal
                print(
                    f"gate: run {generation} superseded before start by run "
                    f"{read_generation(generation_path)}",
                    file=sys.stderr,
                )
                return SUPERSEDED_EXIT
            locks.callback(clear_holder, run_lock)

            environment = os.environ.copy()
            environment.pop("GATE_WORKER_REAPS_DESCENDANTS", None)
            environment.pop(COORDINATOR_SUPERVISED_FLAG, None)
            environment.pop(PROCESS_SUPERVISOR_SIGNAL_GRACE, None)
            environment["GATE_RUN_ID"] = str(generation)
            if preflight:
                # The preflight lock is not gate capacity: never name it as
                # such, and tell gate.sh not to re-acquire capacity itself.
                environment.pop(CAPACITY_LOCK_FD, None)
                environment["BEAMFALL_GATE_CAPACITY_HELD"] = "1"
            else:
                environment[CAPACITY_LOCK_FD] = str(capacity_lock.fileno())
            if machine_slot is not None:
                environment["BEAMFALL_GATE_SLOT_HELD"] = str(machine_slot.path)
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
