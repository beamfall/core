#!/usr/bin/env python3
# scenario:agent-test-selection
"""Selftest for .github/cishards/test-commands.json.

`corvint test` maps changed paths to focused verification with the commands this
file declares. A command that names a missing make target or npm script would
turn every narrowed run for that language into a silent failure, so this test
checks each declared command against the repo.
"""

import json
import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
COMMANDS = ROOT / ".github" / "cishards" / "test-commands.json"


def make_targets():
    text = (ROOT / "Makefile").read_text(encoding="utf-8")
    return set(re.findall(r"^([A-Za-z0-9_.-]+):", text, re.MULTILINE))


class TestCommandsTest(unittest.TestCase):
    def setUp(self):
        self.doc = json.loads(COMMANDS.read_text(encoding="utf-8"))

    def test_profile(self):
        self.assertEqual(self.doc["profile"], "corvint-test-commands/0")

    def test_go_flags_disable_cache(self):
        # A cached PASS is not a fresh verdict for the changed packages.
        self.assertIn("-count=1", self.doc["go"]["flags"])

    def test_declared_commands_resolve(self):
        targets = make_targets()
        for language, argv in self.doc["languages"].items():
            with self.subTest(language=language):
                if isinstance(argv, dict):
                    self.assertTrue(argv.get("none"), "a skipped language needs a reason")
                    continue
                self.assertIsInstance(argv, list)
                if argv[0] == "make":
                    self.assertIn(argv[1], targets)
                elif argv[:2] == ["npm", "--prefix"]:
                    pkg = json.loads((ROOT / argv[2] / "package.json").read_text(encoding="utf-8"))
                    self.assertIn(argv[3], pkg.get("scripts", {}))
                else:
                    self.fail(f"unrecognised command {argv!r}")


if __name__ == "__main__":
    unittest.main()
