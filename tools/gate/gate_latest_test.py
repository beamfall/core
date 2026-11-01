#!/usr/bin/env python3

from __future__ import annotations

import fcntl
import importlib.util
import os
from pathlib import Path
import signal
import shutil
import subprocess
import sys
import tempfile
import time
from typing import Optional
import unittest


ROOT = Path(__file__).resolve().parents[2]
RUNNER = ROOT / "tools/gate/gate_latest.py"
CAPACITY = ROOT / "script/gate-capacity.py"
PROCESS_SUPERVISOR = ROOT / "script/test-process-supervisor.py"
PROCESS_SUPERVISOR_SPEC = importlib.util.spec_from_file_location(
    "gate_latest_process_supervisor", PROCESS_SUPERVISOR
)
assert PROCESS_SUPERVISOR_SPEC is not None
assert PROCESS_SUPERVISOR_SPEC.loader is not None
PROCESS_SUPERVISOR_MODULE = importlib.util.module_from_spec(PROCESS_SUPERVISOR_SPEC)
PROCESS_SUPERVISOR_SPEC.loader.exec_module(PROCESS_SUPERVISOR_MODULE)

FIXTURE = r'''#!/usr/bin/env python3
import fcntl
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

mode, label, events, active, auxiliary, delay, result = sys.argv[1:]

def append(path, value):
    with open(path, "a", encoding="utf-8") as stream:
        stream.write(value + "\n")
        stream.flush()

if mode == "descendant":
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    signal.signal(signal.SIGHUP, signal.SIG_IGN)
    append(auxiliary, str(os.getpid()))
    if label == "child":
        subprocess.Popen([
            sys.executable, __file__, "descendant", "grandchild", events,
            active, auxiliary, delay, result,
        ])
    while True:
        time.sleep(1)

Path(events).with_name(f"{label}.cache").write_text(
    os.environ.get("GOCACHE", "<unset>"), encoding="utf-8"
)
Path(events).with_name(f"{label}.run-id").write_text(
    os.environ.get("GATE_RUN_ID", "<unset>"), encoding="utf-8"
)
active_stream = open(active, "a+b")
try:
    fcntl.flock(active_stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
except BlockingIOError:
    append(events, f"overlap {label}")
    raise SystemExit(91)

append(events, f"start {label}")
if mode == "release":
    release = Path(auxiliary)
    (release.parent / f"{release.name}.ready").touch()
    while not release.exists():
        time.sleep(0.02)
elif mode == "stubborn":
    append(auxiliary, str(os.getpid()))
    subprocess.Popen([
        sys.executable, __file__, "descendant", "child", events,
        active, auxiliary, delay, result,
    ])
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    signal.signal(signal.SIGHUP, signal.SIG_IGN)
elif mode == "barrier":
    barrier = Path(auxiliary)
    barrier.mkdir(parents=True, exist_ok=True)
    (barrier / f"{label}.ready").touch()
    deadline = time.monotonic() + 5
    while len(list(barrier.glob("*.ready"))) < 2 and time.monotonic() < deadline:
        time.sleep(0.02)
    if len(list(barrier.glob("*.ready"))) < 2:
        raise SystemExit(92)
elif mode == "cache":
    cache = Path(os.environ["GOCACHE"])
    (cache / f"{label}.sentinel").write_text(label, encoding="utf-8")
    barrier = Path(auxiliary)
    barrier.mkdir(parents=True, exist_ok=True)
    (barrier / f"{label}.ready").touch()
    deadline = time.monotonic() + 5
    while len(list(barrier.glob("*.ready"))) < 2 and time.monotonic() < deadline:
        time.sleep(0.02)
    if len(list(barrier.glob("*.ready"))) < 2:
        raise SystemExit(93)
    cache_paths = [
        path.read_text(encoding="utf-8")
        for path in Path(events).parent.glob("*.cache")
    ]
    # GATE-GOCACHE-PERSIST-1: concurrent runs now share one persistent
    # default GOCACHE (both processes must see the identical path, and both
    # sentinels must coexist in it), not isolated per-run caches.
    if len(set(cache_paths)) != 1 or len(list(cache.glob("*.sentinel"))) != 2:
        raise SystemExit(94)
elif mode == "outer-checks":
    for check in ("web-pre", "pins", "vuln", "contracts", "web-post"):
        append(events, f"check {os.environ['GATE_RUN_ID']} {check}")
        time.sleep(float(delay))

time.sleep(float(delay))
append(events, f"end {label}")
raise SystemExit(int(result))
'''

CAPACITY_TRANSITION_PROBE = r'''#!/usr/bin/env python3
import importlib.util
import os
from pathlib import Path
import signal
import sys

capacity_path = Path(sys.argv[1])
interrupt_signal = int(sys.argv[2])
signal_state = sys.argv[3]
delivery_point = sys.argv[4]
command = sys.argv[6:]
spec = importlib.util.spec_from_file_location("gate_capacity_transition", capacity_path)
assert spec is not None
assert spec.loader is not None
capacity = importlib.util.module_from_spec(spec)
spec.loader.exec_module(capacity)

original_signal = signal.signal
original_sigpending = signal.sigpending
signal_calls = 0

if signal_state == "ignored":
    original_signal(interrupt_signal, signal.SIG_IGN)
elif signal_state == "blocked":
    signal.pthread_sigmask(signal.SIG_BLOCK, {interrupt_signal})

def interrupt_during_first_restoration(signum, handler):
    global signal_calls
    signal_calls += 1
    if signal_calls == 4:
        os.kill(os.getpid(), interrupt_signal)
    return original_signal(signum, handler)

def interrupt_after_pending_snapshot():
    pending = original_sigpending()
    os.kill(os.getpid(), interrupt_signal)
    return pending

if delivery_point == "restoration":
    capacity.signal.signal = interrupt_during_first_restoration
else:
    capacity.signal.sigpending = interrupt_after_pending_snapshot
raise SystemExit(capacity.run(command))
'''

SUPERVISOR_TIMEOUT_PROBE = r'''#!/usr/bin/env python3
import os
import sys

timeout_name = "BEAMFALL_PROCESS_SUPERVISOR_SIGNAL_GRACE_SECONDS"
if timeout_name in os.environ:
    print(
        f"TimeoutExpired: interruption timeout raised to {os.environ[timeout_name]}",
        file=sys.stderr,
    )
supervisor = os.environ["GATE_TEST_REAL_PROCESS_SUPERVISOR"]
os.execvpe(
    sys.executable,
    [sys.executable, supervisor, *sys.argv[1:]],
    os.environ,
)
'''


class GateLatestTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.directory = Path(self.temporary.name)
        self.fixture = self.directory / "fixture.py"
        self.fixture.write_text(FIXTURE, encoding="utf-8")
        self.capacity_transition_probe = self.directory / "capacity-transition.py"
        self.capacity_transition_probe.write_text(
            CAPACITY_TRANSITION_PROBE, encoding="utf-8"
        )
        self.supervisor_timeout_probe = self.directory / "supervisor-timeout.py"
        self.supervisor_timeout_probe.write_text(
            SUPERVISOR_TIMEOUT_PROBE, encoding="utf-8"
        )
        self.repository = self.init_fixture_repository(self.directory / "repository")
        self.runner = self.repository / "tools/gate/gate_latest.py"
        self.events = self.directory / "events"
        self.active = self.directory / "active.lock"
        self.pids = self.directory / "pids"
        # Per-test default persistent cache dir (GATE-GOCACHE-PERSIST-1):
        # every invocation() call in a test shares this path unless it sets
        # GOCACHE explicitly, mirroring the real default-GOCACHE reuse.
        self.gocache_dir = self.directory / "gocache"

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def git(self, repository: Path, *args: str) -> str:
        return subprocess.run(
            ["git", "-C", str(repository), *args],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()

    def init_fixture_repository(self, repository: Path) -> Path:
        repository.mkdir()
        self.git(repository, "init", "-q", "-b", "main")
        self.git(repository, "config", "user.email", "gate-test@example.invalid")
        self.git(repository, "config", "user.name", "Gate Test")
        runner = repository / "tools/gate/gate_latest.py"
        runner.parent.mkdir(parents=True)
        shutil.copy2(RUNNER, runner)
        self.git(repository, "add", "tools/gate/gate_latest.py")
        self.git(repository, "commit", "-q", "-m", "fixture")
        return repository

    def linked_worktree(self, name: str) -> Path:
        worktree = self.directory / name
        self.git(
            self.repository,
            "worktree",
            "add",
            "-q",
            "-b",
            f"fixture-{name}",
            str(worktree),
            "main",
        )
        return worktree

    def assert_capacity_available(self, repository: Path) -> None:
        common_dir = Path(
            self.git(
                repository,
                "rev-parse",
                "--path-format=absolute",
                "--git-common-dir",
            )
        ).resolve()
        with (common_dir / "beamfall-gate.capacity.lock").open("a+b") as stream:
            fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)

    def invocation(
        self,
        state: Path,
        mode: str,
        label: str,
        delay: float,
        result: int,
        *,
        active: Optional[Path] = None,
        auxiliary: Optional[Path] = None,
        supervised: bool = False,
        runner: Optional[Path] = None,
        coordinator_supervised: bool = False,
        capacity_wrapped: bool = False,
    ) -> tuple[list[str], dict[str, str]]:
        environment = os.environ.copy()
        environment["GATE_LATEST_STATE_DIR"] = str(state)
        environment["GATE_CANCEL_GRACE_SECONDS"] = "0.15"
        environment["GATE_PROCESS_SUPERVISOR"] = str(PROCESS_SUPERVISOR)
        environment.pop("GOCACHE", None)
        environment["GATE_LATEST_GOCACHE_DIR"] = str(self.gocache_dir)
        worker = [
            sys.executable,
            str(self.fixture),
            mode,
            label,
            str(self.events),
            str(active or self.active),
            str(auxiliary or self.pids),
            str(delay),
            str(result),
        ]
        if supervised:
            worker = [
                sys.executable,
                str(PROCESS_SUPERVISOR),
                "GATE_PROCESS_SUPERVISED",
                *worker,
            ]
        if capacity_wrapped:
            worker = [sys.executable, str(CAPACITY), "--", *worker]
        command = [sys.executable, str(runner or self.runner), *worker]
        if coordinator_supervised:
            command = [
                sys.executable,
                str(PROCESS_SUPERVISOR),
                "GATE_LATEST_COORDINATOR_TEST_SUPERVISED",
                *command,
            ]
        return command, environment

    def wait_for(self, predicate, message: str) -> None:
        # A readiness bound, not a speed assertion: under a loaded gate host
        # (load average 50+) supervised Python startups measured p50 3.0s and
        # max 5.9s (CORE-0954), so 5s produced false reds.
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if predicate():
                return
            time.sleep(0.02)
        self.fail(message)

    def event_seen(self, event: str) -> bool:
        try:
            return event in self.events.read_text(encoding="utf-8").splitlines()
        except FileNotFoundError:
            return False

    def cache_path(self, label: str) -> Path:
        return Path((self.directory / f"{label}.cache").read_text(encoding="utf-8"))

    def run_id(self, label: str) -> str:
        return (self.directory / f"{label}.run-id").read_text(encoding="utf-8")

    def assert_default_cache_persists(self, label: str) -> None:
        cache = self.cache_path(label)
        self.assertTrue(cache.is_absolute())
        self.assertEqual(cache, self.gocache_dir.resolve())
        self.assertTrue(cache.exists(), f"persistent GOCACHE missing: {cache}")

    def assert_process_gone(self, pid: int) -> None:
        def gone() -> bool:
            try:
                os.kill(pid, 0)
                return False
            except ProcessLookupError:
                return True

        self.wait_for(gone, f"cancelled gate descendant still alive: {pid}")

    def descendant_identities(self, root: int) -> set[tuple[int, int]]:
        descendants = {root}
        while True:
            before = len(descendants)
            for pid in PROCESS_SUPERVISOR_MODULE.all_process_ids():
                if PROCESS_SUPERVISOR_MODULE.process_parent_id(pid) in descendants:
                    descendants.add(pid)
            if len(descendants) == before:
                break
        identities = {
            identity
            for pid in descendants
            if (identity := PROCESS_SUPERVISOR_MODULE.process_identity(pid)) is not None
        }
        self.assertEqual(
            descendants,
            {pid for pid, _ in identities},
            "stable identity snapshot was incomplete",
        )
        return identities

    def cleanup_invocation(self, process: subprocess.Popen) -> None:
        if process.poll() is not None:
            return
        process.terminate()
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=3)

    def test_returns_worker_exit_code(self) -> None:
        for result in (0, 23):
            with self.subTest(result=result):
                label = f"result-{result}"
                command, environment = self.invocation(
                    self.directory / f"state-{result}", "exit", label, 0, result
                )
                completed = subprocess.run(command, env=environment, check=False, timeout=5)
                self.assertEqual(completed.returncode, result)
                self.assert_default_cache_persists(label)

    def test_capacity_wrapper_reuses_coordinator_lock(self) -> None:
        command, environment = self.invocation(
            self.directory / "nested-capacity-state",
            "exit",
            "nested-capacity",
            0,
            0,
            capacity_wrapped=True,
        )
        process = subprocess.Popen(command, env=environment)
        self.addCleanup(self.cleanup_invocation, process)
        try:
            returncode = process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            process.terminate()
            process.wait(timeout=3)
            self.fail("capacity wrapper deadlocked on the coordinator-owned lock")
        self.assertEqual(returncode, 0)
        self.assertTrue(self.event_seen("start nested-capacity"))
        self.assertTrue(self.event_seen("end nested-capacity"))
        self.assert_capacity_available(self.repository)

    def test_cancellation_during_common_dir_lookup_reports_signal(self) -> None:
        # CORE-0954: a group-delivered cancellation reaches the coordinator
        # and its git child together; the dead git child must not turn the
        # cancellation into a coordinator failure (70).
        fake_bin = self.directory / "signalling-git-bin"
        fake_bin.mkdir()
        fake_git = fake_bin / "git"
        fake_git.write_text(
            "#!/bin/sh\nkill -INT \"$PPID\"\nkill -INT $$\nsleep 5\n",
            encoding="utf-8",
        )
        fake_git.chmod(0o755)
        command, environment = self.invocation(
            self.directory / "lookup-signal-state", "exit", "lookup-signal", 0, 0
        )
        environment["PATH"] = f"{fake_bin}{os.pathsep}{environment['PATH']}"
        completed = subprocess.run(
            command, env=environment, check=False, capture_output=True, text=True, timeout=5
        )
        self.assertEqual(completed.returncode, 128 + signal.SIGINT, completed.stderr)
        self.assertNotIn("gate coordinator:", completed.stderr)
        self.assertFalse(self.event_seen("start lookup-signal"))

    def test_spawn_failure_leaves_persistent_cache_dir(self) -> None:
        environment = os.environ.copy()
        environment["GATE_LATEST_STATE_DIR"] = str(self.directory / "spawn-state")
        environment["GATE_LATEST_GOCACHE_DIR"] = str(self.gocache_dir)
        environment.pop("GOCACHE", None)
        completed = subprocess.run(
            [sys.executable, str(self.runner), "/does/not/exist/beamfall-gate-worker"],
            env=environment,
            check=False,
            capture_output=True,
            text=True,
            timeout=5,
        )
        self.assertEqual(completed.returncode, 70)
        # GATE-GOCACHE-PERSIST-1: the persistent default cache dir is
        # created (and left in place) before spawning the worker, so even a
        # spawn failure must not leave it missing or clean it up.
        self.assertTrue(self.gocache_dir.is_dir())

    def test_explicit_cache_is_forwarded_and_untouched(self) -> None:
        explicit = self.directory / "caller cache"
        explicit.mkdir()
        sentinel = explicit / "caller.sentinel"
        sentinel.write_text("keep", encoding="utf-8")
        for result in (0, 19):
            with self.subTest(result=result):
                label = f"explicit-{result}"
                command, environment = self.invocation(
                    self.directory / f"explicit-state-{result}", "exit", label, 0, result
                )
                environment["GOCACHE"] = str(explicit)
                completed = subprocess.run(command, env=environment, check=False, timeout=5)
                self.assertEqual(completed.returncode, result)
                self.assertEqual(self.cache_path(label), explicit)
                self.assertEqual(sentinel.read_text(encoding="utf-8"), "keep")

    def test_explicit_empty_cache_is_forwarded_unchanged(self) -> None:
        command, environment = self.invocation(
            self.directory / "empty-state", "exit", "empty", 0, 0
        )
        environment["GOCACHE"] = ""
        completed = subprocess.run(command, env=environment, check=False, timeout=5)
        self.assertEqual(completed.returncode, 0)
        self.assertEqual((self.directory / "empty.cache").read_text(encoding="utf-8"), "")

    def test_latest_cancels_stubborn_process_tree_before_start(self) -> None:
        state = self.directory / "state"
        old_command, old_environment = self.invocation(state, "stubborn", "old", 60, 0)
        old = subprocess.Popen(old_command, env=old_environment)
        self.addCleanup(self.cleanup_invocation, old)
        self.wait_for(
            lambda: self.event_seen("start old")
            and self.pids.exists()
            and len(self.pids.read_text(encoding="utf-8").splitlines()) >= 3,
            "stubborn process tree did not start",
        )

        new_command, new_environment = self.invocation(state, "exit", "new", 0, 37)
        newest = subprocess.run(new_command, env=new_environment, check=False, timeout=8)
        self.assertEqual(newest.returncode, 37)
        self.assertEqual(old.wait(timeout=5), 75)
        self.assertNotIn("overlap new", self.events.read_text(encoding="utf-8"))
        for raw_pid in self.pids.read_text(encoding="utf-8").splitlines():
            self.assert_process_gone(int(raw_pid))
        self.assert_default_cache_persists("old")
        self.assert_default_cache_persists("new")

    def test_each_newer_run_supersedes_the_current_owner(self) -> None:
        state = self.directory / "state"
        first_command, first_environment = self.invocation(state, "exit", "first", 60, 0)
        first = subprocess.Popen(first_command, env=first_environment)
        self.addCleanup(self.cleanup_invocation, first)
        self.wait_for(lambda: self.event_seen("start first"), "first gate did not start")

        second_command, second_environment = self.invocation(state, "exit", "second", 60, 0)
        second = subprocess.Popen(second_command, env=second_environment)
        self.addCleanup(self.cleanup_invocation, second)
        self.wait_for(lambda: self.event_seen("start second"), "second gate did not start")

        third_command, third_environment = self.invocation(state, "exit", "third", 0, 0)
        third = subprocess.run(third_command, env=third_environment, check=False, timeout=8)
        self.assertEqual(third.returncode, 0)
        self.assertEqual(first.wait(timeout=5), 75)
        self.assertEqual(second.wait(timeout=5), 75)
        self.assertFalse(any(line.startswith("overlap ") for line in self.events.read_text().splitlines()))
        self.assertEqual(
            {self.run_id(label) for label in ("first", "second", "third")},
            {"1", "2", "3"},
        )
        for label in ("first", "second", "third"):
            self.assert_default_cache_persists(label)

    def test_outer_checks_execute_once_for_winning_generation(self) -> None:
        state = self.directory / "outer-state"
        old_command, old_environment = self.invocation(
            state, "outer-checks", "outer-old", 0.15, 0
        )
        old = subprocess.Popen(old_command, env=old_environment)
        self.addCleanup(self.cleanup_invocation, old)
        self.wait_for(
            lambda: self.event_seen("check 1 web-pre"),
            "old outer checks did not start",
        )

        new_command, new_environment = self.invocation(
            state, "outer-checks", "outer-winner", 0, 0
        )
        winner = subprocess.run(new_command, env=new_environment, check=False, timeout=8)
        self.assertEqual(winner.returncode, 0)
        self.assertEqual(old.wait(timeout=5), 75)
        winning_checks = [
            line
            for line in self.events.read_text(encoding="utf-8").splitlines()
            if line.startswith("check 2 ")
        ]
        self.assertEqual(
            winning_checks,
            [
                "check 2 web-pre",
                "check 2 pins",
                "check 2 vuln",
                "check 2 contracts",
                "check 2 web-post",
            ],
        )

    def test_outer_check_failure_propagates(self) -> None:
        command, environment = self.invocation(
            self.directory / "outer-failure-state",
            "outer-checks",
            "outer-failure",
            0,
            29,
        )
        completed = subprocess.run(command, env=environment, check=False, timeout=5)
        self.assertEqual(completed.returncode, 29)
        checks = [
            line
            for line in self.events.read_text(encoding="utf-8").splitlines()
            if line.startswith("check 1 ")
        ]
        self.assertEqual(len(checks), 5)

    def test_signals_remove_cache_after_stubborn_descendants(self) -> None:
        for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            with self.subTest(signal=signal.Signals(signum).name):
                suffix = signal.Signals(signum).name.lower()
                self.events = self.directory / f"events-{suffix}"
                self.active = self.directory / f"active-{suffix}.lock"
                self.pids = self.directory / f"pids-{suffix}"
                command, environment = self.invocation(
                    self.directory / f"state-{suffix}", "stubborn", suffix, 60, 0
                )
                process = subprocess.Popen(command, env=environment)
                self.addCleanup(self.cleanup_invocation, process)
                self.wait_for(
                    lambda: self.event_seen(f"start {suffix}")
                    and self.pids.exists()
                    and len(self.pids.read_text(encoding="utf-8").splitlines()) >= 3,
                    f"{suffix} process tree did not start",
                )
                process.send_signal(signum)
                self.assertEqual(process.wait(timeout=5), 128 + signum)
                for raw_pid in self.pids.read_text(encoding="utf-8").splitlines():
                    self.assert_process_gone(int(raw_pid))
                self.assert_default_cache_persists(suffix)

    def test_canonical_wrapper_reaps_nested_session_before_signal_cleanup(self) -> None:
        for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            with self.subTest(signal=signal.Signals(signum).name):
                suffix = f"canonical-{signal.Signals(signum).name.lower()}"
                self.events = self.directory / f"events-{suffix}"
                self.active = self.directory / f"active-{suffix}.lock"
                self.pids = self.directory / f"pids-{suffix}"
                command, environment = self.invocation(
                    self.directory / f"state-{suffix}",
                    "stubborn",
                    suffix,
                    60,
                    0,
                    supervised=True,
                )
                environment["GATE_WORKER_REAPS_DESCENDANTS"] = "1"
                environment[
                    "BEAMFALL_PROCESS_SUPERVISOR_SIGNAL_GRACE_SECONDS"
                ] = "0"
                process = subprocess.Popen(command, env=environment)
                self.addCleanup(self.cleanup_invocation, process)
                self.wait_for(
                    lambda: self.event_seen(f"start {suffix}")
                    and self.pids.exists()
                    and len(self.pids.read_text(encoding="utf-8").splitlines()) >= 3,
                    f"{suffix} nested session did not start",
                )
                process.send_signal(signum)
                self.assertEqual(process.wait(timeout=5), 128 + signum)
                for raw_pid in self.pids.read_text(encoding="utf-8").splitlines():
                    self.assert_process_gone(int(raw_pid))
                self.assert_capacity_available(self.repository)
                self.assert_default_cache_persists(suffix)

    def test_canonical_wrapper_reaps_nested_session_before_supersede(self) -> None:
        suffix = "canonical-supersede"
        state = self.directory / "canonical-supersede-state"
        old_command, old_environment = self.invocation(
            state, "stubborn", suffix, 60, 0, supervised=True
        )
        old_environment["GATE_WORKER_REAPS_DESCENDANTS"] = "1"
        old = subprocess.Popen(old_command, env=old_environment)
        self.addCleanup(self.cleanup_invocation, old)
        self.wait_for(
            lambda: self.event_seen(f"start {suffix}")
            and self.pids.exists()
            and len(self.pids.read_text(encoding="utf-8").splitlines()) >= 3,
            "canonical nested session did not start",
        )

        new_command, new_environment = self.invocation(state, "exit", "replacement", 0, 0)
        newest = subprocess.run(new_command, env=new_environment, check=False, timeout=8)
        self.assertEqual(newest.returncode, 0)
        self.assertEqual(old.wait(timeout=5), 75)
        for raw_pid in self.pids.read_text(encoding="utf-8").splitlines():
            self.assert_process_gone(int(raw_pid))
        self.assert_default_cache_persists(suffix)
        self.assert_default_cache_persists("replacement")

    def test_canonical_marker_is_not_forwarded_to_nested_generic_suite(self) -> None:
        environment = os.environ.copy()
        environment["GATE_WORKER_REAPS_DESCENDANTS"] = "1"
        environment["GATE_LATEST_STATE_DIR"] = str(self.directory / "nested-suite-state")
        environment["GATE_PROCESS_SUPERVISOR"] = str(PROCESS_SUPERVISOR)
        command = [
            sys.executable,
            str(self.runner),
            sys.executable,
            str(PROCESS_SUPERVISOR),
            "GATE_NESTED_SUITE_SUPERVISED",
            sys.executable,
            str(ROOT / "tools/gate/gate_latest_test.py"),
            (
                "GateLatestTest.test_latest_cancels_stubborn_process_tree_before_start"
            ),
        ]
        completed = subprocess.run(
            command,
            cwd=ROOT,
            env=environment,
            check=False,
            capture_output=True,
            text=True,
            timeout=20,
        )
        self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)

    def test_linked_worktrees_serialize_without_cancelling_holder(self) -> None:
        linked = self.linked_worktree("linked-serialization")
        release = self.directory / "serialization.release"
        linked_state = self.directory / "state-linked"
        first_command, first_environment = self.invocation(
            self.directory / "state-primary",
            "release",
            "primary",
            0,
            0,
            auxiliary=release,
            runner=self.runner,
        )
        second_command, second_environment = self.invocation(
            linked_state,
            "exit",
            "linked",
            0,
            0,
            runner=linked / "tools/gate/gate_latest.py",
        )
        first = subprocess.Popen(first_command, env=first_environment)
        self.addCleanup(self.cleanup_invocation, first)
        self.wait_for(lambda: self.event_seen("start primary"), "primary gate did not start")
        second = subprocess.Popen(second_command, env=second_environment)
        self.addCleanup(self.cleanup_invocation, second)
        self.wait_for(
            lambda: (linked_state / "generation").exists(),
            "linked coordinator did not reach capacity queue",
        )
        time.sleep(0.15)
        self.assertIsNone(second.poll(), "linked gate did not queue")
        self.assertFalse(self.event_seen("start linked"))
        release.touch()
        self.assertEqual(first.wait(timeout=5), 0)
        self.assertEqual(second.wait(timeout=5), 0)
        self.assertEqual(
            self.events.read_text(encoding="utf-8").splitlines(),
            ["start primary", "end primary", "start linked", "end linked"],
        )
        self.assert_capacity_available(self.repository)

    def test_distinct_repositories_run_concurrently(self) -> None:
        second_repository = self.init_fixture_repository(
            self.directory / "second-repository"
        )
        barrier = self.directory / "barrier"
        first_command, first_environment = self.invocation(
            self.directory / "state-a",
            "barrier",
            "a",
            0,
            0,
            active=self.directory / "active-a",
            auxiliary=barrier,
            runner=self.runner,
        )
        second_command, second_environment = self.invocation(
            self.directory / "state-b",
            "barrier",
            "b",
            0,
            0,
            active=self.directory / "active-b",
            auxiliary=barrier,
            runner=second_repository / "tools/gate/gate_latest.py",
        )
        first = subprocess.Popen(first_command, env=first_environment)
        second = subprocess.Popen(second_command, env=second_environment)
        self.addCleanup(self.cleanup_invocation, first)
        self.addCleanup(self.cleanup_invocation, second)
        self.assertEqual(first.wait(timeout=8), 0)
        self.assertEqual(second.wait(timeout=8), 0)
        self.assert_default_cache_persists("a")
        self.assert_default_cache_persists("b")

    def test_queued_waiter_signals_leave_no_worker_or_capacity_owner(self) -> None:
        linked = self.linked_worktree("linked-queued-signals")
        for signum in (signal.SIGINT, signal.SIGTERM):
            with self.subTest(signal=signal.Signals(signum).name):
                suffix = signal.Signals(signum).name.lower()
                self.events = self.directory / f"queued-events-{suffix}"
                waiter_pids = self.directory / f"queued-pids-{suffix}"
                holder_release = self.directory / f"queued-holder-release-{suffix}"
                waiter_state = self.directory / f"waiter-state-{suffix}"
                holder_command, holder_environment = self.invocation(
                    self.directory / f"holder-state-{suffix}",
                    "release",
                    f"holder-{suffix}",
                    0,
                    0,
                    auxiliary=holder_release,
                    runner=self.runner,
                )
                holder = subprocess.Popen(holder_command, env=holder_environment)
                self.addCleanup(self.cleanup_invocation, holder)
                self.wait_for(
                    lambda: self.event_seen(f"start holder-{suffix}"),
                    f"{suffix} holder did not start",
                )
                waiter_command, waiter_environment = self.invocation(
                    waiter_state,
                    "stubborn",
                    f"waiter-{suffix}",
                    60,
                    0,
                    auxiliary=waiter_pids,
                    runner=linked / "tools/gate/gate_latest.py",
                    coordinator_supervised=True,
                )
                waiter = subprocess.Popen(waiter_command, env=waiter_environment)
                self.addCleanup(self.cleanup_invocation, waiter)
                try:
                    self.wait_for(
                        lambda: (waiter_state / "generation").exists(),
                        f"{suffix} waiter did not reach capacity queue",
                    )
                    self.assertIsNone(waiter.poll(), "capacity waiter did not queue")
                    self.assertFalse(self.event_seen(f"start waiter-{suffix}"))
                    waiter.send_signal(signum)
                    self.assertEqual(waiter.wait(timeout=5), 128 + signum)
                    self.assertFalse(waiter_pids.exists(), "queued waiter spawned descendants")
                finally:
                    # Release capacity even on failure so the next subtest's
                    # holder is not stranded behind this one.
                    holder_release.touch()
                self.assertEqual(holder.wait(timeout=5), 0)
                self.assertTrue(self.event_seen(f"end holder-{suffix}"))
                self.assert_capacity_available(self.repository)

    def test_capacity_handoff_signals_do_not_exec_or_raise_timeouts(self) -> None:
        timeout_name = "BEAMFALL_PROCESS_SUPERVISOR_SIGNAL_GRACE_SECONDS"
        scenarios = (
            ("default", "restoration"),
            ("ignored", "post-check"),
            ("blocked", "post-check"),
        )
        for signum, (signal_state, delivery_point) in (
            (signum, scenario)
            for signum in (signal.SIGHUP, signal.SIGINT, signal.SIGTERM)
            for scenario in scenarios
        ):
            with self.subTest(
                signal=signal.Signals(signum).name,
                state=signal_state,
                point=delivery_point,
            ):
                suffix = signal.Signals(signum).name.lower()
                case = f"{suffix}-{signal_state}-{delivery_point}"
                self.events = self.directory / f"handoff-events-{case}"
                release = self.directory / f"handoff-release-{case}"
                stderr_path = self.directory / f"handoff-stderr-{case}"
                worker_command, environment = self.invocation(
                    self.directory / f"handoff-state-{case}",
                    "release",
                    f"handoff-{case}",
                    0,
                    0,
                    auxiliary=release,
                )
                command = [
                    *worker_command[:2],
                    sys.executable,
                    str(self.capacity_transition_probe),
                    str(CAPACITY),
                    str(int(signum)),
                    signal_state,
                    delivery_point,
                    "--",
                    *worker_command[2:],
                ]
                environment["GATE_WORKER_REAPS_DESCENDANTS"] = "1"
                environment["GATE_PROCESS_SUPERVISOR"] = str(
                    self.supervisor_timeout_probe
                )
                environment["GATE_TEST_REAL_PROCESS_SUPERVISOR"] = str(
                    PROCESS_SUPERVISOR
                )
                environment.pop(timeout_name, None)
                with stderr_path.open("w", encoding="utf-8") as stderr:
                    process = subprocess.Popen(
                        command,
                        cwd=self.repository,
                        env=environment,
                        stderr=stderr,
                    )
                self.addCleanup(self.cleanup_invocation, process)
                try:
                    self.wait_for(
                        lambda: process.poll() is not None
                        or self.event_seen(f"start handoff-{case}"),
                        f"{case} handoff interruption produced no outcome",
                    )
                    with self.subTest(enforcement="interruption-status"):
                        self.assertEqual(process.poll(), 128 + signum)
                    with self.subTest(enforcement="capacity-release"):
                        self.assert_capacity_available(self.repository)
                    with self.subTest(enforcement="no-command"):
                        self.assertFalse(
                            self.event_seen(f"start handoff-{case}"),
                            "interrupted capacity handoff started its command",
                        )
                    with self.subTest(enforcement="no-raised-timeout"):
                        self.assertNotIn(
                            "timeout",
                            stderr_path.read_text(encoding="utf-8").lower(),
                        )
                finally:
                    self.cleanup_invocation(process)

    def test_running_holder_signals_release_capacity_after_descendants(self) -> None:
        linked = self.linked_worktree("linked-holder-signals")
        for signum in (signal.SIGINT, signal.SIGTERM):
            with self.subTest(signal=signal.Signals(signum).name):
                suffix = signal.Signals(signum).name.lower()
                self.events = self.directory / f"holder-events-{suffix}"
                self.pids = self.directory / f"holder-pids-{suffix}"
                waiter_state = self.directory / f"released-state-{suffix}"
                holder_command, holder_environment = self.invocation(
                    self.directory / f"running-state-{suffix}",
                    "stubborn",
                    f"running-{suffix}",
                    60,
                    0,
                    supervised=True,
                    runner=self.runner,
                )
                holder_environment["GATE_WORKER_REAPS_DESCENDANTS"] = "1"
                holder = subprocess.Popen(holder_command, env=holder_environment)
                self.addCleanup(self.cleanup_invocation, holder)
                self.wait_for(
                    lambda: self.event_seen(f"start running-{suffix}")
                    and self.pids.exists()
                    and len(self.pids.read_text(encoding="utf-8").splitlines()) >= 3,
                    f"{suffix} holder descendants did not start",
                )
                waiter_command, waiter_environment = self.invocation(
                    waiter_state,
                    "exit",
                    f"released-{suffix}",
                    0,
                    0,
                    runner=linked / "tools/gate/gate_latest.py",
                )
                waiter = subprocess.Popen(waiter_command, env=waiter_environment)
                self.addCleanup(self.cleanup_invocation, waiter)
                self.wait_for(
                    lambda: (waiter_state / "generation").exists(),
                    f"{suffix} waiter did not reach capacity queue",
                )
                self.assertFalse(self.event_seen(f"start released-{suffix}"))
                holder.send_signal(signum)
                self.assertEqual(holder.wait(timeout=5), 128 + signum)
                for raw_pid in self.pids.read_text(encoding="utf-8").splitlines():
                    self.assert_process_gone(int(raw_pid))
                self.assertEqual(waiter.wait(timeout=5), 0)
                self.assertTrue(self.event_seen(f"start released-{suffix}"))
                self.assert_capacity_available(self.repository)

    def test_sigkill_direct_holder_retires_stable_descendants_before_capacity(self) -> None:
        linked = self.linked_worktree("linked-holder-sigkill")
        suffix = "holder-sigkill"
        self.events = self.directory / f"events-{suffix}"
        self.pids = self.directory / f"pids-{suffix}"
        holder_command, holder_environment = self.invocation(
            self.directory / "holder-sigkill-state",
            "stubborn",
            suffix,
            60,
            0,
            supervised=True,
            runner=self.runner,
        )
        holder_environment["GATE_WORKER_REAPS_DESCENDANTS"] = "1"
        holder_wrapper = subprocess.Popen(holder_command, env=holder_environment)
        self.addCleanup(self.cleanup_invocation, holder_wrapper)
        self.wait_for(
            lambda: self.event_seen(f"start {suffix}")
            and self.pids.exists()
            and len(self.pids.read_text(encoding="utf-8").splitlines()) >= 3,
            "SIGKILL holder descendants did not start",
        )

        fixture_root = int(self.pids.read_text(encoding="utf-8").splitlines()[0])
        chain = [fixture_root]
        while chain[-1] != holder_wrapper.pid:
            parent = PROCESS_SUPERVISOR_MODULE.process_parent_id(chain[-1])
            self.assertIsNotNone(parent, f"incomplete holder ancestry: {chain}")
            self.assertNotIn(parent, chain, f"cyclic holder ancestry: {chain}")
            chain.append(parent)
        self.assertGreaterEqual(
            len(chain),
            4,
            "canonical coordinator was not independently parent-loss supervised",
        )
        direct_holder = chain[-2]
        holder_identities = self.descendant_identities(direct_holder)
        recorded = {
            int(raw_pid)
            for raw_pid in self.pids.read_text(encoding="utf-8").splitlines()
        }
        self.assertTrue(
            recorded <= {pid for pid, _ in holder_identities},
            "stable holder snapshot missed recorded descendants",
        )

        waiter_command, waiter_environment = self.invocation(
            self.directory / "holder-sigkill-waiter-state",
            "exit",
            "holder-sigkill-released",
            0,
            0,
            runner=linked / "tools/gate/gate_latest.py",
        )
        waiter = subprocess.Popen(waiter_command, env=waiter_environment)
        self.addCleanup(self.cleanup_invocation, waiter)
        self.wait_for(
            lambda: (
                self.directory / "holder-sigkill-waiter-state" / "generation"
            ).exists(),
            "SIGKILL waiter did not reach capacity queue",
        )
        self.assertFalse(self.event_seen("start holder-sigkill-released"))

        os.kill(direct_holder, signal.SIGKILL)
        self.assertEqual(holder_wrapper.wait(timeout=5), 137)
        self.wait_for(
            lambda: self.event_seen("start holder-sigkill-released"),
            "capacity remained unavailable after direct holder SIGKILL",
        )
        residual = {
            identity
            for identity in holder_identities
            if PROCESS_SUPERVISOR_MODULE.process_identity(identity[0]) == identity
        }
        self.assertFalse(
            residual,
            f"capacity became available before stable holder descendants retired: {residual}",
        )
        self.assertEqual(waiter.wait(timeout=5), 0)
        self.assert_capacity_available(self.repository)

    def test_distinct_runs_share_persistent_cache(self) -> None:
        # GATE-GOCACHE-PERSIST-1: concurrent runs without an explicit
        # GOCACHE now share the one persistent default cache dir (Go's
        # build/test cache is designed for concurrent access), instead of
        # each run getting an isolated, discarded tempdir.
        barrier = self.directory / "cache-barrier"
        second_repository = self.init_fixture_repository(
            self.directory / "cache-second-repository"
        )
        processes = []
        for label, runner in (
            ("cache-a", self.runner),
            ("cache-b", second_repository / "tools/gate/gate_latest.py"),
        ):
            command, environment = self.invocation(
                self.directory / f"state-{label}",
                "cache",
                label,
                0.1,
                0,
                active=self.directory / f"active-{label}",
                auxiliary=barrier,
                runner=runner,
            )
            process = subprocess.Popen(command, env=environment)
            self.addCleanup(self.cleanup_invocation, process)
            processes.append(process)
        for process in processes:
            self.assertEqual(process.wait(timeout=8), 0)
        caches = [self.cache_path(label) for label in ("cache-a", "cache-b")]
        self.assertEqual(caches[0], caches[1])
        for cache in caches:
            self.assertTrue(cache.is_absolute())
            self.assertTrue(cache.exists())

    def test_lint_inherits_caller_cache(self) -> None:
        binary_dir = self.directory / "bin"
        binary_dir.mkdir()
        capture = self.directory / "lint-environment"
        fake_lint = binary_dir / "golangci-lint"
        fake_lint.write_text(
            '#!/bin/sh\nprintf "%s\\n%s\\n" "$GOCACHE" "$*" > "$LINT_CAPTURE"\n',
            encoding="utf-8",
        )
        fake_lint.chmod(0o755)
        environment = os.environ.copy()
        environment["PATH"] = f"{binary_dir}{os.pathsep}{environment['PATH']}"
        environment["GOCACHE"] = str(self.directory / "lint cache")
        environment["LINT_CAPTURE"] = str(capture)
        completed = subprocess.run(
            ["make", "-s", "lint"],
            cwd=ROOT,
            env=environment,
            check=False,
            capture_output=True,
            text=True,
            timeout=5,
        )
        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual(
            capture.read_text(encoding="utf-8").splitlines(),
            [environment["GOCACHE"], "run --allow-serial-runners ./..."],
        )


if __name__ == "__main__":
    unittest.main()
