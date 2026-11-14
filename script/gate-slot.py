#!/usr/bin/env python3
"""Hold the one machine-wide gate slot, first come first served (CORE-1470).

    gate-slot.py [--no-wait] -- COMMAND [ARG ...]
    gate-slot.py --status

Every repository's full gate takes the same per-user queue, so only one runs on
the machine at a time and the rest start in the order they arrived. A caller
that has to wait prints who holds the slot and its own place in the queue.

The queue is a directory of small files, one per caller, named by a sequence
number. The lowest-numbered entry whose process is alive holds the slot. An
entry counts only while its process is alive (pid plus recorded start time), so
a holder that exits, crashes or is killed frees the slot with no timeout and no
cleanup step. That is also why the command replaces this process instead of
running beneath it: the entry names COMMAND's own pid, and the slot is held for
exactly as long as COMMAND lives.

A caller that already runs under the holder (a nested gate) does not queue.

Exit status: COMMAND's own; 69 with GATE_SLOT_BUSY when --no-wait (or
BEAMFALL_GATE_SLOT_NOWAIT=1) finds the slot held; 128+signal when interrupted
while waiting; 64 for usage errors; 127 when COMMAND cannot be started; 70 when
--status cannot read the queue.
"""

from __future__ import annotations

import calendar
from contextlib import contextmanager
import fcntl
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time


SLOT_DIR = "BEAMFALL_GATE_SLOT_DIR"
SLOT_NOWAIT = "BEAMFALL_GATE_SLOT_NOWAIT"
SLOT_HELD = "BEAMFALL_GATE_SLOT_HELD"
BUSY_EXIT = 69
POLL_SECONDS = 0.5
NOTICE_SECONDS = 30.0
# procps derives a start time from the boot time and can move it by a second
# between calls; a reused pid is never this close to the original's start.
START_TOLERANCE_SECONDS = 2
COMMAND_WIDTH = 120


def slot_directory() -> Path:
    configured = os.environ.get(SLOT_DIR)
    if configured:
        return Path(configured)
    return Path.home() / ".beamfall" / "gate-slot"


def _ps(*arguments: str) -> str | None:
    """ps output, or None when ps itself cannot be run."""
    try:
        result = subprocess.run(
            ["ps", *arguments],
            capture_output=True,
            # Start times are compared between callers: one locale and one zone.
            env={**os.environ, "LC_ALL": "C", "TZ": "UTC"},
            text=True,
            timeout=30,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    # ps exits non-zero when a listed pid is gone; what it printed still holds.
    return result.stdout


def _start_epoch(text: str) -> int | None:
    try:
        return calendar.timegm(time.strptime(" ".join(text.split()), "%a %b %d %H:%M:%S %Y"))
    except (ValueError, OverflowError):
        return None


def process_table(pids: list[int]) -> dict[int, tuple[str, str]] | None:
    """pid -> (state, start time) for the listed pids that exist."""
    if not pids:
        return {}
    output = _ps("-o", "pid=,stat=,lstart=", "-p", ",".join(str(pid) for pid in pids))
    if output is None:
        return None
    table = {}
    for line in output.splitlines():
        fields = line.split(None, 2)
        if len(fields) == 3 and fields[0].isdecimal():
            table[int(fields[0])] = (fields[1], fields[2].strip())
    return table


def own_start() -> str:
    table = process_table([os.getpid()])
    return table.get(os.getpid(), ("", ""))[1] if table else ""


def pid_exists(pid: int) -> bool:
    if pid <= 0:
        return False
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError:
        return False
    return True


def entry_alive(entry: dict, table: dict[int, tuple[str, str]] | None) -> bool:
    pid = entry["pid"]
    if not pid_exists(pid):
        return False
    if table is None or pid not in table:
        # ps could not be asked, or lost a race with the process exiting; the
        # next look settles it.
        return True
    state, started = table[pid]
    if state.startswith("Z"):
        return False
    recorded = _start_epoch(entry.get("start", ""))
    current = _start_epoch(started)
    if recorded is None or current is None:
        return True
    return abs(recorded - current) <= START_TOLERANCE_SECONDS


def ancestors() -> set[int]:
    """This process and every process above it."""
    found = {os.getpid()}
    output = _ps("-axo", "pid=,ppid=")
    if output is None:
        return found
    parents = {}
    for line in output.splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[0].isdecimal() and fields[1].isdecimal():
            parents[int(fields[0])] = int(fields[1])
    pid = os.getpid()
    while pid in parents and parents[pid] not in found and parents[pid] > 0:
        pid = parents[pid]
        found.add(pid)
    return found


def _git(cwd: Path, *arguments: str) -> str:
    try:
        result = subprocess.run(
            ["git", "-C", str(cwd), *arguments],
            capture_output=True,
            text=True,
            timeout=30,
        )
    except (OSError, subprocess.SubprocessError):
        return ""
    return result.stdout.strip() if result.returncode == 0 else ""


def describe_caller(cwd: Path, command: list[str]) -> dict:
    toplevel = _git(cwd, "rev-parse", "--show-toplevel")
    text = " ".join(command)
    if len(text) > COMMAND_WIDTH:
        text = text[: COMMAND_WIDTH - 3] + "..."
    return {
        "repo": toplevel or str(cwd),
        "head": _git(cwd, "rev-parse", "--short", "HEAD"),
        "branch": _git(cwd, "rev-parse", "--abbrev-ref", "HEAD"),
        "cwd": str(cwd),
        "command": text,
    }


def elapsed(seconds: float) -> str:
    seconds = max(0, int(seconds))
    if seconds < 60:
        return f"{seconds}s"
    if seconds < 3600:
        return f"{seconds // 60}m{seconds % 60:02d}s"
    return f"{seconds // 3600}h{seconds % 3600 // 60:02d}m"


def describe(entry: dict, now: float | None = None) -> str:
    """One line naming an entry: repository, head, pid, elapsed time, command."""
    now = time.time() if now is None else now
    since = entry.get("acquired_at") or entry.get("enqueued_at") or now
    where = entry.get("head") or "no head"
    if entry.get("branch") and entry["branch"] != "HEAD":
        where = f"{entry['branch']} @ {where}"
    text = f"{entry.get('repo') or '?'} ({where}) pid {entry['pid']}, {elapsed(now - since)}"
    return f"{text}: {entry['command']}" if entry.get("command") else text


class Store:
    """The queue directory. Every read-modify-write happens under its mutex."""

    def __init__(self, directory: Path | None = None) -> None:
        self.directory = Path(directory) if directory is not None else slot_directory()
        self.queue = self.directory / "queue"

    def prepare(self) -> None:
        self.queue.mkdir(mode=0o700, parents=True, exist_ok=True)

    @contextmanager
    def locked(self):
        descriptor = os.open(self.directory / "mutex", os.O_CLOEXEC | os.O_CREAT | os.O_RDWR, 0o600)
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX)
            yield
        finally:
            os.close(descriptor)

    def write(self, path: Path, entry: dict) -> None:
        temporary = self.directory / f"entry.{os.getpid()}.tmp"
        temporary.write_text(json.dumps(entry, sort_keys=True) + "\n", encoding="utf-8")
        os.replace(temporary, path)

    def next_sequence(self, entries: list[dict]) -> int:
        counter = self.directory / "sequence"
        try:
            last = int(counter.read_text(encoding="utf-8").strip() or "0")
        except (OSError, ValueError):
            last = 0
        sequence = max([last, *(entry["seq"] for entry in entries)]) + 1
        counter.write_text(f"{sequence}\n", encoding="utf-8")
        return sequence

    def live(self) -> tuple[list[dict], list[str]]:
        """Live entries in queue order, after removing the dead ones.

        Call with the mutex held. Returns the entries and one note per entry
        removed.
        """
        entries, notes = [], []
        for path in sorted(self.queue.glob("*.json")):
            try:
                entry = json.loads(path.read_text(encoding="utf-8"))
                entry["seq"] = int(entry["seq"])
                entry["pid"] = int(entry["pid"])
            except (OSError, ValueError, KeyError, TypeError):
                path.unlink(missing_ok=True)
                notes.append(f"removed unreadable queue entry {path.name}")
                continue
            entry["path"] = str(path)
            entries.append(entry)
        entries.sort(key=lambda entry: entry["seq"])
        table = process_table([entry["pid"] for entry in entries])
        alive = []
        for entry in entries:
            if entry_alive(entry, table):
                alive.append(entry)
                continue
            Path(entry["path"]).unlink(missing_ok=True)
            role = "holder" if entry.get("acquired_at") else "waiter"
            notes.append(f"{role} {describe(entry)} has ended; removed its queue entry")
        return alive, notes


def snapshot(directory: Path | None = None) -> tuple[list[dict], list[str]]:
    """Live entries in queue order (the first holds the slot), and removal notes."""
    store = Store(directory)
    if not store.queue.is_dir():
        return [], []
    with store.locked():
        return store.live()


def reentrant(directory: Path | None = None) -> dict | None:
    """The holder's entry when this process already runs under the slot."""
    entries, _notes = snapshot(directory)
    if not entries:
        return None
    marker = os.environ.get(SLOT_HELD)
    above = ancestors()
    for entry in entries:
        if entry["pid"] in above or (marker and entry["path"] == marker):
            return entry
    return None


class State:
    def __init__(self, held: bool, position: int, waiting: int, holder: dict | None, notes: list[str]) -> None:
        self.held = held
        self.position = position
        self.waiting = waiting
        self.holder = holder
        self.notes = notes


class Slot:
    """One caller's place in the queue: join(), poll() until held, leave()."""

    def __init__(
        self,
        command: list[str],
        cwd: Path | None = None,
        directory: Path | None = None,
    ) -> None:
        self.store = Store(directory)
        self.caller = describe_caller(Path(cwd) if cwd is not None else Path.cwd(), command)
        self.path: Path | None = None
        self.entry: dict | None = None

    def join(self, only_if_free: bool = False) -> dict | None:
        """Take a place at the back of the queue.

        With only_if_free, a held slot is left alone and its holder returned.
        """
        self.store.prepare()
        start = own_start()
        with self.store.locked():
            entries, _notes = self.store.live()
            if only_if_free and entries:
                return entries[0]
            sequence = self.store.next_sequence(entries)
            self.entry = {
                **self.caller,
                "seq": sequence,
                "pid": os.getpid(),
                "start": start,
                "enqueued_at": time.time(),
                "acquired_at": None,
            }
            self.path = self.store.queue / f"{sequence:012d}.{os.getpid()}.json"
            self.store.write(self.path, self.entry)
        return None

    def poll(self) -> State:
        assert self.path is not None and self.entry is not None
        with self.store.locked():
            entries, notes = self.store.live()
            if not any(entry["path"] == str(self.path) for entry in entries):
                # Our entry was removed from under us; rejoin at the back
                # rather than run without a place in the queue.
                notes.append("this caller's queue entry was removed; rejoining the queue")
                sequence = self.store.next_sequence(entries)
                self.entry["seq"] = sequence
                self.path = self.store.queue / f"{sequence:012d}.{os.getpid()}.json"
                self.store.write(self.path, self.entry)
                entries.append({**self.entry, "path": str(self.path)})
            ahead = [entry for entry in entries if entry["seq"] < self.entry["seq"]]
            if not ahead:
                if not self.entry["acquired_at"]:
                    self.entry["acquired_at"] = time.time()
                    self.store.write(self.path, self.entry)
                return State(True, 0, len(entries) - 1, None, notes)
            return State(False, len(ahead), len(entries) - 1, ahead[0], notes)

    def leave(self) -> None:
        if self.path is not None:
            try:
                self.path.unlink()
            except OSError:
                # An entry left behind names a process that is gone and is
                # removed by the next caller.
                pass
            self.path = None


def waiting_line(state: State) -> str:
    return (
        f"waiting for the machine gate slot, queue position {state.position} of {state.waiting}; "
        f"held by {describe(state.holder)}"
    )


def say(message: str) -> None:
    print(f"gate-slot: {message}", file=sys.stderr, flush=True)


def status() -> int:
    try:
        entries, notes = snapshot()
    except OSError as error:
        say(f"slot store unusable ({error})")
        return 70
    for note in notes:
        say(note)
    if not entries:
        print("gate-slot: free")
        return 0
    print(f"gate-slot: held by {describe(entries[0])}")
    for position, entry in enumerate(entries[1:], start=1):
        print(f"gate-slot: waiting {position}: {describe(entry)}")
    return 0


def run(command: list[str], no_wait: bool) -> int:
    received_signal = 0

    def remember_signal(signum: int, _frame: object) -> None:
        nonlocal received_signal
        if not received_signal:
            received_signal = signum

    handled = (signal.SIGHUP, signal.SIGINT, signal.SIGTERM)
    inherited = {signum: signal.signal(signum, remember_signal) for signum in handled}
    inherited_mask = signal.pthread_sigmask(signal.SIG_UNBLOCK, handled)

    slot: Slot | None = None
    try:
        holder = reentrant()
        if holder is not None:
            say(f"already under the slot held by {describe(holder)}; running")
        else:
            slot = Slot(command)
            holder = slot.join(only_if_free=no_wait)
            if holder is not None:
                say(f"GATE_SLOT_BUSY held by {describe(holder)}")
                return BUSY_EXIT
    except OSError as error:
        say(f"slot store unusable ({error}); running without the machine slot")
        slot = None

    try:
        if slot is not None:
            last_notice, noticed_at = None, 0.0
            while True:
                if received_signal:
                    return 128 + received_signal
                try:
                    state = slot.poll()
                except OSError as error:
                    say(f"slot store unusable ({error}); running without the machine slot")
                    break
                for note in state.notes:
                    say(note)
                if state.held:
                    os.environ[SLOT_HELD] = str(slot.path)
                    break
                if no_wait:
                    say(f"GATE_SLOT_BUSY held by {describe(state.holder)}")
                    return BUSY_EXIT
                notice = (state.holder["path"], state.position, state.waiting)
                now = time.monotonic()
                if notice != last_notice or now - noticed_at >= NOTICE_SECONDS:
                    say(waiting_line(state))
                    last_notice, noticed_at = notice, now
                time.sleep(POLL_SECONDS)

        # The command gets the signal dispositions and mask this process was
        # started with. They are put back with the signals blocked, so one that
        # arrives meanwhile is seen here and not lost across exec.
        signal.pthread_sigmask(signal.SIG_BLOCK, handled)
        for signum in handled:
            signal.signal(signum, inherited[signum] or signal.SIG_DFL)
        if not received_signal:
            pending = signal.sigpending()
            received_signal = next((signum for signum in handled if signum in pending), 0)
        if received_signal:
            return 128 + received_signal
        signal.pthread_sigmask(signal.SIG_SETMASK, inherited_mask)
        try:
            os.execvpe(command[0], command, os.environ)
        except OSError as error:
            say(f"cannot run {command[0]}: {error}")
            return 127
    finally:
        # Reached only when the command did not replace this process.
        if slot is not None:
            slot.leave()
    return 70


def main() -> int:
    arguments = sys.argv[1:]
    usage = f"usage: {sys.argv[0]} [--no-wait] -- COMMAND [ARG ...] | --status"
    no_wait = os.environ.get(SLOT_NOWAIT) == "1"
    while arguments and arguments[0] != "--":
        option = arguments.pop(0)
        if option == "--status" and not arguments:
            return status()
        if option == "--no-wait":
            no_wait = True
        else:
            print(usage, file=sys.stderr)
            return 64
    command = arguments[1:]
    if not command:
        print(usage, file=sys.stderr)
        return 64
    return run(command, no_wait)


if __name__ == "__main__":
    raise SystemExit(main())
