#!/usr/bin/env python3
"""Self-tests for native_evidence_check.py.

Covers the --allow-missing-repos guard specifically: a missing catalog repo dir
must return 0 with the flag set and non-zero without it.
"""

from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("native_evidence_check.py")
SPEC = importlib.util.spec_from_file_location("native_evidence_check", MODULE_PATH)
native_evidence_check = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
sys.modules[SPEC.name] = native_evidence_check
SPEC.loader.exec_module(native_evidence_check)


def write_json(path: Path, data: object) -> None:
    path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")


class AllowMissingReposTests(unittest.TestCase):
    """Tests for --allow-missing-repos flag behaviour."""

    def test_missing_repo_without_flag_returns_nonzero(self) -> None:
        """Absent catalog directory → error without --allow-missing-repos."""
        result = native_evidence_check.main([
            "--apple-catalog", "/nonexistent/apple-catalog",
            "--android-catalog", "/nonexistent/android-catalog",
        ])
        self.assertNotEqual(result, 0, "Expected non-zero exit for missing repos without flag")

    def test_missing_repo_with_flag_returns_zero(self) -> None:
        """Absent catalog directory → 0 (blocker only) with --allow-missing-repos."""
        result = native_evidence_check.main([
            "--apple-catalog", "/nonexistent/apple-catalog",
            "--android-catalog", "/nonexistent/android-catalog",
            "--allow-missing-repos",
        ])
        self.assertEqual(result, 0, "Expected 0 exit for missing repos with --allow-missing-repos")

    def test_present_repo_with_missing_manifest_errors(self) -> None:
        """Present dir but no manifest.json → error even with --allow-missing-repos."""
        with tempfile.TemporaryDirectory() as tmp:
            apple = Path(tmp) / "apple"
            android = Path(tmp) / "android"
            apple.mkdir()
            android.mkdir()
            result = native_evidence_check.main([
                "--apple-catalog", str(apple),
                "--android-catalog", str(android),
                "--allow-missing-repos",
            ])
            self.assertNotEqual(result, 0, "Expected error when repo present but manifest missing")

    def test_partial_missing_one_repo_allowed(self) -> None:
        """One repo missing, one present with errors: error because present repo has gaps."""
        with tempfile.TemporaryDirectory() as tmp:
            android = Path(tmp) / "android"
            android.mkdir()
            # Android dir present but empty → validate_catalog returns errors
            result = native_evidence_check.main([
                "--apple-catalog", "/nonexistent/apple-catalog",
                "--android-catalog", str(android),
                "--allow-missing-repos",
            ])
            # Apple is skipped (missing + flag), Android has no manifest → error
            self.assertNotEqual(result, 0, "Expected error when present repo has missing manifest")

    def test_valid_catalog_passes(self) -> None:
        """Minimal valid catalog with one captured row per required surface → passes."""
        with tempfile.TemporaryDirectory() as tmp:
            apple = Path(tmp) / "apple"
            apple.mkdir()
            images = apple / "images"
            baselines = apple / "baselines"
            images.mkdir()
            baselines.mkdir()
            android = Path(tmp) / "android"
            android.mkdir()
            android_images = android / "images"
            android_baselines = android / "baselines"
            android_images.mkdir()
            android_baselines.mkdir()

            def make_entry(surface: str, idx: int) -> dict:
                entry_id = f"{surface}.test.{idx}"
                (images / f"{entry_id}.png").write_bytes(b"\x89PNG")
                (images / f"{entry_id}.txt").write_text("hierarchy\n", encoding="utf-8")
                (baselines / f"{entry_id}.png").write_bytes(b"\x89PNG")
                return {
                    "id": entry_id,
                    "surface": surface,
                    "status": "captured",
                    "screenshot": f"images/{entry_id}.png",
                    "hierarchy": f"images/{entry_id}.txt",
                    "baseline": f"baselines/{entry_id}.png",
                }

            def make_android_entry(surface: str, idx: int) -> dict:
                entry_id = f"{surface}.test.{idx}"
                (android_images / f"{entry_id}.png").write_bytes(b"\x89PNG")
                (android_images / f"{entry_id}.txt").write_text("hierarchy\n", encoding="utf-8")
                (android_baselines / f"{entry_id}.png").write_bytes(b"\x89PNG")
                return {
                    "id": entry_id,
                    "surface": surface,
                    "status": "captured",
                    "screenshot": f"images/{entry_id}.png",
                    "hierarchy": f"images/{entry_id}.txt",
                    "baseline": f"baselines/{entry_id}.png",
                }

            apple_entries = [make_entry(s, i) for i, s in enumerate(("iphone", "ipad", "tvos", "macos"))]
            write_json(apple / "manifest.json", {"entries": apple_entries})

            android_entries = [make_android_entry(s, i) for i, s in enumerate(("androidtv", "androidmobile"))]
            write_json(android / "manifest.json", {"entries": android_entries})

            result = native_evidence_check.main([
                "--apple-catalog", str(apple),
                "--android-catalog", str(android),
            ])
            self.assertEqual(result, 0, "Expected 0 for valid minimal catalog")


if __name__ == "__main__":
    unittest.main()
