#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import os
from pathlib import Path
import secrets
import select
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parent.parent
SUPERVISOR = ROOT / "script/test-process-supervisor.py"
SPEC = importlib.util.spec_from_file_location("test_process_supervisor", SUPERVISOR)
assert SPEC and SPEC.loader
PROCESS_SUPERVISOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROCESS_SUPERVISOR)

OWNER = r'''
import os, signal
ready_fd = int(os.environ.pop("TOKEN_TEST_READY_FD"))
os.write(ready_fd, b"R")
os.close(ready_fd)
while True:
    signal.pause()
'''


def token_in_guardian_argv(pid: int, token: str) -> bool:
    if sys.platform == "darwin":
        # KERN_PROCARGS2 backs process_has_token on Darwin and covers both argv
        # and environment; a raw token followed by NUL catches either channel.
        visible = PROCESS_SUPERVISOR.process_has_token(pid, token.encode("ascii"))
        if visible is None:
            raise AssertionError("guardian argv/environment inspection was indeterminate")
        return visible
    proc = Path("/proc")
    if proc.is_dir():
        return token.encode("ascii") + b"\0" in (proc / str(pid) / "cmdline").read_bytes()
    raise AssertionError("no direct guardian argv inspection is available")


def start_owner(token: str) -> subprocess.Popen[bytes]:
    read_fd, write_fd = os.pipe()
    env = os.environ.copy()
    env[PROCESS_SUPERVISOR.token_env_name(token)] = token
    env["TOKEN_TEST_READY_FD"] = str(write_fd)
    try:
        child = subprocess.Popen(
            [sys.executable, "-c", OWNER],
            cwd=ROOT,
            env=env,
            pass_fds=(write_fd,),
        )
    finally:
        os.close(write_fd)
    try:
        readable, _, _ = select.select([read_fd], [], [], 3.0)
        if not readable or os.read(read_fd, 1) != b"R":
            raise AssertionError("token-owned child did not become ready")
    except BaseException:
        child.kill()
        child.wait()
        raise
    finally:
        os.close(read_fd)
    return child


def start_scoped_guardian(token: str, owner_pid: int) -> tuple[object, int]:
    original_file = PROCESS_SUPERVISOR.__file__
    original_owner = os.environ.get("TOKEN_TEST_OWNER_PID")
    try:
        # Exercise the production fork/exec + secret-FD launcher, but exec this
        # test's bounded inventory adapter so unrelated sandbox-denied host
        # processes cannot make the behavioral oracle indeterminate.
        PROCESS_SUPERVISOR.__file__ = __file__
        os.environ["TOKEN_TEST_OWNER_PID"] = str(owner_pid)
        return PROCESS_SUPERVISOR.start_guardian(token, frozenset())
    finally:
        PROCESS_SUPERVISOR.__file__ = original_file
        if original_owner is None:
            os.environ.pop("TOKEN_TEST_OWNER_PID", None)
        else:
            os.environ["TOKEN_TEST_OWNER_PID"] = original_owner


def stop_process(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is not None:
        process.wait()
        return
    process.terminate()
    try:
        process.wait(timeout=1.0)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()


class ProcessSupervisorTokenTest(unittest.TestCase):
    def guardian_reaps_owner(self, guardian_token: str, owner_token: str, *, check_secrecy: bool) -> bool:
        owner = start_owner(owner_token)
        guardian = None
        control_fd = None
        try:
            owner_entry = f"{PROCESS_SUPERVISOR.token_env_name(owner_token)}={owner_token}".encode("ascii")
            self.assertIs(
                PROCESS_SUPERVISOR.process_has_token(owner.pid, owner_entry),
                True,
                "ready child did not expose its exact ownership token",
            )
            guardian, control_fd = start_scoped_guardian(guardian_token, owner.pid)
            if check_secrecy:
                token_entry = f"{PROCESS_SUPERVISOR.token_env_name(guardian_token)}={guardian_token}".encode("ascii")
                self.assertFalse(token_in_guardian_argv(guardian.pid, guardian_token))
                self.assertIs(
                    PROCESS_SUPERVISOR.process_has_token(guardian.pid, token_entry),
                    False,
                    "guardian inherited the ownership token in its environment",
                )
            os.close(control_fd)
            control_fd = None
            try:
                owner.wait(timeout=1.0)
                reaped = True
            except subprocess.TimeoutExpired:
                reaped = False
            # Reap the owner before joining the guardian: on Darwin an
            # unreaped child zombie can have neither procargs nor an identity,
            # which the fail-closed guardian correctly treats as indeterminate.
            self.assertEqual(guardian.wait(timeout=3.0), 0)
            return reaped
        finally:
            if control_fd is not None:
                os.close(control_fd)
            if guardian is not None and guardian.returncode is None:
                guardian.kill()
                guardian.wait()
            stop_process(owner)

    def test_guardian_receives_exact_token_by_fd_not_argv_or_environment(self) -> None:
        token = secrets.token_hex(24)
        self.assertTrue(self.guardian_reaps_owner(token, token, check_secrecy=True))
        substituted = secrets.token_hex(24)
        self.assertNotEqual(substituted, token)
        self.assertFalse(self.guardian_reaps_owner(substituted, token, check_secrecy=False))

    def test_guardian_rejects_disarm_with_owned_process(self) -> None:
        token = secrets.token_hex(24)
        owner = start_owner(token)
        guardian = None
        control_fd = None
        try:
            guardian, control_fd = start_scoped_guardian(token, owner.pid)
            os.write(control_fd, f"{owner.pid}\nD\n".encode("ascii"))
            os.close(control_fd)
            control_fd = None
            owner.wait(timeout=3.0)
            self.assertEqual(guardian.wait(timeout=3.0), 1)
        finally:
            if control_fd is not None:
                os.close(control_fd)
            if guardian is not None and guardian.returncode is None:
                guardian.kill()
                guardian.wait()
            stop_process(owner)

    def test_orphaned_guardian_self_terminates_when_inventory_stays_indeterminate(self) -> None:
        read_fd, write_fd = os.pipe()
        broker_pid = os.fork()
        if broker_pid == 0:
            os.close(read_fd)
            original_file = PROCESS_SUPERVISOR.__file__
            try:
                PROCESS_SUPERVISOR.__file__ = __file__
                os.environ["TOKEN_TEST_FORCE_INDETERMINATE"] = "1"
                guardian, control_fd = PROCESS_SUPERVISOR.start_guardian(
                    secrets.token_hex(24), frozenset()
                )
                os.write(write_fd, f"{guardian.pid}\n".encode("ascii"))
                os.close(control_fd)
            except BaseException:
                os._exit(70)
            finally:
                PROCESS_SUPERVISOR.__file__ = original_file
                os.close(write_fd)
            os._exit(0)

        os.close(write_fd)
        guardian_pid = None
        try:
            readable, _, _ = select.select([read_fd], [], [], 3.0)
            self.assertTrue(readable, "orphan guardian broker did not report its child")
            guardian_pid = int(os.read(read_fd, 32))
            _, broker_status = os.waitpid(broker_pid, 0)
            self.assertEqual(os.waitstatus_to_exitcode(broker_status), 0)
            identity = PROCESS_SUPERVISOR.process_identity(guardian_pid)
            self.assertIsNotNone(
                identity, "orphan guardian exited before the probe observed it"
            )
            deadline = time.monotonic() + 3.0
            while (
                PROCESS_SUPERVISOR.process_identity(guardian_pid) == identity
                and time.monotonic() < deadline
            ):
                time.sleep(0.02)
            self.assertNotEqual(
                PROCESS_SUPERVISOR.process_identity(guardian_pid),
                identity,
                "orphan guardian retried indeterminate cleanup forever",
            )
        finally:
            os.close(read_fd)
            try:
                waited_pid, _ = os.waitpid(broker_pid, os.WNOHANG)
            except ChildProcessError:
                waited_pid = broker_pid
            if waited_pid == 0:
                try:
                    os.kill(broker_pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                try:
                    os.waitpid(broker_pid, 0)
                except ChildProcessError:
                    pass
            if guardian_pid is not None:
                try:
                    os.kill(guardian_pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass

    def test_abandoned_guardian_keeps_reaping_visible_owned_process(self) -> None:
        control_read_fd, control_write_fd = os.pipe()
        os.close(control_write_fd)
        token_read_fd, token_write_fd = os.pipe()
        os.write(token_write_fd, secrets.token_hex(24).encode("ascii"))
        os.close(token_write_fd)
        owned = [True]
        result = []
        with mock.patch.object(PROCESS_SUPERVISOR, "reap_owned"), mock.patch.object(
            PROCESS_SUPERVISOR,
            "owned_alive",
            side_effect=lambda *_args, **_kwargs: owned[0],
        ), mock.patch.object(
            PROCESS_SUPERVISOR,
            "owned_process_ids",
            side_effect=lambda *_args, **_kwargs: (
                ([12345], False) if owned[0] else ([], False)
            ),
        ), mock.patch.object(PROCESS_SUPERVISOR, "group_alive", return_value=False):
            guardian = threading.Thread(
                target=lambda: result.append(
                    PROCESS_SUPERVISOR.guardian_main(
                        control_read_fd, token_read_fd, frozenset()
                    )
                ),
                daemon=True,
            )
            guardian.start()
            time.sleep(PROCESS_SUPERVISOR.ORPHAN_INDETERMINATE_GRACE_SECONDS + 0.2)
            self.assertTrue(
                guardian.is_alive(),
                "guardian retired while a concrete owned process remained visible",
            )
            owned[0] = False
            guardian.join(timeout=1.0)
        self.assertFalse(guardian.is_alive())
        self.assertEqual(result, [0])

    def test_disarm_accepts_empty_confirmation_inventory(self) -> None:
        control_read_fd, control_write_fd = os.pipe()
        os.write(control_write_fd, b"D\n")
        os.close(control_write_fd)
        token_read_fd, token_write_fd = os.pipe()
        os.write(token_write_fd, secrets.token_hex(24).encode("ascii"))
        os.close(token_write_fd)
        with mock.patch.object(
            PROCESS_SUPERVISOR, "wait_owned_quiescent", return_value=False
        ), mock.patch.object(
            PROCESS_SUPERVISOR, "owned_process_inventory", return_value=[]
        ), mock.patch.object(PROCESS_SUPERVISOR, "reap_owned") as reap_owned:
            self.assertEqual(
                PROCESS_SUPERVISOR.guardian_main(
                    control_read_fd, token_read_fd, frozenset()
                ),
                0,
            )
        reap_owned.assert_not_called()

    def test_quiescence_requires_stable_absence(self) -> None:
        ownership = iter([False, True, False, False, False, False])
        with mock.patch.object(
            PROCESS_SUPERVISOR,
            "owned_alive",
            side_effect=lambda *_args, **_kwargs: next(ownership, False),
        ) as owned_alive:
            self.assertTrue(
                PROCESS_SUPERVISOR.wait_owned_quiescent(
                    12345, secrets.token_hex(24), grace_seconds=0.05
                )
            )
        self.assertGreaterEqual(
            owned_alive.call_count,
            4,
            "quiescence accepted the first empty ownership snapshot",
        )

    def test_reap_takes_one_inventory_sweep_per_round(self) -> None:
        # Two live rounds, then retirement. Each round's sweep must both prove
        # liveness and name the signal targets; a rescan per signal multiplied
        # loaded cleanup time past the SIGKILL-holder wrapper budget.
        inventory = iter([([4242], False), ([4242], False), ([], False)])
        with mock.patch.object(
            PROCESS_SUPERVISOR,
            "owned_process_ids",
            side_effect=lambda *_args, **_kwargs: next(inventory, ([], False)),
        ) as owned_process_ids, mock.patch.object(PROCESS_SUPERVISOR.os, "kill") as kill:
            self.assertTrue(
                PROCESS_SUPERVISOR.reap_owned(
                    None, secrets.token_hex(24), grace_seconds=5.0
                )
            )
        self.assertEqual(owned_process_ids.call_count, 3)
        self.assertEqual(
            kill.call_args_list,
            [mock.call(4242, signal.SIGTERM), mock.call(4242, signal.SIGCONT)] * 2,
        )

    def test_reap_escalation_takes_one_inventory_sweep_per_kill_round(self) -> None:
        # A fake clock ticks one second per sleep, so a two-second grace gives
        # exactly two TERM rounds before escalation. Escalation takes one fresh
        # sweep; every SIGKILL round then takes exactly one more.
        clock = [0.0]
        fake_time = mock.Mock()
        fake_time.monotonic.side_effect = lambda: clock[0]
        fake_time.sleep.side_effect = lambda _seconds: clock.__setitem__(
            0, clock[0] + 1.0
        )
        live = ([4242], False)
        inventory = iter([live, live, live, live, live, ([], False)])
        with mock.patch.object(PROCESS_SUPERVISOR, "time", fake_time), mock.patch.object(
            PROCESS_SUPERVISOR,
            "owned_process_ids",
            side_effect=lambda *_args, **_kwargs: next(inventory, ([], False)),
        ) as owned_process_ids, mock.patch.object(PROCESS_SUPERVISOR.os, "kill") as kill:
            self.assertTrue(
                PROCESS_SUPERVISOR.reap_owned(
                    None, secrets.token_hex(24), grace_seconds=2.0
                )
            )
        # Initial sweep, two TERM rounds, the escalation sweep, one live
        # SIGKILL round and the retiring sweep.
        self.assertEqual(owned_process_ids.call_count, 6)
        self.assertEqual(
            kill.call_args_list,
            [mock.call(4242, signal.SIGTERM), mock.call(4242, signal.SIGCONT)] * 3
            + [mock.call(4242, signal.SIGKILL)] * 2,
        )

    def test_reap_signals_live_group_when_inventory_names_no_pids(self) -> None:
        inventory = iter([([], False), ([], False)])
        with mock.patch.object(
            PROCESS_SUPERVISOR,
            "owned_process_ids",
            side_effect=lambda *_args, **_kwargs: next(inventory, ([], False)),
        ), mock.patch.object(
            PROCESS_SUPERVISOR, "group_alive", side_effect=[True, False]
        ), mock.patch.object(
            PROCESS_SUPERVISOR, "signal_group"
        ) as signal_group, mock.patch.object(PROCESS_SUPERVISOR.os, "kill") as kill:
            self.assertTrue(
                PROCESS_SUPERVISOR.reap_owned(
                    77, secrets.token_hex(24), grace_seconds=5.0
                )
            )
        self.assertEqual(
            signal_group.call_args_list,
            [mock.call(77, signal.SIGTERM), mock.call(77, signal.SIGCONT)],
        )
        kill.assert_not_called()

    def test_reap_treats_indeterminate_inventory_as_live(self) -> None:
        # An unreadable owned process is not proof of retirement: the group is
        # still signalled and the reap reports that it had work to do.
        inventory = iter([([], True), ([], False)])
        with mock.patch.object(
            PROCESS_SUPERVISOR,
            "owned_process_ids",
            side_effect=lambda *_args, **_kwargs: next(inventory, ([], False)),
        ), mock.patch.object(
            PROCESS_SUPERVISOR, "group_alive", return_value=False
        ), mock.patch.object(
            PROCESS_SUPERVISOR, "signal_group"
        ) as signal_group, mock.patch.object(PROCESS_SUPERVISOR.os, "kill") as kill:
            self.assertTrue(
                PROCESS_SUPERVISOR.reap_owned(
                    77, secrets.token_hex(24), grace_seconds=5.0
                )
            )
        self.assertEqual(
            signal_group.call_args_list,
            [mock.call(77, signal.SIGTERM), mock.call(77, signal.SIGCONT)],
        )
        kill.assert_not_called()

    def test_unreadable_concurrent_peer_does_not_poison_ownership(self) -> None:
        protected = frozenset({(150, 1)})
        with mock.patch.object(
            PROCESS_SUPERVISOR, "all_process_ids", return_value=[200]
        ), mock.patch.object(
            PROCESS_SUPERVISOR, "process_matches_effective_uid", return_value=None
        ), mock.patch.object(
            PROCESS_SUPERVISOR, "process_has_token", return_value=None
        ), mock.patch.object(
            PROCESS_SUPERVISOR, "process_parent_id", side_effect=lambda pid: {200: 150}[pid]
        ), mock.patch.object(
            PROCESS_SUPERVISOR,
            "process_identity",
            side_effect=lambda pid: {200: (200, 2), 150: (150, 1)}[pid],
        ), mock.patch("os.getsid", return_value=999):
            self.assertEqual(
                PROCESS_SUPERVISOR.owned_process_ids(100, "a" * 48, protected),
                ([], False),
            )

    def test_unreadable_owned_descendant_remains_indeterminate(self) -> None:
        protected = frozenset({(150, 1)})
        with mock.patch.object(
            PROCESS_SUPERVISOR, "all_process_ids", return_value=[200]
        ), mock.patch.object(
            PROCESS_SUPERVISOR, "process_matches_effective_uid", return_value=None
        ), mock.patch.object(
            PROCESS_SUPERVISOR, "process_has_token", return_value=None
        ), mock.patch.object(
            PROCESS_SUPERVISOR, "process_parent_id", return_value=100
        ), mock.patch.object(
            PROCESS_SUPERVISOR, "process_identity", return_value=(200, 2)
        ), mock.patch("os.getsid", return_value=999):
            self.assertEqual(
                PROCESS_SUPERVISOR.owned_process_ids(100, "b" * 48, protected),
                ([], True),
            )

    def test_only_shard_supervisors_get_extended_quiescence(self) -> None:
        with mock.patch.dict(os.environ, {}, clear=True):
            self.assertEqual(
                PROCESS_SUPERVISOR.quiescence_grace_seconds(
                    "ROADMAP_TEST_SHARD_7_SUPERVISED"
                ),
                PROCESS_SUPERVISOR.SHARD_QUIESCENCE_GRACE_SECONDS,
            )
            self.assertEqual(
                PROCESS_SUPERVISOR.quiescence_grace_seconds(
                    "ROADMAP_FINISH_GATE_SUPERVISED"
                ),
                0.15,
            )
        ancestor_token = PROCESS_SUPERVISOR.token_env_name("c" * 48)
        with mock.patch.dict(os.environ, {ancestor_token: "c" * 48}, clear=True):
            self.assertEqual(
                PROCESS_SUPERVISOR.quiescence_grace_seconds(
                    "ROADMAP_MERGE_GATE_SUPERVISED"
                ),
                PROCESS_SUPERVISOR.SHARD_QUIESCENCE_GRACE_SECONDS,
            )


if __name__ == "__main__":
    if sys.argv[1:2] == ["--guardian"]:
        if os.environ.pop("TOKEN_TEST_FORCE_INDETERMINATE", None) == "1":
            PROCESS_SUPERVISOR.owned_process_ids = lambda *_args, **_kwargs: ([], True)
        else:
            owner_pid = int(os.environ.pop("TOKEN_TEST_OWNER_PID"))

            def scoped_process_ids() -> list[int]:
                try:
                    os.kill(owner_pid, 0)
                except ProcessLookupError:
                    return []
                return [owner_pid]

            PROCESS_SUPERVISOR.all_process_ids = scoped_process_ids
        protected = frozenset(
            tuple(map(int, identity.split(":")))
            for identity in sys.argv[4].split(",")
            if identity
        )
        raise SystemExit(
            PROCESS_SUPERVISOR.guardian_main(int(sys.argv[2]), int(sys.argv[3]), protected)
        )
    unittest.main()
