#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("mutation_runner", ROOT / "script/mutation-runner.py")
assert SPEC and SPEC.loader
RUNNER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RUNNER)


FAKE = r'''#!/usr/bin/env python3
import json, os, subprocess, sys, time
if sys.argv[1:] == ["--version"]:
    print("gremlins version v" + os.environ.get("FAKE_VERSION", "0.6.0"))
    raise SystemExit(0)
mode = os.environ.get("FAKE_MODE", "complete")
package = sys.argv[-1]
output = next(arg.split("=", 1)[1] for arg in sys.argv if arg.startswith("--output="))
if mode == "nonzero":
    raise SystemExit(9)
if mode == "malformed":
    open(output, "w").write("{not-json")
    raise SystemExit(0)
if mode in ("timeout-first", "timeboxed") and (mode == "timeout-first" or package.endswith("/webhook")):
    child = subprocess.Popen(["sleep", "30"])
    if os.environ.get("FAKE_CHILD_PID"):
        open(os.environ["FAKE_CHILD_PID"], "w").write(str(child.pid))
    time.sleep(30)
policy = json.load(open(os.environ["FAKE_POLICY"]))["packages"][package]
payload = {
    "mutants_killed": policy["measured_killed"],
    "mutants_lived": policy["measured_lived"],
    "mutants_not_covered": policy["measured_not_covered"],
}
if mode == "counter-drift":
    payload["mutants_not_covered"] += 1
open(output, "w").write(json.dumps(payload))
if mode == "threshold-exit":
    raise SystemExit(1)
'''


class MutationRunnerTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.directory = Path(self.temp.name)
        binary = self.directory / "bin"
        binary.mkdir()
        self.fake = binary / "gremlins"
        self.fake.write_text(FAKE, encoding="utf-8")
        self.fake.chmod(0o755)
        self.evidence = self.directory / "evidence"
        self.env = os.environ.copy()
        self.env.update(
            {
                "PATH": str(binary) + os.pathsep + self.env["PATH"],
                "FAKE_POLICY": str(ROOT / "docs/testing/mutation-budget.json"),
                "MUTATION_EVIDENCE_DIR": str(self.evidence),
                "MUTATION_PKG_CAP_SEC": "1",
                "MUTATION_RUN_CAP_SEC": "5",
            }
        )

    def tearDown(self) -> None:
        self.temp.cleanup()

    def invoke(self, mode: str = "named", fake_mode: str = "complete") -> subprocess.CompletedProcess[str]:
        env = self.env.copy()
        env["FAKE_MODE"] = fake_mode
        return subprocess.run(
            [str(ROOT / "script/mutation-test.sh"), mode],
            cwd=ROOT,
            env=env,
            text=True,
            capture_output=True,
        )

    def status(self) -> dict:
        return json.loads((self.evidence / "status.json").read_text(encoding="utf-8"))

    def test_complete_version_and_counter_failures(self) -> None:
        complete = self.invoke("streamtoken")
        self.assertEqual(complete.returncode, 0, complete.stderr)
        self.assertEqual(self.status()["status"], "COMPLETE")

        self.env["FAKE_VERSION"] = "0.5.0"
        mismatch = self.invoke("streamtoken")
        self.assertNotEqual(mismatch.returncode, 0)
        self.assertIn("version mismatch", mismatch.stderr)
        self.env.pop("FAKE_VERSION")
        for fake_mode, marker in (("counter-drift", "does not match"), ("malformed", "malformed"), ("nonzero", "exit 9")):
            failed = self.invoke("streamtoken", fake_mode)
            self.assertNotEqual(failed.returncode, 0)
            self.assertIn(marker, failed.stderr)
        threshold = self.invoke("streamtoken", "threshold-exit")
        self.assertEqual(threshold.returncode, 0, threshold.stderr)
        self.assertEqual(self.status()["status"], "COMPLETE")

    def test_timeout_semantics_and_descendant_cleanup(self) -> None:
        self.env["MUTATION_PKG_CAP_SEC"] = "0.15"
        pid_path = self.directory / "child.pid"
        self.env["FAKE_CHILD_PID"] = str(pid_path)
        first = self.invoke("streamtoken", "timeout-first")
        self.assertNotEqual(first.returncode, 0)
        self.assertEqual(self.status()["status"], "FAILED")

        timed = self.invoke("named", "timeboxed")
        self.assertEqual(timed.returncode, 0, timed.stderr)
        payload = self.status()
        self.assertEqual(payload["status"], "TIMEBOXED")
        self.assertEqual([row["package"] for row in payload["completed"]], ["./internal/streamtoken"])
        self.assertEqual(payload["interrupted_package"], "./internal/webhook")
        self.assertEqual(payload["remaining_packages"], ["./internal/webhook", "./internal/offlinepack"])
        child_pid = int(pid_path.read_text())
        for _ in range(20):
            try:
                os.kill(child_pid, 0)
            except ProcessLookupError:
                break
            time.sleep(0.025)
        else:
            self.fail(f"timed-out Gremlins descendant survived: {child_pid}")

    def test_between_package_timeout_has_exact_remaining_ledger(self) -> None:
        self.env["MUTATION_RUN_CAP_SEC"] = "0.5"
        self.env["MUTATION_TEST_INTER_PACKAGE_DELAY_SEC"] = "0.6"
        result = self.invoke("named")
        self.assertEqual(result.returncode, 0, result.stderr)
        payload = self.status()
        self.assertEqual(payload["status"], "TIMEBOXED")
        self.assertEqual(payload["interrupted_package"], "./internal/webhook")
        self.assertEqual(payload["remaining_packages"], ["./internal/webhook", "./internal/offlinepack"])

    def test_exclusion_flags_and_workflow_routing(self) -> None:
        command = RUNNER.gremlins_command(
            Path("report.json"),
            "./internal/example",
            [{"path": "generated.go", "kind": "source", "reason": "fixture"}],
        )
        self.assertIn(r"--exclude-files=^generated\.go$", command)
        workflow = (ROOT / ".github/workflows/mutation-nightly.yml").read_text(encoding="utf-8")
        self.assertIn("steps.gremlins-version.outputs.version", workflow)
        self.assertIn("steps.mutation.outputs.status == 'COMPLETE'", workflow)
        self.assertIn("steps.mutation.outputs.status == 'TIMEBOXED'", workflow)
        self.assertIn("mutation-nightly-timeboxed-residual", workflow)


if __name__ == "__main__":
    unittest.main()
