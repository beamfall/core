#!/usr/bin/env python3
"""Prove direct Make interruption reaps cross-repo runner descendants."""

from __future__ import annotations

import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time
from typing import Callable


ROOT = Path(__file__).resolve().parent.parent
START_TIMEOUT_SECONDS = 10.0
CLEANUP_TIMEOUT_SECONDS = 10.0


def run(*command: str, cwd: Path) -> None:
    subprocess.run(command, cwd=cwd, check=True, stdout=subprocess.DEVNULL)


def commit_fixture(repo: Path) -> None:
    run("git", "init", "-q", cwd=repo)
    run("git", "add", ".", cwd=repo)
    run(
        "git",
        "-c",
        "commit.gpgsign=false",
        "-c",
        "user.name=Beamfall Test",
        "-c",
        "user.email=test@beamfall.invalid",
        "commit",
        "-qm",
        "fixture",
        cwd=repo,
    )


def revision(repo: Path) -> str:
    return subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=repo,
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()


def write_fixture_repos(case: Path) -> tuple[Path, Path]:
    producer = case / "producer-source"
    consumer = case / "consumer-source"
    producer.mkdir()
    consumer.mkdir()
    (producer / "README.md").write_text("fixture producer\n", encoding="utf-8")

    test_dir = consumer / "test"
    core_wire_dir = test_dir / "core-wire"
    script_dir = consumer / "script"
    vitest_dir = consumer / "vitest-fixture"
    core_wire_dir.mkdir(parents=True)
    script_dir.mkdir()
    vitest_dir.mkdir()
    (test_dir / "coreWireContract.test.ts").write_text(
        "// interruption fixture\n", encoding="utf-8"
    )
    (core_wire_dir / "client-core-wire.generated.json").write_text(
        '{"schemaVersion":1,"artifact":"internal/conformance/wire-contract-v1.0.0.json",'
        '"artifactVersion":"1.0.0"}\n',
        encoding="utf-8",
    )
    drift = script_dir / "wire-drift-red.sh"
    drift.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
    drift.chmod(0o755)
    (consumer / "package.json").write_text(
        '{"name":"consumer-fixture","version":"1.0.0","private":true,'
        '"devDependencies":{"vitest":"file:vitest-fixture"}}\n',
        encoding="utf-8",
    )
    (vitest_dir / "package.json").write_text(
        '{"name":"vitest","version":"0.0.0","bin":{"vitest":"vitest.py"}}\n',
        encoding="utf-8",
    )
    vitest = vitest_dir / "vitest.py"
    vitest.write_text(
        """#!/usr/bin/env python3
import os
from pathlib import Path
import signal
import time

for caught in (signal.SIGHUP, signal.SIGINT, signal.SIGTERM):
    signal.signal(caught, signal.SIG_IGN)
target = Path(os.environ["CROSSREPO_INTERRUPT_CHILD_PID_FILE"])
partial = target.with_suffix(".partial")
partial.write_text(f"{os.getpid()}\\n", encoding="utf-8")
os.replace(partial, target)
while True:
    time.sleep(1)
""",
        encoding="utf-8",
    )
    vitest.chmod(0o755)
    run(
        "npm",
        "install",
        "--package-lock-only",
        "--ignore-scripts",
        "--no-audit",
        "--no-fund",
        cwd=consumer,
    )
    commit_fixture(consumer)

    pin_registry = producer / "testing" / "release-gates" / "gate-commands.tsv"
    pin_registry.parent.mkdir(parents=True)
    pin_registry.write_text(
        "# repo\tsource\tgate\tcontract_bearing\tpin\n"
        f"beamfall-web\t../beamfall-web\tmake gate\tyes\t{revision(consumer)}\n",
        encoding="utf-8",
    )
    contract_registry = producer / "contracts" / "registry.json"
    contract_registry.parent.mkdir()
    contract_registry.write_text(
        '{"schemaVersion":1,"boundaries":[{"boundary":"client-core-wire",'
        '"consumers":[{"repo":"beamfall-web","pinnedVersion":"1.0.0"}]}]}\n',
        encoding="utf-8",
    )
    commit_fixture(producer)
    return producer, consumer


def process_table() -> dict[int, set[int]]:
    output = subprocess.run(
        ["ps", "-axo", "pid=,ppid="],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    children: dict[int, set[int]] = {}
    for row in output.splitlines():
        pid_text, parent_text = row.split()
        children.setdefault(int(parent_text), set()).add(int(pid_text))
    return children


def descendants(root: int) -> set[int]:
    table = process_table()
    found: set[int] = set()
    pending = [root]
    while pending:
        parent = pending.pop()
        for child in table.get(parent, set()):
            if child not in found:
                found.add(child)
                pending.append(child)
    return found


def process_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def group_alive(group: int) -> bool:
    try:
        os.killpg(group, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def wait_until(predicate: Callable[[], bool], timeout: float) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return True
        time.sleep(0.05)
    return predicate()


def terminate_group(group: int) -> None:
    if not group_alive(group):
        return
    try:
        os.killpg(group, signal.SIGTERM)
    except ProcessLookupError:
        return
    if wait_until(lambda: not group_alive(group), 1.0):
        return
    try:
        os.killpg(group, signal.SIGKILL)
    except ProcessLookupError:
        return
    wait_until(lambda: not group_alive(group), 2.0)


def log_tail(path: Path) -> str:
    if not path.exists():
        return ""
    return "\n".join(path.read_text(encoding="utf-8", errors="replace").splitlines()[-80:])


def run_interruption_case(target: str, temp_pattern: str) -> None:
    case = Path(tempfile.mkdtemp(prefix=f"beamfall-{target}-interrupt-test."))
    runner_tmp = case / "runner-tmp"
    child_pid_file = case / "child.pid"
    output = case / "make.log"
    runner_tmp.mkdir()
    producer, consumer = write_fixture_repos(case)
    env = os.environ.copy()
    for name in ("MAKEFLAGS", "MFLAGS", "MAKELEVEL"):
        env.pop(name, None)
    env.update(
        {
            "BEAMFALL_CROSSREPO_FIXTURE_SUPERVISED": "1",
            "BEAMFALL_CROSSREPO_PRODUCER_SOURCE": str(producer),
            "BEAMFALL_CROSSREPO_CONSUMER_SOURCE": str(consumer),
            "CROSSREPO_INTERRUPT_CHILD_PID_FILE": str(child_pid_file),
            "TMPDIR": str(runner_tmp),
        }
    )

    make_process: subprocess.Popen[bytes] | None = None
    child_pid = 0
    try:
        with output.open("wb") as log:
            make_process = subprocess.Popen(
                ["make", target],
                cwd=ROOT,
                env=env,
                stdout=log,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
            started = wait_until(
                lambda: child_pid_file.is_file()
                and bool(list(runner_tmp.glob(temp_pattern))),
                START_TIMEOUT_SECONDS,
            )
            if not started:
                raise AssertionError(f"{target} stub did not start\n" + log_tail(output))
            child_pid = int(child_pid_file.read_text(encoding="utf-8").strip())
            live_descendants = descendants(make_process.pid)
            if child_pid not in live_descendants:
                raise AssertionError("stub is not a descendant of the Make target")

            os.killpg(make_process.pid, signal.SIGINT)
            status = make_process.wait(timeout=CLEANUP_TIMEOUT_SECONDS)
            if status == 0:
                raise AssertionError("interrupted Make target exited zero")

            processes_gone = wait_until(
                lambda: all(not process_alive(pid) for pid in live_descendants),
                CLEANUP_TIMEOUT_SECONDS,
            )
            if not processes_gone:
                survivors = sorted(pid for pid in live_descendants if process_alive(pid))
                raise AssertionError(f"{target} descendants survived interruption: {survivors}")
            if not wait_until(lambda: not group_alive(make_process.pid), 2.0):
                raise AssertionError("Make process group survived interruption")
            temp_gone = wait_until(
                lambda: not any(runner_tmp.iterdir()),
                CLEANUP_TIMEOUT_SECONDS,
            )
            if not temp_gone:
                raise AssertionError(f"{target} temporary directory survived interruption")
    except (AssertionError, subprocess.TimeoutExpired) as error:
        raise SystemExit(f"crossrepo interruption regression failed: {error}\n{log_tail(output)}")
    finally:
        if make_process is not None:
            terminate_group(make_process.pid)
        if child_pid and process_alive(child_pid):
            os.kill(child_pid, signal.SIGKILL)
            wait_until(lambda: not process_alive(child_pid), 2.0)
        shutil.rmtree(case, ignore_errors=True)


def main() -> int:
    run_interruption_case(
        "crossrepo-conformance-fixture", "beamfall-crossrepo-conformance.*"
    )
    run_interruption_case(
        "crossrepo-conformance-prove-red",
        "beamfall-crossrepo-conformance-test.*",
    )
    print(
        "crossrepo-conformance_interrupt_test: OK "
        "(runner and harness SIGINT reaped descendants and temp trees)"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
