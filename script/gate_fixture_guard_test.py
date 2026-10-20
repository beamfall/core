#!/usr/bin/env python3
from __future__ import annotations

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "script"))
import importlib.util

SPEC = importlib.util.spec_from_file_location("gate_fixture_guard", ROOT / "script/gate-fixture-guard.py")
assert SPEC and SPEC.loader
GUARD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GUARD)


class FixtureGuardTest(unittest.TestCase):
    def test_every_writer_is_rejected_and_runtime_update_url_is_allowed(self) -> None:
        for writer in GUARD.WRITER_ENV:
            env = os.environ.copy()
            env[writer] = "1"
            result = subprocess.run(
                [sys.executable, str(ROOT / "script/gate-fixture-guard.py"), "--", "true"],
                cwd=ROOT,
                env=env,
                text=True,
                capture_output=True,
            )
            self.assertEqual(result.returncode, 2, writer)
            self.assertIn(writer, result.stderr)
        env = os.environ.copy()
        env["BEAMFALL_UPDATE_CHECK_BASE_URL"] = "http://127.0.0.1.invalid"
        result = subprocess.run(
            [sys.executable, str(ROOT / "script/gate-fixture-guard.py"), "--", "true"],
            cwd=ROOT,
            env=env,
        )
        self.assertEqual(result.returncode, 0)

    def test_tree_digest_detects_content_and_names(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            scope = root / "fixtures"
            scope.mkdir()
            (scope / "a").write_text("a", encoding="utf-8")
            first = GUARD.tree_digest(root, "fixtures")
            (scope / "a").write_text("b", encoding="utf-8")
            self.assertNotEqual(first, GUARD.tree_digest(root, "fixtures"))
            (scope / "a").rename(scope / "b")
            self.assertNotEqual(first, GUARD.tree_digest(root, "fixtures"))

    def test_snapshot_precedes_command_spawn(self) -> None:
        events: list[str] = []
        child = mock.Mock(pid=123, poll=mock.Mock(return_value=0), wait=mock.Mock(return_value=0))
        with mock.patch.object(
            GUARD,
            "snapshot",
            side_effect=lambda _root, scopes: events.append("snapshot")
            or {scope: "stable" for scope in scopes},
        ), mock.patch.object(
            GUARD.subprocess,
            "Popen",
            side_effect=lambda *_args, **_kwargs: events.append("spawn") or child,
        ), mock.patch.object(
            GUARD,
            "cleanup_group",
            return_value=(False, True),
        ), mock.patch.object(sys, "argv", ["guard", "--", "true"]):
            self.assertEqual(GUARD.main(), 0)
        self.assertEqual(events, ["snapshot", "spawn", "snapshot"])

    def test_shell_grandchild_is_reaped_before_final_snapshot(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "fixtures").mkdir()
            command = [
                "/bin/sh",
                "-c",
                'trap "" HUP; (trap "" HUP; sleep 0.3; printf late > fixtures/late) & exit 0',
            ]
            status = GUARD.run_guarded(root, ("fixtures",), command, os.environ.copy())
            self.assertEqual(status, 1)
            self.assertFalse((root / "fixtures/late").exists())
            import time

            time.sleep(0.4)
            self.assertFalse((root / "fixtures/late").exists())


if __name__ == "__main__":
    unittest.main()
