#!/usr/bin/env python3

from __future__ import annotations

import fcntl
import os
from pathlib import Path
import signal
import shutil
import subprocess
import sys
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
CAPACITY = ROOT / "script/gate-capacity.py"
LATEST = ROOT / "tools/gate/gate_latest.py"
SUPERVISOR = ROOT / "script/test-process-supervisor.py"
CAPACITY_LOCK_FD = "BEAMFALL_GATE_CAPACITY_FD"

FIXTURE = r'''#!/usr/bin/env python3
import fcntl
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

mode, label, events, active, auxiliary, delay = sys.argv[1:]

def append(value):
    with open(events, "a", encoding="utf-8") as stream:
        stream.write(value + "\n")
        stream.flush()

append(f"proc {os.getpid()} pgid {os.getpgrp()}")

if mode == "descendant":
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    signal.signal(signal.SIGHUP, signal.SIG_IGN)
    append(f"pid {os.getpid()}")
    if label == "child":
        subprocess.Popen([
            sys.executable, __file__, "descendant", "grandchild", events,
            active, auxiliary, delay,
        ])
    while True:
        time.sleep(1)

if mode == "barrier":
    barrier = Path(auxiliary)
    barrier.mkdir(parents=True, exist_ok=True)
    (barrier / f"{label}.ready").touch()
    deadline = time.monotonic() + 5
    while len(list(barrier.glob("*.ready"))) < 2 and time.monotonic() < deadline:
        time.sleep(0.02)
    raise SystemExit(0 if len(list(barrier.glob("*.ready"))) == 2 else 92)

lock = open(active, "a+b")
try:
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
except BlockingIOError:
    append(f"overlap {label}")
    raise SystemExit(91)

append(f"start {label}")
if mode == "stubborn":
    subprocess.Popen([
        sys.executable, __file__, "descendant", "child", events,
        active, auxiliary, delay,
    ])
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    signal.signal(signal.SIGHUP, signal.SIG_IGN)
time.sleep(float(delay))
append(f"end {label}")
'''


class GateCapacityTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.directory = Path(self.temporary.name)
        self.fixture = self.directory / "fixture.py"
        self.fixture.write_text(FIXTURE, encoding="utf-8")
        self.events = self.directory / "events"
        self.active = self.directory / "active.lock"
        self.pids = self.directory / "pids"
        self.processes: list[subprocess.Popen[bytes]] = []
        self.process_groups: set[int] = set()
        self.environment = os.environ.copy()
        self.environment.pop(CAPACITY_LOCK_FD, None)

    def tearDown(self) -> None:
        try:
            for process in self.processes:
                if process.poll() is None:
                    try:
                        os.killpg(process.pid, signal.SIGTERM)
                    except ProcessLookupError:
                        pass
            for process in self.processes:
                if process.poll() is None:
                    try:
                        process.wait(timeout=2)
                    except subprocess.TimeoutExpired:
                        try:
                            os.killpg(process.pid, signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                        process.wait(timeout=2)
            try:
                event_lines = self.events.read_text(encoding="utf-8").splitlines()
            except FileNotFoundError:
                event_lines = []
            self.process_groups.update(
                int(parts[3])
                for line in event_lines
                for parts in [line.split()]
                if len(parts) == 4 and parts[:1] == ["proc"] and parts[2] == "pgid"
            )
            deadline = time.monotonic() + 2
            live_groups = self.live_process_groups()
            while live_groups and time.monotonic() < deadline:
                time.sleep(0.02)
                live_groups = self.live_process_groups()
            if live_groups:
                for group_id in live_groups:
                    try:
                        os.killpg(group_id, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                self.fail(f"test leaked process groups: {sorted(live_groups)}")
        finally:
            self.temporary.cleanup()

    def live_process_groups(self) -> set[int]:
        live = set()
        for group_id in self.process_groups:
            try:
                os.killpg(group_id, 0)
            except ProcessLookupError:
                continue
            live.add(group_id)
        return live

    def run_command(
        self,
        command: list[str],
        environment: dict[str, str] | None = None,
        *,
        timeout: float = 5,
    ) -> int:
        process = self.start(command, environment)
        try:
            return process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait(timeout=2)
            raise

    def nested_command(self, cwd: Path, label: str) -> list[str]:
        return [
            "/bin/bash",
            "-c",
            'cd "$1" && shift && exec "$@"',
            "gate-capacity",
            str(cwd),
            sys.executable,
            str(CAPACITY),
            "--",
            sys.executable,
            str(CAPACITY),
            "--",
            sys.executable,
            str(self.fixture),
            "serial",
            label,
            str(self.events),
            str(self.active),
            str(self.pids),
            "0",
        ]

    def repo(self, name: str) -> tuple[Path, Path]:
        repo = self.directory / name
        worktree = self.directory / f"{name}-worktree"
        repo.mkdir()
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=repo, check=True)
        subprocess.run(
            ["git", "config", "user.email", "gate-capacity@example.invalid"],
            cwd=repo,
            check=True,
        )
        subprocess.run(
            ["git", "config", "user.name", "Gate Capacity Test"],
            cwd=repo,
            check=True,
        )
        (repo / "tracked").write_text("base\n", encoding="utf-8")
        runner = repo / "tools/gate/gate_latest.py"
        runner.parent.mkdir(parents=True)
        shutil.copy2(LATEST, runner)
        subprocess.run(
            ["git", "add", "tracked", "tools/gate/gate_latest.py"],
            cwd=repo,
            check=True,
        )
        subprocess.run(["git", "commit", "-q", "-m", "base"], cwd=repo, check=True)
        subprocess.run(
            ["git", "worktree", "add", "-q", "-b", f"{name}-linked", worktree, "main"],
            cwd=repo,
            check=True,
        )
        return repo, worktree

    def command(
        self,
        cwd: Path,
        mode: str,
        label: str,
        delay: float,
        *,
        supervised: bool = False,
    ) -> list[str]:
        worker = [
            sys.executable,
            str(self.fixture),
            mode,
            label,
            str(self.events),
            str(self.active),
            str(self.pids),
            str(delay),
        ]
        if supervised:
            worker = [
                sys.executable,
                str(SUPERVISOR),
                "GATE_CAPACITY_TEST_SUPERVISED",
                *worker,
            ]
        return [
            "/bin/bash",
            "-c",
            'cd "$1" && exec "$2" "$3" -- "${@:4}"',
            "gate-capacity",
            str(cwd),
            sys.executable,
            str(CAPACITY),
            *worker,
        ]

    def start(
        self,
        command: list[str],
        environment: dict[str, str] | None = None,
    ):
        process = subprocess.Popen(
            command,
            env=self.environment if environment is None else environment,
            start_new_session=True,
        )
        self.processes.append(process)
        self.process_groups.add(process.pid)
        return process

    def wait_for(self, predicate, message: str) -> None:
        deadline = time.monotonic() + 5
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

    def capacity_held(self, cwd: Path) -> bool:
        return subprocess.run(
            [sys.executable, str(CAPACITY), "--held", str(cwd)],
            check=False,
            env=self.environment,
        ).returncode == 0

    def assert_process_gone(self, pid: int) -> None:
        def gone() -> bool:
            try:
                os.kill(pid, 0)
                return False
            except ProcessLookupError:
                return True

        self.wait_for(gone, f"gate descendant survived interruption: {pid}")

    def test_linked_worktrees_serialize_without_cancelling_holder(self) -> None:
        repo, linked = self.repo("shared")
        first = self.start(self.command(repo, "serial", "first", 0.4))
        self.wait_for(lambda: self.event_seen("start first"), "first gate did not start")
        self.assertTrue(self.capacity_held(repo))
        self.assertTrue(self.capacity_held(linked))
        second = self.start(self.command(linked, "serial", "second", 0))
        time.sleep(0.1)
        self.assertIsNone(second.poll(), "standalone second gate did not block")
        self.assertFalse(self.event_seen("start second"))
        self.assertEqual(first.wait(timeout=5), 0)
        self.assertEqual(second.wait(timeout=5), 0)
        self.assertFalse(self.capacity_held(repo))
        self.assertEqual(
            self.events.read_text(encoding="utf-8").splitlines(),
            [
                f"proc {first.pid} pgid {first.pid}",
                "start first",
                "end first",
                f"proc {second.pid} pgid {second.pid}",
                "start second",
                "end second",
            ],
        )

    def test_fresh_acquisition_exports_private_lock_for_inherited_compose(
        self,
    ) -> None:
        repo, _ = self.repo("compose")
        lock_path = repo / ".git" / "beamfall-gate.capacity.lock"
        self.assertFalse(lock_path.exists())
        self.assertEqual(self.run_command(self.nested_command(repo, "nested")), 0)
        self.assertTrue(lock_path.is_file())
        self.assertFalse(self.capacity_held(repo))
        self.assertIn("start nested", self.events.read_text(encoding="utf-8"))

    def test_supervisor_discards_closed_inherited_capacity_fd(self) -> None:
        repo, _ = self.repo("stale-supervisor")
        source_fd = os.open(os.devnull, os.O_RDONLY)
        try:
            stale_fd = fcntl.fcntl(source_fd, fcntl.F_DUPFD_CLOEXEC, 64)
        finally:
            os.close(source_fd)
        os.close(stale_fd)
        environment = self.environment.copy()
        environment[CAPACITY_LOCK_FD] = str(stale_fd)

        result = subprocess.run(
            [
                sys.executable,
                str(SUPERVISOR),
                "GATE_CAPACITY_TEST_STALE_FD",
                sys.executable,
                str(CAPACITY),
                "--",
                sys.executable,
                str(self.fixture),
                "serial",
                "stale-supervisor",
                str(self.events),
                str(self.active),
                str(self.pids),
                "0",
            ],
            cwd=repo,
            env=environment,
            capture_output=True,
            text=True,
            timeout=5,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        diagnostic = (
            f"test process supervisor discarded stale {CAPACITY_LOCK_FD}={stale_fd}"
        )
        self.assertEqual(result.stderr.splitlines().count(diagnostic), 1)
        self.assertEqual(
            self.events.read_text(encoding="utf-8").splitlines()[1:],
            ["start stale-supervisor", "end stale-supervisor"],
        )
        self.assertTrue(
            (repo / ".git" / "beamfall-gate.capacity.lock").is_file()
        )
        self.assertFalse(self.capacity_held(repo))

    def test_distinct_repositories_run_in_parallel(self) -> None:
        first_repo, _ = self.repo("repo-a")
        second_repo, _ = self.repo("repo-b")
        first = self.start(
            self.command(first_repo, "barrier", "repo-a", 0),
        )
        second = self.start(
            self.command(second_repo, "barrier", "repo-b", 0),
        )
        self.assertEqual(first.wait(timeout=5), 0)
        self.assertEqual(second.wait(timeout=5), 0)
        self.assertEqual(len(list(self.pids.glob("*.ready"))), 2)

    def test_waiter_interruption_does_not_disturb_holder_or_lock(self) -> None:
        repo, linked = self.repo("waiter")
        first = self.start(self.command(repo, "serial", "holder", 0.5))
        self.wait_for(lambda: self.event_seen("start holder"), "holder did not start")
        waiter = self.start(self.command(linked, "serial", "waiter", 0))
        time.sleep(0.1)
        self.assertFalse(self.event_seen("start waiter"))
        waiter.send_signal(signal.SIGTERM)
        self.assertEqual(waiter.wait(timeout=3), 143)
        self.assertEqual(first.wait(timeout=5), 0)
        replacement = self.run_command(
            self.command(linked, "serial", "replacement", 0),
            timeout=5,
        )
        self.assertEqual(replacement, 0)

    def test_holder_interruption_reaps_descendants_and_releases_lock(self) -> None:
        repo, linked = self.repo("holder")
        holder = self.start(
            self.command(repo, "stubborn", "holder", 60, supervised=True)
        )
        self.wait_for(
            lambda: self.event_seen("start holder")
            and sum(line.startswith("pid ") for line in self.events.read_text().splitlines())
            >= 2,
            "stubborn holder descendants did not start",
        )
        holder.send_signal(signal.SIGTERM)
        self.assertNotEqual(holder.wait(timeout=5), 0)
        for line in self.events.read_text(encoding="utf-8").splitlines():
            if line.startswith("pid "):
                self.assert_process_gone(int(line.removeprefix("pid ")))
        replacement = self.run_command(
            self.command(linked, "serial", "replacement", 0),
            timeout=5,
        )
        self.assertEqual(replacement, 0)

    def test_latest_wins_remains_outside_repository_capacity(self) -> None:
        repo, _ = self.repo("latest")
        state = self.directory / "latest-state"
        environment = self.environment.copy()
        environment["GATE_LATEST_STATE_DIR"] = str(state)
        environment["GATE_CANCEL_GRACE_SECONDS"] = "0.15"
        environment["GATE_WORKER_REAPS_DESCENDANTS"] = "1"
        environment["GATE_PROCESS_SUPERVISOR"] = str(SUPERVISOR)
        old = self.start(
            [sys.executable, str(repo / "tools/gate/gate_latest.py"), *self.command(
                repo, "stubborn", "old", 60, supervised=True
            )],
            environment,
        )
        self.wait_for(lambda: self.event_seen("start old"), "old gate did not start")
        newest = self.run_command(
            [sys.executable, str(repo / "tools/gate/gate_latest.py"), *self.command(
                repo, "serial", "new", 0, supervised=True
            )],
            environment,
            timeout=8,
        )
        self.assertEqual(newest, 0)
        self.assertEqual(old.wait(timeout=5), 75)
        self.assertNotIn("overlap new", self.events.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
