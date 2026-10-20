#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from typing import Optional


ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("mutation_policy", ROOT / "script/mutation-policy.py")
assert SPEC and SPEC.loader
POLICY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(POLICY)


def package_budget(cap: int = 0) -> dict[str, int]:
    return {"max_surviving": cap, "measured_killed": 1, "measured_lived": 0, "measured_not_covered": 0}


class MutationPolicyTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.repo = Path(self.temp.name)
        subprocess.run(["git", "init", "-q", "-b", "main", str(self.repo)], check=True)
        subprocess.run(["git", "-C", str(self.repo), "config", "user.name", "test"], check=True)
        subprocess.run(["git", "-C", str(self.repo), "config", "user.email", "test@example.invalid"], check=True)

    def tearDown(self) -> None:
        self.temp.cleanup()

    def write(self, packages: dict[str, dict[str, int]], migrations: Optional[list[dict[str, str]]] = None) -> dict:
        policy = {
            "tool": "gremlins",
            "tool_version": "0.6.0",
            "migrations": migrations or [],
            "source_exclusions": {package: [] for package in packages},
            "packages": packages,
        }
        path = self.repo / POLICY.POLICY
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(policy), encoding="utf-8")
        for package in packages:
            package_dir = self.repo / package[2:]
            package_dir.mkdir(parents=True, exist_ok=True)
            source = package_dir / "source.go"
            if not source.exists():
                source.write_text("package fixture\n", encoding="utf-8")
        return policy

    def commit(self, message: str) -> None:
        subprocess.run(["git", "-C", str(self.repo), "add", "-A"], check=True)
        subprocess.run(["git", "-C", str(self.repo), "commit", "-qm", message], check=True)

    def test_ratchet_reintroduction_and_rename(self) -> None:
        initial = {"./internal/anchor": package_budget(), "./internal/item": package_budget(3)}
        self.write(initial)
        self.commit("initial")
        lowered = self.write({"./internal/anchor": package_budget(), "./internal/item": package_budget(2)})
        POLICY.validate_history(self.repo, lowered)
        raised = self.write({"./internal/anchor": package_budget(), "./internal/item": package_budget(4)})
        with self.assertRaisesRegex(POLICY.PolicyError, "ancestral minimum"):
            POLICY.validate_history(self.repo, raised)

        self.write(
            {"./internal/anchor": package_budget()},
            [{"kind": "remove", "package": "./internal/item"}],
        )
        self.commit("remove item")
        reintroduced = self.write({"./internal/anchor": package_budget(), "./internal/item": package_budget(2)})
        with self.assertRaisesRegex(POLICY.PolicyError, "reappeared"):
            POLICY.validate_history(self.repo, reintroduced)
        reintroduced["migrations"] = [{"kind": "reintroduce", "package": "./internal/item"}]
        (self.repo / POLICY.POLICY).write_text(json.dumps(reintroduced), encoding="utf-8")
        POLICY.validate_history(self.repo, reintroduced)

    def test_silent_package_removal_requires_migration(self) -> None:
        self.write({"./internal/anchor": package_budget(), "./internal/item": package_budget()})
        self.commit("initial")
        removed = self.write({"./internal/anchor": package_budget()})
        with self.assertRaisesRegex(POLICY.PolicyError, "removed without an explicit migration"):
            POLICY.validate_history(self.repo, removed)
        removed["migrations"] = [{"kind": "remove", "package": "./internal/item"}]
        (self.repo / POLICY.POLICY).write_text(json.dumps(removed), encoding="utf-8")
        POLICY.validate_history(self.repo, removed)

    def test_dirty_rename_requires_migration(self) -> None:
        self.write({"./internal/anchor": package_budget(), "./internal/old": package_budget()})
        self.commit("initial")
        (self.repo / "internal/old").rename(self.repo / "internal/new")
        renamed = self.write({"./internal/anchor": package_budget(), "./internal/new": package_budget()})
        with self.assertRaisesRegex(POLICY.PolicyError, "renamed without a migration"):
            POLICY.validate_history(self.repo, renamed)
        renamed["migrations"] = [{"kind": "rename", "from": "./internal/old", "to": "./internal/new"}]
        (self.repo / POLICY.POLICY).write_text(json.dumps(renamed), encoding="utf-8")
        POLICY.validate_history(self.repo, renamed)

    def test_source_exclusion_and_shallow_checkout(self) -> None:
        current = self.write({"./internal/item": package_budget()})
        tagged = self.repo / "internal/item/tagged.go"
        tagged.write_text("//go:build special\n\npackage fixture\n", encoding="utf-8")
        with self.assertRaisesRegex(POLICY.PolicyError, "explicit build-tag exclusion"):
            POLICY.source_inventory(self.repo, current)
        current["source_exclusions"]["./internal/item"] = [
            {"path": "tagged.go", "kind": "build-tag", "reason": "not in default mutation build"}
        ]
        POLICY.source_inventory(self.repo, current)
        (self.repo / POLICY.POLICY).write_text(json.dumps(current), encoding="utf-8")
        self.commit("initial")
        shallow_root = Path(self.temp.name + "-shallow")
        self.addCleanup(shutil.rmtree, shallow_root, True)
        subprocess.run(["git", "clone", "-q", "--depth", "1", "file://" + str(self.repo), str(shallow_root)], check=True)
        with self.assertRaisesRegex(POLICY.PolicyError, "non-shallow"):
            POLICY.validate_history(shallow_root, current)

    def test_workflow_callers_fetch_full_history(self) -> None:
        for relative, marker in (
            (".github/workflows/gate.yml", "run: make gate"),
            (".github/workflows/release-gate.yml", "run: make release-test-gates"),
            (".github/workflows/clean-checkout-canary.yml", "run: script/clean-checkout-canary.sh --repo beamfall"),
        ):
            text = (ROOT / relative).read_text(encoding="utf-8")
            position = text.index(marker)
            checkout = text.rfind("uses: actions/checkout@", 0, position)
            setup = text.find("uses: actions/setup-", checkout, position)
            block = text[checkout : setup if setup >= 0 else position]
            self.assertIn("fetch-depth: 0", block, relative)


if __name__ == "__main__":
    unittest.main()
