#!/usr/bin/env python3
"""Self-tests for apple_a11y_check.py."""

from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("apple_a11y_check.py")
SPEC = importlib.util.spec_from_file_location("apple_a11y_check", MODULE_PATH)
apple_a11y_check = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
sys.modules[SPEC.name] = apple_a11y_check
SPEC.loader.exec_module(apple_a11y_check)


def write_json(path: Path, data: object) -> None:
    path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")


class AppleA11yCheckTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        (self.root / "images").mkdir()
        (self.root / "baselines").mkdir()
        self.manifest_path = self.root / "manifest.json"
        self.evidence_path = self.root / "voiceover-evidence.json"

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def valid_manifest_and_evidence(self) -> tuple[list[dict[str, object]], list[dict[str, object]]]:
        manifest: list[dict[str, object]] = []
        evidence: list[dict[str, object]] = []
        for requirement in apple_a11y_check.APPLE7_REQUIREMENTS:
            entry_id = ".".join(requirement.key())
            hierarchy = f"images/{entry_id}.txt"
            (self.root / hierarchy).write_text("hierarchy\n", encoding="utf-8")
            entry: dict[str, object] = {
                "id": entry_id,
                "surface": requirement.surface,
                "screen": requirement.screen,
                "state": requirement.state,
                "a11y": requirement.a11y,
                "hierarchy": hierarchy,
                "status": "captured",
            }
            if requirement.is_visual:
                screenshot = f"images/{entry_id}.png"
                baseline = f"baselines/{entry_id}.png"
                (self.root / screenshot).write_bytes(b"png")
                (self.root / baseline).write_bytes(b"png")
                entry["screenshot"] = screenshot
                entry["baseline"] = baseline
            else:
                for assertion_id in requirement.assertion_ids:
                    evidence.append(
                        {
                            "surface": requirement.surface,
                            "screen": requirement.screen,
                            "state": requirement.state,
                            "a11y": requirement.a11y,
                            "assertionId": assertion_id,
                            "result": "pass",
                        }
                    )
            manifest.append(entry)
        return manifest, evidence

    def assert_errors_contain(self, errors: list[str], needle: str) -> None:
        self.assertTrue(errors, "expected validation errors")
        self.assertIn(needle, "\n".join(errors))

    def validate(self, manifest: list[dict[str, object]], evidence: list[dict[str, object]]) -> list[str]:
        write_json(self.manifest_path, manifest)
        write_json(self.evidence_path, evidence)
        return apple_a11y_check.validate(
            manifest_path=self.manifest_path,
            repo_root=self.root,
            voiceover_evidence_path=self.evidence_path,
        )

    def test_missing_surface_tuple_fails(self) -> None:
        manifest, evidence = self.valid_manifest_and_evidence()
        missing = next(r for r in apple_a11y_check.APPLE7_REQUIREMENTS if r.surface == "iphone" and r.screen == "browse")
        manifest = [
            row
            for row in manifest
            if (row["surface"], row["screen"], row["state"], row["a11y"]) != missing.key()
        ]
        errors = self.validate(manifest, evidence)
        self.assert_errors_contain(errors, f"missing required APPLE-7 tuple {missing.display_key()}")

    def test_listed_only_visual_fails(self) -> None:
        manifest, evidence = self.valid_manifest_and_evidence()
        visual = next(r for r in apple_a11y_check.APPLE7_REQUIREMENTS if r.is_visual)
        for row in manifest:
            if (row["surface"], row["screen"], row["state"], row["a11y"]) == visual.key():
                row["status"] = "listed"
        errors = self.validate(manifest, evidence)
        self.assert_errors_contain(errors, f"{visual.display_key()} must have status captured")

    def test_missing_visual_artifact_fails(self) -> None:
        manifest, evidence = self.valid_manifest_and_evidence()
        visual = next(r for r in apple_a11y_check.APPLE7_REQUIREMENTS if r.is_visual)
        for row in manifest:
            if (row["surface"], row["screen"], row["state"], row["a11y"]) == visual.key():
                (self.root / str(row["baseline"])).unlink()
        errors = self.validate(manifest, evidence)
        self.assert_errors_contain(errors, f"{visual.display_key()} baseline missing")

    def test_missing_voiceover_evidence_fails(self) -> None:
        manifest, evidence = self.valid_manifest_and_evidence()
        voiceover = next(r for r in apple_a11y_check.APPLE7_REQUIREMENTS if not r.is_visual)
        evidence = [
            row
            for row in evidence
            if not (
                row["surface"] == voiceover.surface
                and row["screen"] == voiceover.screen
                and row["state"] == voiceover.state
                and row["a11y"] == voiceover.a11y
                and row["assertionId"] == voiceover.assertion_ids[0]
            )
        ]
        errors = self.validate(manifest, evidence)
        self.assert_errors_contain(errors, f"{voiceover.display_key()} missing VoiceOver assertion")

    def test_unknown_a11y_fails(self) -> None:
        manifest, evidence = self.valid_manifest_and_evidence()
        manifest.append(
            {
                "id": "ios.browse.locked.magic",
                "surface": "ios",
                "screen": "browse",
                "state": "locked",
                "a11y": "magic",
                "hierarchy": "images/ios.browse.locked.magic.txt",
                "status": "captured",
            }
        )
        errors = self.validate(manifest, evidence)
        self.assert_errors_contain(errors, "unknown a11y value")

    def test_text_expansion_is_a_known_catalog_variant(self) -> None:
        manifest, evidence = self.valid_manifest_and_evidence()
        manifest.append(
            {
                "id": "iphone.browse.populated.text-expansion",
                "surface": "iphone",
                "screen": "browse",
                "state": "populated",
                "a11y": "text-expansion",
                "hierarchy": "images/iphone.browse.populated.text-expansion.txt",
                "status": "captured",
            }
        )
        self.assertEqual([], self.validate(manifest, evidence))

    def test_valid_coverage_passes(self) -> None:
        manifest, evidence = self.valid_manifest_and_evidence()
        self.assertEqual([], self.validate(manifest, evidence))


class AllowMissingReposTests(unittest.TestCase):
    """Tests for --allow-missing-repos flag in apple_a11y_check.main()."""

    def test_missing_repo_without_flag_raises_or_errors(self) -> None:
        """Absent repo root without --allow-missing-repos → FileNotFoundError or non-zero."""
        # The original behaviour: validate() raises FileNotFoundError when manifest is absent.
        # The test documents that the caller MUST NOT get a 0 exit — either an exception
        # propagates or the return code is non-zero. Both are acceptable pre-flag behaviours.
        try:
            result = apple_a11y_check.main([
                "--manifest", "/nonexistent/manifest.json",
                "--repo-root", "/nonexistent/repo",
                "--voiceover-evidence", "/nonexistent/voiceover.json",
            ])
            self.assertNotEqual(result, 0, "Expected non-zero exit for missing repo without flag")
        except (FileNotFoundError, OSError):
            pass  # Expected: original behaviour raises before returning

    def test_missing_repo_with_flag_returns_zero(self) -> None:
        """Absent repo root → 0 (blocker only) with --allow-missing-repos."""
        result = apple_a11y_check.main([
            "--allow-missing-repos",
            "--repo-root", "/nonexistent/repo",
            "--manifest", "/nonexistent/manifest.json",
            "--voiceover-evidence", "/nonexistent/voiceover.json",
        ])
        self.assertEqual(result, 0, "Expected 0 exit for missing repo with --allow-missing-repos")

    def test_missing_repo_flag_no_args_returns_zero(self) -> None:
        """--allow-missing-repos with no paths at all → 0 (skip path)."""
        result = apple_a11y_check.main(["--allow-missing-repos"])
        self.assertEqual(result, 0, "Expected 0 when flag set and no paths given")

    def test_present_repo_with_valid_paths_still_validates(self) -> None:
        """--allow-missing-repos does not suppress validation when repo IS present."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "images").mkdir()
            (root / "baselines").mkdir()
            # Deliberately omit all required APPLE7 rows — should still error.
            write_json(root / "manifest.json", [])
            write_json(root / "voiceover-evidence.json", [])
            result = apple_a11y_check.main([
                "--allow-missing-repos",
                "--repo-root", str(root),
                "--manifest", str(root / "manifest.json"),
                "--voiceover-evidence", str(root / "voiceover-evidence.json"),
            ])
            self.assertNotEqual(result, 0, "Expected error when repo present but APPLE-7 rows missing")


if __name__ == "__main__":
    unittest.main(verbosity=2)
