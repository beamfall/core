#!/usr/bin/env python3
"""Tests for release-deploy-manifests.py."""

from __future__ import annotations

import importlib.util
import hashlib
import json
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("release-deploy-manifests.py")
SPEC = importlib.util.spec_from_file_location("release_deploy_manifests", MODULE_PATH)
module = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(module)

DIGEST = "sha256:" + "a" * 64
OTHER_DIGEST = "sha256:" + "b" * 64


class ReleaseDeployManifestsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name) / "repo"
        self.out = Path(self.tmp.name) / "out"
        for name, (template, placeholder) in module.TEMPLATES.items():
            path = self.root / template
            path.parent.mkdir(parents=True, exist_ok=True)
            if template.endswith(".xml"):
                path.write_text(
                    f"<Container>\n  <Repository>{placeholder}</Repository>\n</Container>\n",
                    encoding="utf-8",
                )
            else:
                path.write_text(
                    f"services:\n  beamfall:\n    image: {placeholder}\n", encoding="utf-8"
                )

    def render(self, digest: str = DIGEST) -> list[str]:
        return module.render(digest, "v1.2.3", self.out, self.root)

    def verify(self, digest: str = DIGEST) -> list[str]:
        return module.verify(digest, self.out)

    def rebind(self, name: str, content: str) -> None:
        # Rewrite a rendered manifest AND its recorded hash, so the assertion
        # under test is the content policy, not the tamper hash.
        (self.out / name).write_text(content, encoding="utf-8")
        binding_path = self.out / module.BINDING_NAME
        binding = json.loads(binding_path.read_text(encoding="utf-8"))
        binding["manifests"][name]["sha256"] = hashlib.sha256(content.encode()).hexdigest()
        binding_path.write_text(json.dumps(binding), encoding="utf-8")

    def assert_verify_error(self, expected: str, digest: str = DIGEST) -> None:
        errors = self.verify(digest)
        self.assertTrue(
            any(expected in error for error in errors),
            f"missing {expected!r} in {errors!r}",
        )

    def test_render_then_verify_round_trip(self) -> None:
        self.assertEqual([], self.render())
        self.assertEqual([], self.verify())
        pinned = f"{module.IMAGE_REPO}@{DIGEST}"
        for name in module.TEMPLATES:
            self.assertIn(pinned, (self.out / name).read_text(encoding="utf-8"))

    def test_binding_records_release_provenance_identity(self) -> None:
        self.assertEqual([], self.render())
        binding = json.loads((self.out / module.BINDING_NAME).read_text(encoding="utf-8"))
        self.assertEqual(DIGEST, binding["digest"])
        self.assertEqual(f"{module.IMAGE_REPO}@{DIGEST}", binding["image"])
        self.assertEqual("v1.2.3", binding["version"])
        self.assertEqual(sorted(module.TEMPLATES), sorted(binding["manifests"]))

    def test_render_rejects_malformed_digest(self) -> None:
        for digest in ("stable", "sha256:" + "a" * 63, "sha256:" + "A" * 64, ""):
            with self.subTest(digest=digest):
                self.assertTrue(any("malformed image digest" in e for e in self.render(digest)))

    def test_render_fails_closed_on_template_without_placeholder(self) -> None:
        template, _ = module.TEMPLATES["compose.cpu.yml"]
        (self.root / template).write_text(
            "services:\n  beamfall:\n    image: ghcr.io/beamfall/core:stable\n",
            encoding="utf-8",
        )
        errors = self.render()
        self.assertTrue(any("exactly one fail-closed image placeholder" in e for e in errors))

    def test_verify_rejects_wrong_digest(self) -> None:
        self.assertEqual([], self.render())
        self.assert_verify_error("is not the release digest", digest=OTHER_DIGEST)

    def test_verify_rejects_tampered_manifest(self) -> None:
        self.assertEqual([], self.render())
        path = self.out / "compose.cpu.yml"
        path.write_text(path.read_text(encoding="utf-8") + "# tampered\n", encoding="utf-8")
        self.assert_verify_error("does not match its recorded binding hash")

    def test_verify_rejects_tag_rollback(self) -> None:
        self.assertEqual([], self.render())
        self.rebind(
            "compose.cpu.yml",
            "services:\n  beamfall:\n    image: ghcr.io/beamfall/core:stable\n",
        )
        self.assert_verify_error("mutable tag reference")

    def test_verify_rejects_leftover_placeholder(self) -> None:
        self.assertEqual([], self.render())
        self.rebind(
            "unraid-beamfall-core.xml",
            f"<Container>\n  <Repository>{module.UNRAID_PLACEHOLDER}</Repository>\n</Container>\n",
        )
        self.assert_verify_error("leftover fail-closed placeholder")

    def test_verify_rejects_substituted_digest_in_manifest(self) -> None:
        self.assertEqual([], self.render())
        rendered = (self.out / "compose.cpu.yml").read_text(encoding="utf-8")
        self.rebind("compose.cpu.yml", rendered.replace(DIGEST, OTHER_DIGEST))
        self.assert_verify_error("image reference is not the release digest")

    def test_verify_rejects_substituted_binding_digest(self) -> None:
        self.assertEqual([], self.render())
        binding_path = self.out / module.BINDING_NAME
        binding = json.loads(binding_path.read_text(encoding="utf-8"))
        binding["digest"] = OTHER_DIGEST
        binding_path.write_text(json.dumps(binding), encoding="utf-8")
        self.assert_verify_error("binding digest")

    def test_verify_rejects_missing_manifest(self) -> None:
        self.assertEqual([], self.render())
        (self.out / "truenas-docker-compose.yaml").unlink()
        self.assert_verify_error("missing rendered manifest")

    def test_verify_rejects_missing_binding(self) -> None:
        self.assertEqual([], self.render())
        (self.out / module.BINDING_NAME).unlink()
        self.assert_verify_error("cannot read binding manifest")

    def test_cli_render_and_verify_exit_codes(self) -> None:
        argv = ["render", "--digest", DIGEST, "--version", "v1.2.3",
                "--out", str(self.out), "--root", str(self.root)]
        self.assertEqual(0, module.main(argv))
        self.assertEqual(0, module.main(["verify", "--digest", DIGEST, "--dir", str(self.out)]))
        self.assertEqual(
            1, module.main(["verify", "--digest", OTHER_DIGEST, "--dir", str(self.out)])
        )


if __name__ == "__main__":
    unittest.main()
