#!/usr/bin/env python3
"""Selftest for the machine-wide gate slot (script/gate-slot.py, CORE-1470).

Every case points the slot at its own temporary queue, so it neither sees nor
disturbs the real one, including when it runs inside a gate that holds it.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
SLOT = ROOT / "script/gate-slot.py"
LATEST = ROOT / "tools/gate/gate_latest.py"
CAPACITY = ROOT / "script/gate-capacity.py"
TIMEOUT = 30

# A stand-in gate: says when it starts, runs until told to stop, says when it ends.
HOLD = r'''
import sys
import time
from pathlib import Path

events, label, release = sys.argv[1:]

def append(value):
    with open(events, "a", encoding="utf-8") as stream:
        stream.write(value + "\n")

append(f"{label} start")
while not Path(release).exists():
    time.sleep(0.02)
append(f"{label} end")
'''


class SlotCase(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.directory = Path(self.temporary.name).resolve()
        self.store = self.directory / "slot"
        self.events = self.directory / "events"
        self.hold = self.directory / "hold.py"
        self.hold.write_text(HOLD, encoding="utf-8")
        self.repository = self.fixture_repository("repository")
        self.head = self.git(self.repository, "rev-parse", "--short", "HEAD")
        self.processes: list[subprocess.Popen] = []
        self.environment = {
            name: value
            for name, value in os.environ.items()
            if not name.startswith(("BEAMFALL_GATE_", "GATE_"))
        }
        self.environment["BEAMFALL_GATE_SLOT_DIR"] = str(self.store)
        self.environment["GOCACHE"] = str(self.directory / "gocache")

    def tearDown(self) -> None:
        for process in self.processes:
            if process.poll() is None:
                process.kill()
            process.wait()
            for stream in (process.stdout, process.stderr):
                if stream is not None:
                    stream.close()
        self.temporary.cleanup()

    def git(self, repository: Path, *arguments: str) -> str:
        return subprocess.run(
            ["git", "-C", str(repository), *arguments],
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()

    def fixture_repository(self, name: str) -> Path:
        """A repository carrying the coordinator and the slot, as core does."""
        repository = self.directory / name
        (repository / "tools/gate").mkdir(parents=True)
        (repository / "script").mkdir()
        shutil.copy2(LATEST, repository / "tools/gate/gate_latest.py")
        shutil.copy2(SLOT, repository / "script/gate-slot.py")
        self.git(repository, "init", "-q", "-b", "main")
        self.git(repository, "config", "user.email", "gate-test@example.invalid")
        self.git(repository, "config", "user.name", "Gate Test")
        self.git(repository, "add", ".")
        self.git(repository, "commit", "-q", "-m", "fixture")
        return repository

    def start(self, arguments: list[str], cwd: Path | None = None, **environment: str) -> subprocess.Popen:
        process = subprocess.Popen(
            arguments,
            cwd=cwd or self.repository,
            env={**self.environment, **environment},
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        self.processes.append(process)
        return process

    def slot(self, *command: str, options: tuple[str, ...] = (), cwd: Path | None = None, **environment: str):
        return self.start([sys.executable, str(SLOT), *options, "--", *command], cwd, **environment)

    def gate(self, *command: str, repository: Path | None = None, **environment: str):
        repository = repository or self.repository
        runner = repository / "tools/gate/gate_latest.py"
        return self.start([sys.executable, str(runner), "--", *command], repository, **environment)

    def holding(self, label: str) -> list[str]:
        return [sys.executable, str(self.hold), str(self.events), label, str(self.release(label))]

    def release(self, label: str) -> Path:
        return self.directory / f"release-{label}"

    def finish(self, process: subprocess.Popen) -> tuple[int, str]:
        _output, errors = process.communicate(timeout=TIMEOUT)
        return process.returncode, errors

    def seen(self) -> list[str]:
        if not self.events.exists():
            return []
        return self.events.read_text(encoding="utf-8").split("\n")[:-1]

    def entries(self) -> list[dict]:
        return [
            json.loads(path.read_text(encoding="utf-8"))
            for path in sorted((self.store / "queue").glob("*.json"))
        ]

    def wait_for(self, condition, what: str) -> None:
        deadline = time.monotonic() + TIMEOUT
        while not condition():
            if time.monotonic() > deadline:
                self.fail(f"timed out waiting for {what}; events={self.seen()}")
            time.sleep(0.02)

    def wait_started(self, label: str) -> None:
        self.wait_for(lambda: f"{label} start" in self.seen(), f"{label} to start")

    def wait_queued(self, count: int) -> None:
        self.wait_for(lambda: len(self.entries()) == count, f"{count} queue entries")

    def status(self) -> str:
        result = subprocess.run(
            [sys.executable, str(SLOT), "--status"],
            env=self.environment,
            capture_output=True,
            text=True,
            timeout=TIMEOUT,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout


class GateSlotTest(SlotCase):
    def test_gates_run_one_at_a_time_in_arrival_order(self) -> None:
        first = self.slot(*self.holding("first"))
        self.wait_started("first")
        second = self.slot(*self.holding("second"))
        self.wait_queued(2)
        third = self.slot(*self.holding("third"))
        self.wait_queued(3)
        self.release("third").touch()
        self.release("second").touch()
        time.sleep(1.5)
        self.assertEqual(self.seen(), ["first start"])

        self.release("first").touch()
        self.assertEqual(self.finish(first)[0], 0)
        second_status, second_errors = self.finish(second)
        third_status, third_errors = self.finish(third)
        self.assertEqual((second_status, third_status), (0, 0))
        self.assertEqual(
            self.seen(),
            ["first start", "first end", "second start", "second end", "third start", "third end"],
        )

        # The waiter names the holder (repository, head, pid, elapsed time,
        # command) and its own place.
        self.assertRegex(
            second_errors,
            r"waiting for the machine gate slot, queue position 1 of 1; held by "
            + f"{self.repository} \\(main @ {self.head}\\) pid {first.pid}, [0-9]+s: {re.escape(sys.executable)} ",
        )
        self.assertIn("queue position 2 of 2; held by", third_errors)
        self.assertIn(f"pid {first.pid}", third_errors)

    def test_command_status_is_returned_and_the_slot_freed(self) -> None:
        self.assertEqual(self.finish(self.slot("sh", "-c", "exit 7"))[0], 7)
        self.assertIn("gate-slot: free", self.status())
        self.assertEqual(self.entries(), [])
        self.assertEqual(self.finish(self.slot("sh", "-c", "exit 0"))[0], 0)

    def test_no_wait_refuses_a_held_slot_without_queueing(self) -> None:
        ran = self.directory / "ran"
        status, _errors = self.finish(self.slot("touch", str(ran), options=("--no-wait",)))
        self.assertEqual(status, 0)
        self.assertTrue(ran.exists())
        ran.unlink()

        holder = self.slot(*self.holding("holder"))
        self.wait_started("holder")
        for options, environment in ((("--no-wait",), {}), ((), {"BEAMFALL_GATE_SLOT_NOWAIT": "1"})):
            with self.subTest(options=options, environment=environment):
                status, errors = self.finish(self.slot("touch", str(ran), options=options, **environment))
                self.assertEqual(status, 69)
                self.assertIn("GATE_SLOT_BUSY held by", errors)
                self.assertIn(f"pid {holder.pid}", errors)
                self.assertFalse(ran.exists())
                self.assertEqual(len(self.entries()), 1)
        self.release("holder").touch()
        self.assertEqual(self.finish(holder)[0], 0)

    def test_killed_holder_is_reclaimed_by_the_next_waiter(self) -> None:
        holder = self.slot(*self.holding("holder"))
        self.wait_started("holder")
        waiter = self.slot(*self.holding("waiter"))
        self.wait_queued(2)
        holder.kill()
        # Unreaped, the holder is still a process table entry: gone all the same.
        self.wait_started("waiter")
        holder.wait()
        self.release("waiter").touch()
        status, errors = self.finish(waiter)
        self.assertEqual(status, 0)
        self.assertRegex(errors, f"holder .* pid {holder.pid}, .* has ended; removed its queue entry")
        self.assertNotIn("holder end", self.seen())

    def test_callers_in_different_time_zones_agree_on_a_live_holder(self) -> None:
        holder = self.slot(*self.holding("holder"), TZ="Asia/Tokyo")
        self.wait_started("holder")
        waiter = self.slot(*self.holding("waiter"), TZ="America/Toronto")
        self.wait_queued(2)
        time.sleep(1.0)
        self.assertEqual(self.seen(), ["holder start"])
        self.release("holder").touch()
        self.assertEqual(self.finish(holder)[0], 0)
        self.release("waiter").touch()
        status, errors = self.finish(waiter)
        self.assertEqual(status, 0, errors)
        self.assertEqual(self.seen(), ["holder start", "holder end", "waiter start", "waiter end"])

    def test_stale_entries_do_not_hold_the_slot(self) -> None:
        gone = subprocess.Popen(["true"])
        gone.wait()
        queue = self.store / "queue"
        queue.mkdir(parents=True)
        entry = {"repo": "/stale", "head": "abc1234", "command": "make gate", "enqueued_at": 1.0}
        (queue / "000000000004.1.json").write_text(
            json.dumps({**entry, "seq": 4, "pid": gone.pid, "start": "", "acquired_at": 1.0}),
            encoding="utf-8",
        )
        # A live pid that started at another time is a reused pid, not the caller.
        (queue / "000000000005.2.json").write_text(
            json.dumps(
                {
                    **entry,
                    "seq": 5,
                    "pid": os.getpid(),
                    "start": "Mon Jan  1 00:00:00 2001",
                    "acquired_at": None,
                }
            ),
            encoding="utf-8",
        )
        (queue / "000000000006.3.json").write_text("not json", encoding="utf-8")
        (self.store / "sequence").write_text("6\n", encoding="utf-8")

        process = self.slot(*self.holding("fresh"))
        self.wait_started("fresh")
        self.assertEqual([entry["seq"] for entry in self.entries()], [7])
        self.release("fresh").touch()
        self.assertEqual(self.finish(process)[0], 0)
        self.assertEqual(self.status(), "gate-slot: free\n")

    def test_interrupted_waiter_leaves_the_queue(self) -> None:
        holder = self.slot(*self.holding("holder"))
        self.wait_started("holder")
        for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            with self.subTest(signal=signum.name):
                waiter = self.slot(*self.holding("waiter"))
                self.wait_queued(2)
                self.wait_for(
                    lambda: len(self.status().splitlines()) == 2, "the waiter to be polling"
                )
                waiter.send_signal(signum)
                self.assertEqual(self.finish(waiter)[0], 128 + signum)
                self.assertEqual([entry["pid"] for entry in self.entries()], [holder.pid])
        self.release("holder").touch()
        self.assertEqual(self.finish(holder)[0], 0)
        self.assertEqual(self.seen(), ["holder start", "holder end"])

    def test_gate_under_the_holder_runs_without_queueing(self) -> None:
        inner_done = self.directory / "inner-done"
        release = self.release("outer")
        for label, prefix in (("marker", ""), ("ancestry", "env -u BEAMFALL_GATE_SLOT_HELD ")):
            with self.subTest(found_by=label):
                for path in (inner_done, release):
                    if path.exists():
                        path.unlink()
                outer = self.slot(
                    "sh",
                    "-c",
                    f'{prefix}"$1" "$2" -- touch "$3" || exit 9; '
                    'while [ ! -e "$4" ]; do sleep 0.05; done',
                    "sh",
                    sys.executable,
                    str(SLOT),
                    str(inner_done),
                    str(release),
                )
                self.wait_for(inner_done.exists, "the nested gate to run")
                # Still the outer holder's slot once the nested gate is done.
                refused = self.finish(self.slot("true", options=("--no-wait",)))
                self.assertEqual(refused[0], 69)
                self.assertEqual(len(self.entries()), 1)
                release.touch()
                status, errors = self.finish(outer)
                self.assertEqual(status, 0)
                self.assertIn("already under the slot held by", errors)

        # The marker alone is enough for a process that is not a descendant.
        holder = self.slot(*self.holding("holder"))
        self.wait_started("holder")
        marker = str(next((self.store / "queue").glob("*.json")))
        status, _errors = self.finish(self.slot("true", BEAMFALL_GATE_SLOT_HELD=marker))
        self.assertEqual(status, 0)
        self.release("holder").touch()
        self.assertEqual(self.finish(holder)[0], 0)

    def test_status_lists_the_holder_and_the_waiters(self) -> None:
        self.assertEqual(self.status(), "gate-slot: free\n")
        holder = self.slot(*self.holding("holder"))
        self.wait_started("holder")
        waiter = self.slot(*self.holding("waiter"))
        self.wait_queued(2)
        lines = self.status().splitlines()
        self.assertRegex(lines[0], f"^gate-slot: held by {self.repository} .* pid {holder.pid}, ")
        self.assertRegex(lines[1], f"^gate-slot: waiting 1: {self.repository} .* pid {waiter.pid}, ")
        self.assertEqual(len(lines), 2)
        self.release("holder").touch()
        self.release("waiter").touch()
        self.assertEqual((self.finish(holder)[0], self.finish(waiter)[0]), (0, 0))

    def test_usage_and_unstartable_command(self) -> None:
        for arguments in ([], ["--"], ["--bogus", "--", "true"], ["true"]):
            with self.subTest(arguments=arguments):
                process = self.start([sys.executable, str(SLOT), *arguments])
                self.assertEqual(self.finish(process)[0], 64)
        status, errors = self.finish(self.slot(str(self.directory / "no-such-command")))
        self.assertEqual(status, 127)
        self.assertIn("cannot run", errors)
        self.assertEqual(self.entries(), [])

    def test_unusable_store_runs_the_command(self) -> None:
        blocker = self.directory / "blocker"
        blocker.write_text("", encoding="utf-8")
        ran = self.directory / "ran"
        status, errors = self.finish(
            self.slot("touch", str(ran), BEAMFALL_GATE_SLOT_DIR=str(blocker / "slot"))
        )
        self.assertEqual(status, 0)
        self.assertIn("running without the machine slot", errors)
        self.assertTrue(ran.exists())


class CoordinatorSlotTest(SlotCase):
    """The gate coordinator (tools/gate/gate_latest.py) takes the same slot."""

    def test_full_gate_queues_behind_another_repositorys_gate(self) -> None:
        elsewhere = self.directory / "elsewhere"
        elsewhere.mkdir()
        holder = self.slot(*self.holding("other"), cwd=elsewhere)
        self.wait_started("other")
        gate = self.gate(*self.holding("gate"))
        self.wait_queued(2)
        time.sleep(1.0)
        self.assertEqual(self.seen(), ["other start"])
        self.release("other").touch()
        self.assertEqual(self.finish(holder)[0], 0)

        # Now the gate holds it, and another repository's gate is told so.
        self.wait_started("gate")
        status, errors = self.finish(self.slot("true", options=("--no-wait",), cwd=elsewhere))
        self.assertEqual(status, 69)
        self.assertRegex(errors, f"GATE_SLOT_BUSY held by {self.repository} \\(main @ {self.head}\\)")
        self.release("gate").touch()
        status, errors = self.finish(gate)
        self.assertEqual(status, 0, errors)
        self.assertRegex(
            errors,
            r"gate: run 1 waiting for the machine gate slot, queue position 1 of 1; "
            + f"held by {elsewhere} \\(no head\\) pid {holder.pid}, ",
        )
        self.assertIn("gate: run 1 took the machine gate slot after", errors)
        self.assertEqual(self.entries(), [])
        self.assertEqual(self.seen(), ["other start", "other end", "gate start", "gate end"])

    def test_superseded_or_cancelled_while_queued_leaves_the_queue(self) -> None:
        holder = self.slot(*self.holding("other"), cwd=self.directory)
        self.wait_started("other")
        older = self.gate(*self.holding("older"))
        self.wait_queued(2)
        newer = self.gate(*self.holding("newer"))
        status, errors = self.finish(older)
        self.assertEqual(status, 75, errors)
        self.assertIn("gate: run 1 superseded before start by run 2", errors)
        self.wait_queued(2)
        self.assertEqual([entry["pid"] for entry in self.entries()], [holder.pid, newer.pid])

        newer.send_signal(signal.SIGTERM)
        self.assertEqual(self.finish(newer)[0], 128 + signal.SIGTERM)
        self.assertEqual([entry["pid"] for entry in self.entries()], [holder.pid])
        self.release("other").touch()
        self.assertEqual(self.finish(holder)[0], 0)
        self.assertEqual(self.seen(), ["other start", "other end"])

    def test_preflight_names_the_holder_and_does_not_wait(self) -> None:
        holder = self.slot(*self.holding("other"), cwd=self.directory)
        self.wait_started("other")
        self.release("preflight").touch()
        status, errors = self.finish(self.gate(*self.holding("preflight"), GATE_PREFLIGHT="1"))
        self.assertEqual(status, 0, errors)
        self.assertEqual(errors.count("gate: the machine gate slot is held by"), 1)
        self.assertIn(f"pid {holder.pid}", errors)
        self.assertIn("this preflight does not wait for it", errors)
        self.assertEqual([entry["pid"] for entry in self.entries()], [holder.pid])
        self.assertEqual(self.seen(), ["other start", "preflight start", "preflight end"])
        self.release("other").touch()
        self.assertEqual(self.finish(holder)[0], 0)

        # With the slot free a preflight says nothing and still takes no place.
        status, errors = self.finish(self.gate(*self.holding("preflight"), GATE_PREFLIGHT="1"))
        self.assertEqual(status, 0, errors)
        self.assertNotIn("machine gate slot", errors)

    def test_gate_under_a_slot_holder_reenters(self) -> None:
        # `gate-slot.py -- <runner> make gate`, the runner passing on only an
        # allowlisted environment: the coordinator finds the holder above it.
        runner = self.repository / "tools/gate/gate_latest.py"
        self.release("gate").touch()
        outer = self.slot(
            "env",
            "-u",
            "BEAMFALL_GATE_SLOT_HELD",
            sys.executable,
            str(runner),
            "--",
            *self.holding("gate"),
            cwd=self.directory,
        )
        status, errors = self.finish(outer)
        self.assertEqual(status, 0, errors)
        self.assertNotIn("waiting for the machine gate slot", errors)
        self.assertEqual(self.seen(), ["gate start", "gate end"])

    def test_gate_that_inherited_capacity_does_not_wait_for_the_slot(self) -> None:
        # Capacity taken first, by a wrapper that did not take the slot: the
        # gate must not then queue for the slot with capacity in hand.
        holder = self.slot(*self.holding("other"), cwd=self.directory)
        self.wait_started("other")
        runner = self.repository / "tools/gate/gate_latest.py"
        self.release("gate").touch()
        gate = self.start(
            [sys.executable, str(CAPACITY), "--", sys.executable, str(runner), "--", *self.holding("gate")]
        )
        status, errors = self.finish(gate)
        self.assertEqual(status, 0, errors)
        self.assertIn(
            "gate: repository capacity was taken before the machine gate slot; "
            "running without waiting for the slot",
            errors,
        )
        self.assertEqual([entry["pid"] for entry in self.entries()], [holder.pid])
        self.assertEqual(self.seen(), ["other start", "gate start", "gate end"])
        self.release("other").touch()
        self.assertEqual(self.finish(holder)[0], 0)

    def test_gate_under_another_repositorys_capacity_does_not_wait_for_the_slot(self) -> None:
        # The capacity in hand need not be this repository's: a wrapper that
        # holds another repository's lock and starts this gate must not be
        # left waiting behind a gate that needs that lock.
        elsewhere = self.directory / "elsewhere"
        elsewhere.mkdir()
        subprocess.run(["git", "init", "-q", "-b", "main", str(elsewhere)], check=True)
        holder = self.slot(*self.holding("other"), cwd=self.directory)
        self.wait_started("other")
        runner = self.repository / "tools/gate/gate_latest.py"
        self.release("gate").touch()
        gate = self.start(
            [sys.executable, str(CAPACITY), "--", sys.executable, str(runner), "--", *self.holding("gate")],
            elsewhere,
        )
        status, errors = self.finish(gate)
        self.assertEqual(status, 0, errors)
        self.assertIn("running without waiting for the slot", errors)
        self.assertEqual([entry["pid"] for entry in self.entries()], [holder.pid])
        self.assertEqual(self.seen(), ["other start", "gate start", "gate end"])
        self.release("other").touch()
        self.assertEqual(self.finish(holder)[0], 0)

    def test_unusable_store_runs_the_gate(self) -> None:
        blocker = self.directory / "blocker"
        blocker.write_text("", encoding="utf-8")
        self.release("gate").touch()
        status, errors = self.finish(
            self.gate(*self.holding("gate"), BEAMFALL_GATE_SLOT_DIR=str(blocker / "slot"))
        )
        self.assertEqual(status, 0, errors)
        self.assertIn("running without the machine slot", errors)
        self.assertEqual(self.seen(), ["gate start", "gate end"])

    def test_checkout_without_the_slot_script_runs_the_gate(self) -> None:
        (self.repository / "script/gate-slot.py").unlink()
        holder = self.slot(*self.holding("other"), cwd=self.directory)
        self.wait_started("other")
        self.release("gate").touch()
        status, errors = self.finish(self.gate(*self.holding("gate")))
        self.assertEqual(status, 0, errors)
        self.assertNotIn("machine gate slot", errors)
        self.release("other").touch()
        self.assertEqual(self.finish(holder)[0], 0)


if __name__ == "__main__":
    unittest.main()
