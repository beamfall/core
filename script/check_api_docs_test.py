#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
from pathlib import Path
import shutil
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("check_api_docs", ROOT / "script/check-api-docs.py")
assert SPEC and SPEC.loader
CHECK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK)


class APIDocsTest(unittest.TestCase):
    def fixture(self, directory: str) -> Path:
        root = Path(directory)
        (root / "script").mkdir()
        (root / "docs/api").mkdir(parents=True)
        for source in ("gen-api-docs.sh", "openapi_to_md.py"):
            shutil.copy2(ROOT / "script" / source, root / "script" / source)
        for source in ("openapi.json", "home-automation.md", *CHECK.OUTPUTS):
            shutil.copy2(ROOT / "docs/api" / source, root / "docs/api" / source)
        return root

    def test_fresh_drift_and_missing(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = self.fixture(directory)
            self.assertEqual(CHECK.check(root), [])
            (root / "docs/api/reference.md").write_text("drift\n", encoding="utf-8")
            self.assertIn("generated API doc drift: docs/api/reference.md", CHECK.check(root))
            (root / "docs/api/llms.txt").unlink()
            self.assertIn("missing committed API doc: docs/api/llms.txt", CHECK.check(root))

    def test_generator_output_root_contains_exact_set(self) -> None:
        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as output:
            root = self.fixture(directory)
            import subprocess

            subprocess.run(
                [str(root / "script/gen-api-docs.sh"), "--source-root", str(root), "--output-root", output],
                check=True,
                stdout=subprocess.DEVNULL,
            )
            generated = Path(output) / "docs/api"
            self.assertEqual({path.name for path in generated.iterdir()}, set(CHECK.OUTPUTS))
            self.assertIn((root / "docs/api/home-automation.md").read_text(), (generated / "llms-full.txt").read_text())


if __name__ == "__main__":
    unittest.main()
