#!/usr/bin/env python3
"""Run a test in an isolated session and reap every process it creates."""

from __future__ import annotations

import os
from pathlib import Path
import hashlib
import re
import secrets
import signal
import struct
import subprocess
import sys
import time
import ctypes
import ctypes.util
import errno
from functools import lru_cache
from typing import Optional


TOKEN_ENV_PREFIX = "BEAMFALL_PROCESS_SUPERVISOR_TOKEN_"
SIGNAL_GRACE_ENV = "BEAMFALL_PROCESS_SUPERVISOR_SIGNAL_GRACE_SECONDS"
CAPACITY_LOCK_FD = "BEAMFALL_GATE_CAPACITY_FD"
ORPHAN_INDETERMINATE_GRACE_SECONDS = 1.0
SHARD_QUIESCENCE_GRACE_SECONDS = 1.0
ProcessIdentity = tuple[int, int]


class InspectionIndeterminate(RuntimeError):
    """The process inventory could not prove absence."""


def token_env_name(token: str) -> str:
    # One key per nested supervisor keeps every ancestor's ownership marker in
    # the inherited environment. Reusing one key would make the inner boundary
    # invisible to an outer cancellation sweep.
    return f"{TOKEN_ENV_PREFIX}{token}"


def inherited_capacity_fd() -> Optional[int]:
    raw_fd = os.environ.get(CAPACITY_LOCK_FD)
    if raw_fd is None:
        return None
    if not raw_fd.isdecimal():
        raise OSError(errno.EBADF, f"invalid {CAPACITY_LOCK_FD}: {raw_fd!r}")
    capacity_fd = int(raw_fd)
    try:
        os.fstat(capacity_fd)
    except OSError as error:
        if error.errno != errno.EBADF:
            raise
        os.environ.pop(CAPACITY_LOCK_FD, None)
        print(
            f"test process supervisor discarded stale {CAPACITY_LOCK_FD}={raw_fd}",
            file=sys.stderr,
        )
        return None
    return capacity_fd


@lru_cache(maxsize=1)
def darwin_libc() -> ctypes.CDLL:
    library = ctypes.CDLL(ctypes.util.find_library("c"), use_errno=True)
    library.sysctl.argtypes = [
        ctypes.POINTER(ctypes.c_int),
        ctypes.c_uint,
        ctypes.c_void_p,
        ctypes.POINTER(ctypes.c_size_t),
        ctypes.c_void_p,
        ctypes.c_size_t,
    ]
    library.sysctl.restype = ctypes.c_int
    return library


@lru_cache(maxsize=1)
def darwin_libproc() -> ctypes.CDLL:
    library = ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True)
    library.proc_listallpids.argtypes = [ctypes.c_void_p, ctypes.c_int]
    library.proc_listallpids.restype = ctypes.c_int
    library.proc_pidinfo.argtypes = [
        ctypes.c_int,
        ctypes.c_int,
        ctypes.c_uint64,
        ctypes.c_void_p,
        ctypes.c_int,
    ]
    library.proc_pidinfo.restype = ctypes.c_int
    library.proc_pidpath.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_uint32]
    library.proc_pidpath.restype = ctypes.c_int
    return library


@lru_cache(maxsize=1)
def darwin_procargs_capacity() -> int:
    # KERN_PROCARGS2 may require the host's full ARG_MAX. A fixed smaller
    # buffer makes an unusually large argv/environment invisible to cleanup.
    try:
        return max(256 * 1024, int(os.sysconf("SC_ARG_MAX")))
    except (OSError, ValueError):
        return 1024 * 1024


def all_process_ids() -> list[int]:
    if sys.platform == "darwin":
        library = darwin_libproc()
        count = max(1024, library.proc_listallpids(None, 0) + 256)
        buffer = (ctypes.c_int * count)()
        found = library.proc_listallpids(buffer, ctypes.sizeof(buffer))
        if found < 0:
            raise InspectionIndeterminate("cannot enumerate Darwin processes")
        return [pid for pid in buffer[:found] if pid > 0]
    proc = Path("/proc")
    if proc.is_dir():
        try:
            return [int(entry.name) for entry in proc.iterdir() if entry.name.isdigit()]
        except OSError as error:
            raise InspectionIndeterminate("cannot enumerate /proc") from error
    raise InspectionIndeterminate("no process inventory is available")


def process_has_token(
    pid: int, token_entry: bytes, darwin_buffer: Optional[ctypes.Array] = None
) -> Optional[bool]:
    if sys.platform == "darwin":
        mib = (ctypes.c_int * 3)(1, 49, pid)  # CTL_KERN, KERN_PROCARGS2, pid
        # One buffer belongs to one enumeration sweep. Signal handlers can
        # start a nested sweep, so a process-global mutable buffer is unsafe.
        buffer = (
            darwin_buffer
            if darwin_buffer is not None
            else ctypes.create_string_buffer(darwin_procargs_capacity())
        )
        size = ctypes.c_size_t(len(buffer))
        ctypes.set_errno(0)
        if darwin_libc().sysctl(mib, 3, buffer, ctypes.byref(size), None, 0) != 0:
            error = ctypes.get_errno()
            if error == errno.ESRCH:
                return False
            if error in (errno.EPERM, errno.EACCES):
                return None
            # KERN_PROCARGS2 reports EINVAL for zombies/kernel processes whose
            # argument/environment image no longer exists; they cannot carry an
            # inherited supervisor token. Other live inspection errors remain
            # indeterminate.
            return False if error == errno.EINVAL else None
        return token_entry + b"\0" in buffer.raw[: size.value]
    try:
        return token_entry + b"\0" in Path(f"/proc/{pid}/environ").read_bytes()
    except (FileNotFoundError, ProcessLookupError):
        return False
    except PermissionError:
        return None
    except OSError as error:
        return False if error.errno in (errno.ENOENT, errno.ESRCH) else None


def process_matches_effective_uid(pid: int) -> Optional[bool]:
    """Return whether a Linux process can inherit this supervisor's token.

    Procfs commonly denies ``environ`` for unrelated users (including PID 1 on
    hosted CI). Such a process cannot be one of our descendants, so its denial
    is not global inventory ambiguity. Same-UID and non-proc platforms remain
    fail-closed when token inspection is unavailable.
    """
    proc = Path("/proc")
    if not proc.is_dir():
        return None
    try:
        return (proc / str(pid)).stat().st_uid == os.geteuid()
    except (FileNotFoundError, ProcessLookupError):
        return False
    except OSError as error:
        return False if error.errno in (errno.ENOENT, errno.ESRCH) else None


def parse_linux_process_identity(pid: int, stat_text: str) -> Optional[ProcessIdentity]:
    """Return (pid, starttime) from proc stat, whose comm field may contain ') '."""
    delimiter = stat_text.rfind(") ")
    if delimiter < 0:
        return None
    fields = stat_text[delimiter + 2 :].split()
    # fields[0] is proc field 3 (state), so field 22 (starttime) is index 19.
    if len(fields) <= 19:
        return None
    try:
        return pid, int(fields[19])
    except ValueError:
        return None


def linux_process_identity(pid: int) -> Optional[ProcessIdentity]:
    proc = Path("/proc")
    if not proc.is_dir():
        return None
    try:
        stat_text = (proc / str(pid) / "stat").read_text(encoding="utf-8")
    except (FileNotFoundError, ProcessLookupError):
        return None
    except OSError:
        return None
    return parse_linux_process_identity(pid, stat_text)


def process_identity(pid: int) -> Optional[ProcessIdentity]:
    """Return a PID-reuse-safe identity for a live process."""
    identity = linux_process_identity(pid)
    if identity is not None or Path("/proc").is_dir():
        return identity
    if sys.platform == "darwin":
        info = darwin_process_info(pid)
        if info is None:
            return None
        _, _, started_seconds, started_microseconds = info
        return pid, started_seconds * 1_000_000 + started_microseconds
    return None


def preexisting_indeterminate_identities(token: str) -> Optional[frozenset[ProcessIdentity]]:
    """Snapshot protected identities before the supervised child can exist."""
    token_entry = f"{token_env_name(token)}={token}".encode()
    identities: set[ProcessIdentity] = set()
    try:
        process_ids = all_process_ids()
    except (InspectionIndeterminate, OSError):
        return None
    for pid in process_ids:
        if process_matches_effective_uid(pid) is False:
            continue
        try:
            os.getsid(pid)
        except (FileNotFoundError, ProcessLookupError):
            continue
        except PermissionError:
            identity = process_identity(pid)
            if identity is None:
                return None
            identities.add(identity)
            continue
        token_match = process_has_token(pid, token_entry)
        if token_match is None:
            identity = process_identity(pid)
            if identity is None:
                return None
            identities.add(identity)
        else:
            identity = process_identity(pid)
            if identity is not None:
                identities.add(identity)
    return frozenset(identities)


def descends_from_protected_process(
    pid: int,
    session_id: int,
    protected: frozenset[ProcessIdentity],
) -> Optional[bool]:
    """Classify an unreadable process without mistaking concurrent peers for ours.

    A candidate whose ancestry reaches the supervised session remains
    indeterminate and therefore fail-closed. A candidate whose ancestry first
    reaches a process that existed before our child is a concurrent peer and
    cannot have inherited this invocation's new token. Reparented candidates
    remain indeterminate because their lineage can no longer prove either case.
    """
    current = pid
    seen: set[int] = set()
    while current > 1 and current not in seen:
        seen.add(current)
        parent = process_parent_id(current)
        if parent is None or parent <= 1:
            return None
        if parent == session_id:
            return False
        identity = process_identity(parent)
        if identity is None:
            return None
        if identity in protected:
            return True
        current = parent
    return None


def owned_process_ids(
    session_id: Optional[int],
    token: str,
    preexisting_indeterminate: frozenset[ProcessIdentity] = frozenset(),
) -> tuple[list[int], bool]:
    token_entry = f"{token_env_name(token)}={token}".encode()
    darwin_buffer = (
        ctypes.create_string_buffer(darwin_procargs_capacity())
        if sys.platform == "darwin"
        else None
    )
    found: list[int] = []
    indeterminate = False
    try:
        process_ids = all_process_ids()
    except (InspectionIndeterminate, OSError):
        return found, True
    for pid in process_ids:
        if session_id is not None:
            try:
                same_session = os.getsid(pid) == session_id
            except ProcessLookupError:
                continue
            except PermissionError:
                if process_matches_effective_uid(pid) is not False:
                    indeterminate = True
                continue
            if same_session:
                if pid not in found:
                    found.append(pid)
                continue
        if process_matches_effective_uid(pid) is False:
            continue
        token_match = process_has_token(pid, token_entry, darwin_buffer)
        if token_match is True:
            if pid not in found:
                found.append(pid)
        elif token_match is None:
            identity = process_identity(pid)
            if identity is not None and identity in preexisting_indeterminate:
                continue
            if session_id is not None and descends_from_protected_process(
                pid, session_id, preexisting_indeterminate
            ) is True:
                continue
            indeterminate = True
    return found, indeterminate


def group_alive(group_id: int) -> bool:
    try:
        os.killpg(group_id, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        # EPERM proves the group exists but is not signalable by this caller.
        # Only ESRCH is evidence that cleanup has completed.
        return True
    return True


def signal_group(group_id: int, sig: signal.Signals) -> None:
    try:
        os.killpg(group_id, sig)
    except ProcessLookupError:
        pass
    except PermissionError:
        # Preserve the unknown/live result for the subsequent group_alive probe.
        pass


def signal_owned(
    session_id: Optional[int],
    token: str,
    sig: signal.Signals,
    preexisting_indeterminate: frozenset[ProcessIdentity] = frozenset(),
) -> None:
    pids, _ = owned_process_ids(session_id, token, preexisting_indeterminate)
    if not pids:
        if session_id is not None:
            signal_group(session_id, sig)
        return
    for pid in pids:
        try:
            os.kill(pid, sig)
        except (ProcessLookupError, PermissionError):
            pass


def owned_alive(
    session_id: Optional[int],
    token: str,
    preexisting_indeterminate: frozenset[ProcessIdentity] = frozenset(),
) -> bool:
    pids, indeterminate = owned_process_ids(
        session_id, token, preexisting_indeterminate
    )
    group_exists = session_id is not None and group_alive(session_id)
    return bool(pids) or indeterminate or group_exists


def reap_owned(
    session_id: Optional[int],
    token: str,
    preexisting_indeterminate: frozenset[ProcessIdentity] = frozenset(),
    grace_seconds: float = 1.0,
) -> bool:
    if not owned_alive(session_id, token, preexisting_indeterminate):
        return False
    signal_owned(session_id, token, signal.SIGTERM, preexisting_indeterminate)
    signal_owned(session_id, token, signal.SIGCONT, preexisting_indeterminate)
    deadline = time.monotonic() + grace_seconds
    while time.monotonic() < deadline:
        if not owned_alive(session_id, token, preexisting_indeterminate):
            return True
        signal_owned(session_id, token, signal.SIGTERM, preexisting_indeterminate)
        signal_owned(session_id, token, signal.SIGCONT, preexisting_indeterminate)
        time.sleep(0.05)
    signal_owned(session_id, token, signal.SIGKILL, preexisting_indeterminate)
    deadline = time.monotonic() + grace_seconds
    while time.monotonic() < deadline and owned_alive(
        session_id, token, preexisting_indeterminate
    ):
        signal_owned(session_id, token, signal.SIGKILL, preexisting_indeterminate)
        time.sleep(0.05)
    return True


def wait_owned_quiescent(
    session_id: int,
    token: str,
    preexisting_indeterminate: frozenset[ProcessIdentity] = frozenset(),
    grace_seconds: float = 0.15,
) -> bool:
    """Require sustained absence so a delayed descendant cannot race disarm."""
    if grace_seconds <= 0:
        return not owned_alive(session_id, token, preexisting_indeterminate)
    deadline = time.monotonic() + grace_seconds * 3
    quiet_since: Optional[float] = None
    while time.monotonic() < deadline:
        if owned_alive(session_id, token, preexisting_indeterminate):
            quiet_since = None
        elif quiet_since is None:
            quiet_since = time.monotonic()
        elif time.monotonic() - quiet_since >= grace_seconds:
            return True
        time.sleep(0.025)
    return False


def quiescence_grace_seconds(env_flag: str) -> float:
    inherited_supervision = any(
        name.startswith(TOKEN_ENV_PREFIX) for name in os.environ
    )
    if env_flag.startswith("ROADMAP_TEST_SHARD_") or inherited_supervision:
        return SHARD_QUIESCENCE_GRACE_SECONDS
    return 0.15


def owned_process_inventory(
    session_id: Optional[int],
    token: str,
    preexisting_indeterminate: frozenset[ProcessIdentity] = frozenset(),
) -> list[str]:
    pids, indeterminate = owned_process_ids(
        session_id, token, preexisting_indeterminate
    )
    rows: list[str] = []
    for pid in sorted(pids):
        rows.append(process_inventory_row(pid))
    if indeterminate:
        rows.append("? ? ? ? <process inventory indeterminate>")
    if not rows and session_id is not None and group_alive(session_id):
        rows.append(f"{session_id} ? {session_id} {session_id} <group inventory unavailable>")
    return rows


def guardian_main(
    control_fd: int,
    token_fd: int,
    preexisting_indeterminate: frozenset[ProcessIdentity],
) -> int:
    try:
        token = os.read(token_fd, 256).decode("ascii")
    finally:
        os.close(token_fd)
    if not re.fullmatch(r"[0-9a-f]{48}", token):
        print("test process supervisor guardian received an invalid ownership token", file=sys.stderr)
        return 70
    run_id = hashlib.sha256(token.encode()).hexdigest()[:16]
    try:
        control = b""
        while True:
            chunk = os.read(control_fd, 64)
            if not chunk:
                break
            control += chunk
    finally:
        os.close(control_fd)
    lines = control.splitlines()
    session_line = lines[0] if lines and lines[0].isdigit() else b""
    session_id = int(session_line) if session_line.isdigit() else None
    disarmed = (
        lines == [b"D"]
        or (
            len(lines) == 2
            and session_id is not None
            and lines[1] == b"D"
        )
    )
    if disarmed and wait_owned_quiescent(
        session_id, token, preexisting_indeterminate
    ):
        return 0
    reported = False
    disarm_inventory = (
        owned_process_inventory(session_id, token, preexisting_indeterminate)
        if disarmed
        else []
    )
    # The first quiescence window can end on a transient process-table result.
    # A second complete empty snapshot proves there is nothing to reap. Concrete
    # or indeterminate inventory still enters the fail-closed rejection path.
    if disarmed and not disarm_inventory:
        return 0
    orphan_indeterminate_since: Optional[float] = None
    while True:
        reap_owned(session_id, token, preexisting_indeterminate, grace_seconds=0.1)
        if not owned_alive(session_id, token, preexisting_indeterminate):
            if disarmed:
                print(
                    "test process supervisor guardian rejected disarm with owned processes:\n"
                    "PID PPID PGID SID COMMAND\n"
                    + "\n".join(disarm_inventory),
                    file=sys.stderr,
                )
                return 1
            return 0
        pids, indeterminate = owned_process_ids(
            session_id, token, preexisting_indeterminate
        )
        group_exists = session_id is not None and group_alive(session_id)
        if disarmed or pids or group_exists or not indeterminate:
            orphan_indeterminate_since = None
        elif orphan_indeterminate_since is None:
            orphan_indeterminate_since = time.monotonic()
        elif (
            time.monotonic() - orphan_indeterminate_since
            >= ORPHAN_INDETERMINATE_GRACE_SECONDS
        ):
            print(
                "test process supervisor orphan guardian could not resolve "
                f"indeterminate inventory; exiting failed (run_id={run_id})",
                file=sys.stderr,
            )
            return 1
        if not reported:
            print(
                "test process supervisor guardian could not prove descendant "
                f"retirement; retrying fail closed (run_id={run_id}):\n"
                "PID PPID PGID SID COMMAND\n"
                + "\n".join(
                    owned_process_inventory(
                        session_id, token, preexisting_indeterminate
                    )
                ),
                file=sys.stderr,
            )
            reported = True
        time.sleep(0.05)


class GuardianProcess:
    """Minimal subprocess-compatible handle for the forked guardian."""

    def __init__(self, pid: int) -> None:
        self.pid = pid
        self.returncode: Optional[int] = None

    def wait(self, timeout: Optional[float] = None) -> int:
        if self.returncode is not None:
            return self.returncode
        deadline = None if timeout is None else time.monotonic() + timeout
        while True:
            try:
                waited_pid, status = os.waitpid(self.pid, os.WNOHANG)
            except ChildProcessError:
                if self.returncode is None:
                    self.returncode = 0
                return self.returncode
            if waited_pid == self.pid:
                self.returncode = os.waitstatus_to_exitcode(status)
                return self.returncode
            if deadline is not None and time.monotonic() >= deadline:
                raise subprocess.TimeoutExpired("guardian", timeout)
            time.sleep(0.01)

    def terminate(self) -> None:
        try:
            os.kill(self.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass

    def kill(self) -> None:
        try:
            os.kill(self.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass


def start_guardian(
    token: str,
    preexisting_indeterminate: frozenset[ProcessIdentity],
    inherited_fds: tuple[int, ...] = (),
) -> tuple[GuardianProcess, int]:
    read_fd, write_fd = os.pipe()
    token_read_fd, token_write_fd = os.pipe()
    exec_read_fd, exec_write_fd = os.pipe()
    protected = ",".join(
        f"{pid}:{started}" for pid, started in preexisting_indeterminate
    )
    guardian_env = os.environ.copy()
    guardian_env.pop(token_env_name(token), None)
    guardian_env.pop(CAPACITY_LOCK_FD, None)
    try:
        guardian_pid = os.fork()
    except BaseException:
        os.close(read_fd)
        os.close(write_fd)
        os.close(token_read_fd)
        os.close(token_write_fd)
        os.close(exec_read_fd)
        os.close(exec_write_fd)
        raise
    if guardian_pid == 0:
        try:
            os.close(write_fd)
            os.close(token_write_fd)
            os.close(exec_read_fd)
            for inherited_fd in inherited_fds:
                os.close(inherited_fd)
            os.set_inheritable(read_fd, True)
            os.set_inheritable(token_read_fd, True)
            os.setsid()
            os.execve(
                sys.executable,
                [
                    sys.executable,
                    str(Path(__file__).resolve()),
                    "--guardian",
                    str(read_fd),
                    str(token_read_fd),
                    protected,
                ],
                guardian_env,
            )
        except BaseException as error:
            try:
                os.write(exec_write_fd, repr(error).encode(errors="replace"))
            except OSError:
                pass
            os._exit(70)
    os.close(read_fd)
    os.close(token_read_fd)
    try:
        os.write(token_write_fd, token.encode("ascii"))
    finally:
        os.close(token_write_fd)
    os.close(exec_write_fd)
    exec_error = os.read(exec_read_fd, 4096)
    os.close(exec_read_fd)
    if exec_error:
        os.close(write_fd)
        os.waitpid(guardian_pid, 0)
        detail = exec_error.decode(errors="replace")
        raise OSError(f"cannot start process guardian: {detail}")
    return GuardianProcess(guardian_pid), write_fd


def stop_guardian(
    guardian: GuardianProcess,
    control_fd: int,
    session_id: Optional[int],
    token: str,
    preexisting_indeterminate: frozenset[ProcessIdentity],
) -> bool:
    disarmed = True
    try:
        os.write(control_fd, b"D\n")
    except OSError:
        disarmed = False
    finally:
        os.close(control_fd)
    try:
        guardian.wait(timeout=1.0)
    except subprocess.TimeoutExpired:
        disarmed = False
        reap_owned(
            session_id,
            token,
            preexisting_indeterminate,
            grace_seconds=0.1,
        )
        if owned_alive(session_id, token, preexisting_indeterminate):
            # The guardian is the last independent containment boundary after
            # this supervisor exits. Never retire it while ownership remains
            # live or indeterminate.
            return False
        guardian.terminate()
        try:
            guardian.wait(timeout=0.1)
        except subprocess.TimeoutExpired:
            guardian.kill()
            guardian.wait()
    return disarmed and guardian.returncode == 0


def supervised_child(control_fd: int) -> int:
    """Arm the guardian before allowing the requested command to exec."""
    command = sys.argv[3:]
    try:
        os.write(control_fd, f"{os.getpid()}\n".encode())
    finally:
        os.close(control_fd)
    os.execvpe(command[0], command, os.environ)
    return 70


def process_inventory_row(pid: int) -> str:
    ppid: int | str = "?"
    pgid: int | str = "?"
    session_id: int | str = "?"
    command = "<command unavailable>"
    if Path("/proc").is_dir():
        try:
            stat_text = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8")
            delimiter = stat_text.rfind(") ")
            fields = stat_text[delimiter + 2 :].split()
            ppid, pgid, session_id = map(int, fields[1:4])
            raw_command = Path(f"/proc/{pid}/cmdline").read_bytes().rstrip(b"\0")
            command = raw_command.replace(b"\0", b" ").decode(errors="replace")
        except (FileNotFoundError, ProcessLookupError, OSError, ValueError):
            pass
    elif sys.platform == "darwin":
        bsd_info = darwin_process_ids(pid)
        if bsd_info is not None:
            ppid, pgid = bsd_info
        path = ctypes.create_string_buffer(4096)
        found = darwin_libproc().proc_pidpath(pid, path, ctypes.sizeof(path))
        if found > 0:
            command = path.value.decode(errors="replace")
    try:
        pgid = os.getpgid(pid)
        session_id = os.getsid(pid)
    except (ProcessLookupError, PermissionError):
        pass
    return f"{pid} {ppid} {pgid} {session_id} {command}"


def darwin_process_info(pid: int) -> Optional[tuple[int, int, int, int]]:
    info = ctypes.create_string_buffer(256)
    found = darwin_libproc().proc_pidinfo(
        pid, 3, 0, info, ctypes.sizeof(info)  # PROC_PIDTBSDINFO
    )
    if found < 136:
        return None
    ppid = struct.unpack_from("=I", info.raw, 16)[0]
    pgid = struct.unpack_from("=I", info.raw, 96)[0]
    started_seconds = struct.unpack_from("=Q", info.raw, 120)[0]
    started_microseconds = struct.unpack_from("=Q", info.raw, 128)[0]
    return ppid, pgid, started_seconds, started_microseconds


def darwin_process_ids(pid: int) -> Optional[tuple[int, int]]:
    info = darwin_process_info(pid)
    return None if info is None else info[:2]


def process_parent_id(pid: int) -> Optional[int]:
    if Path("/proc").is_dir():
        try:
            stat_text = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8")
            delimiter = stat_text.rfind(") ")
            return int(stat_text[delimiter + 2 :].split()[1])
        except (FileNotFoundError, ProcessLookupError, OSError, ValueError, IndexError):
            return None
    if sys.platform == "darwin":
        info = darwin_process_info(pid)
        return None if info is None else info[0]
    return None


def process_group_id(pid: int) -> Optional[int]:
    try:
        return os.getpgid(pid)
    except (ProcessLookupError, PermissionError):
        if sys.platform == "darwin":
            ids = darwin_process_ids(pid)
            return None if ids is None else ids[1]
        return None


def print_group_inventory(group_id: int) -> int:
    try:
        process_ids = all_process_ids()
    except (InspectionIndeterminate, OSError):
        print("PID PPID PGID SID COMMAND\n? ? ? ? <process inventory indeterminate>")
        return 1
    print("PID PPID PGID SID COMMAND")
    found = False
    for pid in process_ids:
        if process_group_id(pid) == group_id:
            print(process_inventory_row(pid))
            found = True
    if not found and group_alive(group_id):
        print(f"? ? {group_id} ? <live group inventory unavailable>")
    return 0


def main() -> int:
    if sys.argv[1:2] == ["--supervised-child"]:
        if len(sys.argv) < 4:
            return 2
        return supervised_child(int(sys.argv[2]))
    if sys.argv[1:2] == ["--guardian"]:
        if len(sys.argv) != 5:
            return 2
        for sig in (signal.SIGHUP, signal.SIGINT, signal.SIGTERM):
            signal.signal(sig, signal.SIG_IGN)
        protected = frozenset(
            (int(pid), int(started))
            for item in sys.argv[4].split(",")
            if item
            for pid, started in [item.split(":", 1)]
        )
        return guardian_main(int(sys.argv[2]), int(sys.argv[3]), protected)
    if sys.argv[1:2] == ["--inventory-group"]:
        if len(sys.argv) != 3:
            print(f"usage: {sys.argv[0]} --inventory-group PGID", file=sys.stderr)
            return 2
        return print_group_inventory(int(sys.argv[2]))
    if len(sys.argv) < 3:
        print(f"usage: {sys.argv[0]} ENV_FLAG COMMAND [ARG ...]", file=sys.stderr)
        return 2
    env_flag, command = sys.argv[1], sys.argv[2:]
    quiescence_grace = quiescence_grace_seconds(env_flag)
    env = os.environ.copy()
    env[env_flag] = "1"
    token = secrets.token_hex(24)
    env[token_env_name(token)] = token
    preexisting_indeterminate = None
    # A just-reaped Darwin process can remain briefly enumerable while its
    # identity/argv image is already unavailable. Retry that indeterminate
    # snapshot; persistent inspection failure still refuses launch.
    for _ in range(20):
        preexisting_indeterminate = preexisting_indeterminate_identities(token)
        if preexisting_indeterminate is not None:
            break
        time.sleep(0.025)
    if preexisting_indeterminate is None:
        print("test process supervisor cannot establish pre-launch process inventory", file=sys.stderr)
        return 1
    try:
        signal_grace = float(os.environ.get(SIGNAL_GRACE_ENV, "1"))
    except ValueError:
        print(f"{SIGNAL_GRACE_ENV} must be numeric", file=sys.stderr)
        return 2
    if signal_grace < 0:
        print(f"{SIGNAL_GRACE_ENV} must be non-negative", file=sys.stderr)
        return 2
    capacity_fd = inherited_capacity_fd()
    inherited_fds = () if capacity_fd is None else (capacity_fd,)
    if capacity_fd is None:
        env.pop(CAPACITY_LOCK_FD, None)
    guardian, guardian_control_fd = start_guardian(
        token, preexisting_indeterminate, inherited_fds
    )
    try:
        child = subprocess.Popen(
            [
                sys.executable,
                str(Path(__file__).resolve()),
                "--supervised-child",
                str(guardian_control_fd),
                *command,
            ],
            env=env,
            pass_fds=(guardian_control_fd, *inherited_fds),
            start_new_session=True,
        )
    except BaseException:
        stop_guardian(
            guardian,
            guardian_control_fd,
            None,
            token,
            preexisting_indeterminate,
        )
        raise
    session_id = child.pid
    forwarded_signal = 0
    forwarded_at = 0.0
    escalated = False

    def forward(signum: int, _frame: object) -> None:
        nonlocal forwarded_signal, forwarded_at
        forwarded_signal = signum
        if not forwarded_at:
            forwarded_at = time.monotonic()
        signal_owned(
            session_id, token, signal.Signals(signum), preexisting_indeterminate
        )

    for sig in (signal.SIGHUP, signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, forward)

    try:
        startup_barrier = os.environ.get(
            "BEAMFALL_PROCESS_SUPERVISOR_STARTUP_BARRIER"
        )
        if startup_barrier:
            ready = Path(f"{startup_barrier}.ready")
            proceed = Path(f"{startup_barrier}.go")
            fail = Path(f"{startup_barrier}.fail")
            ready.write_text(f"{child.pid}\n", encoding="utf-8")
            while (
                not proceed.exists()
                and not fail.exists()
                and not forwarded_signal
                and child.poll() is None
            ):
                time.sleep(0.01)
            if fail.exists():
                raise RuntimeError(
                    "forced process-supervisor startup-barrier failure"
                )
        while True:
            return_code = child.poll()
            if return_code is not None:
                break
            if forwarded_at and time.monotonic() - forwarded_at >= signal_grace:
                escalated = True
                signal_owned(
                    session_id, token, signal.SIGKILL, preexisting_indeterminate
                )
            time.sleep(0.05)
    finally:
        # A successful shell may have joined its children while the kernel is
        # still retiring their process-group entries. Give that clean path a
        # short bounded drain; cancellation still starts containment at once.
        cleanly_drained = (
            not forwarded_signal
            and wait_owned_quiescent(
                session_id,
                token,
                preexisting_indeterminate,
                grace_seconds=quiescence_grace,
            )
        )
        cleanup_inventory = (
            []
            if cleanly_drained
            else owned_process_inventory(
                session_id, token, preexisting_indeterminate
            )
        )
        leaked = (
            False
            if cleanly_drained
            else reap_owned(session_id, token, preexisting_indeterminate)
        )
        guardian_stopped = stop_guardian(
            guardian,
            guardian_control_fd,
            session_id,
            token,
            preexisting_indeterminate,
        )

    if return_code < 0:
        return_code = 128 - return_code
    if forwarded_signal and (return_code == 0 or escalated):
        return_code = 128 + forwarded_signal
    if leaked and return_code == 0:
        print("test process supervisor reaped residual processes", file=sys.stderr)
        return_code = 1
    if not guardian_stopped and return_code == 0:
        print(
            f"test process supervisor guardian failed (exit={guardian.returncode})",
            file=sys.stderr,
        )
        return_code = 1
    if cleanup_inventory:
        print(
            "test process supervisor cleanup inventory:\n"
            "PID PPID PGID SID COMMAND\n"
            + "\n".join(cleanup_inventory),
            file=sys.stderr,
        )
    return return_code


if __name__ == "__main__":
    raise SystemExit(main())
