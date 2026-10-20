#!/usr/bin/env python3
"""Tests for check-release-workflow-pins.py."""

from __future__ import annotations

import importlib.util
import hashlib
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock


MODULE_PATH = Path(__file__).with_name("check-release-workflow-pins.py")
SPEC = importlib.util.spec_from_file_location("check_release_workflow_pins", MODULE_PATH)
module = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(module)


class ReleaseWorkflowPinsTests(unittest.TestCase):
    def check(self, content: str) -> list[str]:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "release.yml"
            path.write_text(content, encoding="utf-8")
            return module.check_file(path)

    def test_accepts_sha_pinned_and_local_actions(self) -> None:
        sha = "a" * 40
        self.assertEqual([], self.check(f"- uses: actions/checkout@{sha} # v5\n- uses: ./local\n"))

    def test_rejects_mutable_tag(self) -> None:
        errors = self.check("- uses: actions/checkout@v5\n")
        self.assertEqual(1, len(errors))
        self.assertIn("mutable", errors[0])

    def test_rejects_missing_ref(self) -> None:
        errors = self.check("uses: actions/checkout\n")
        self.assertEqual(1, len(errors))
        self.assertIn("no immutable SHA", errors[0])


class SupplyChainPolicyTests(unittest.TestCase):
    amd64_url = (
        "https://repo.jellyfin.org/files/ffmpeg/debian/7.x/7.1.4-3/amd64/"
        "jellyfin-ffmpeg7_7.1.4-3-bookworm_amd64.deb"
    )
    arm64_url = (
        "https://repo.jellyfin.org/files/ffmpeg/debian/7.x/7.1.4-3/arm64/"
        "jellyfin-ffmpeg7_7.1.4-3-bookworm_arm64.deb"
    )
    noble_url = (
        "https://repo.jellyfin.org/files/ffmpeg/ubuntu/7.x/7.1.4-3/amd64/"
        "jellyfin-ffmpeg7_7.1.4-3-noble_amd64.deb"
    )

    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        directories = (
            ".github/workflows",
            "internal/ffmpeg",
            "packaging/compose",
            "packaging/truenas",
            "packaging/unraid",
        )
        for directory in directories:
            (self.root / directory).mkdir(parents=True)
        self.write("go.mod", "module example.test/core\n\ngo 1.26.5\n")
        self.write("internal/ffmpeg/ffmpeg.pin", "7.1.4-Jellyfin\n")
        self.write_lock()
        self.write(
            "Dockerfile",
            """ARG GO_VERSION=1.26.5
FROM node:22-bookworm@sha256:{node}
FROM golang:${{GO_VERSION}}-bookworm@sha256:{go}
FROM debian:bookworm@sha256:{debian}
COPY internal/ffmpeg/ffmpeg.pin internal/ffmpeg/ffmpeg.lock /tmp/
RUN FFMPEG_LINUX_AMD64_URL="$(sed -n 's/^FFMPEG_LINUX_AMD64_URL=//p' /tmp/ffmpeg.lock)"; FFMPEG_LINUX_AMD64_SHA256="$(sed -n 's/^FFMPEG_LINUX_AMD64_SHA256=//p' /tmp/ffmpeg.lock)"; FFMPEG_LINUX_ARM64_URL="$(sed -n 's/^FFMPEG_LINUX_ARM64_URL=//p' /tmp/ffmpeg.lock)"; FFMPEG_LINUX_ARM64_SHA256="$(sed -n 's/^FFMPEG_LINUX_ARM64_SHA256=//p' /tmp/ffmpeg.lock)"; case "$TARGETARCH" in amd64) ffmpeg_url="$FFMPEG_LINUX_AMD64_URL"; ffmpeg_sha="$FFMPEG_LINUX_AMD64_SHA256";; arm64) ffmpeg_url="$FFMPEG_LINUX_ARM64_URL"; ffmpeg_sha="$FFMPEG_LINUX_ARM64_SHA256";; esac
""".format(node="c" * 64, go="d" * 64, debian="e" * 64),
        )
        workflow = """jobs:
  gate:
    steps:
      - uses: actions/checkout@{action}
      - uses: actions/setup-node@{action}
        with:
          node-version: 22
""".format(action="a" * 40)
        install = "      - run: sh .github/install-ffmpeg.sh\n"
        self.write(".github/workflows/gate.yml", workflow + install)
        self.write(".github/workflows/release-gate.yml", workflow + install)
        self.write(
            ".github/install-ffmpeg.sh",
            "python3 script/check-release-workflow-pins.py\n"
            "FFMPEG_UBUNTU_NOBLE_AMD64_URL=\"$(sed -n 's/^FFMPEG_UBUNTU_NOBLE_AMD64_URL=//p' internal/ffmpeg/ffmpeg.lock)\"\n"
            "FFMPEG_UBUNTU_NOBLE_AMD64_SHA256=\"$(sed -n 's/^FFMPEG_UBUNTU_NOBLE_AMD64_SHA256=//p' internal/ffmpeg/ffmpeg.lock)\"\n"
            "ffmpeg_url=\"$FFMPEG_UBUNTU_NOBLE_AMD64_URL\"\n"
            "ffmpeg_sha=\"$FFMPEG_UBUNTU_NOBLE_AMD64_SHA256\"\n",
        )
        self.write(
            "packaging/compose/compose.public.yml",
            "services:\n  proxy:\n    image: caddy:2.9.1-alpine@sha256:" + "f" * 64 + "\n",
        )
        fail_closed = "services:\n  beamfall:\n    image: " + module.FAIL_CLOSED_IMAGE + "\n"
        self.write("packaging/compose/compose.cpu.yml", fail_closed)
        self.write("packaging/truenas/docker-compose.yaml", fail_closed)
        self.write(
            "packaging/unraid/beamfall-core.xml",
            "<Container>\n  <Repository>"
            + module.UNRAID_PLACEHOLDER
            + "</Repository>\n</Container>\n",
        )
        self.write(
            ".github/workflows/release.yml",
            workflow
            + "      - run: |\n"
            + "          python3 script/release-deploy-manifests.py render"
            + ' --digest "${digest}" --out /tmp/deploy-manifests\n'
            + "          python3 script/release-deploy-manifests.py verify"
            + ' --digest "${digest}" --dir /tmp/deploy-manifests\n',
        )
        self.write(
            ".github/dependabot.yml",
            'version: 2\nupdates:\n  - package-ecosystem: "github-actions"\n'
            '    directory: "/"\n  - package-ecosystem: "docker"\n    directory: "/"\n',
        )

    def write(self, relative: str, content: str) -> None:
        (self.root / relative).write_text(content, encoding="utf-8")

    def read(self, relative: str) -> str:
        return (self.root / relative).read_text(encoding="utf-8")

    def write_lock(
        self,
        *,
        pin_sha: str | None = None,
        amd64_url: str | None = None,
        arm64_url: str | None = None,
        amd64_sha: str | None = None,
        arm64_sha: str | None = None,
        noble_url: str | None = None,
        noble_sha: str | None = None,
    ) -> None:
        pin = self.read("internal/ffmpeg/ffmpeg.pin")
        self.write(
            "internal/ffmpeg/ffmpeg.lock",
            "\n".join(
                (
                    "FFMPEG_PIN_SHA256="
                    + (pin_sha or hashlib.sha256(pin.encode()).hexdigest()),
                    "FFMPEG_LINUX_AMD64_URL=" + (amd64_url or self.amd64_url),
                    "FFMPEG_LINUX_AMD64_SHA256=" + (amd64_sha or "1" * 64),
                    "FFMPEG_LINUX_ARM64_URL=" + (arm64_url or self.arm64_url),
                    "FFMPEG_LINUX_ARM64_SHA256=" + (arm64_sha or "2" * 64),
                    "FFMPEG_UBUNTU_NOBLE_AMD64_URL=" + (noble_url or self.noble_url),
                    "FFMPEG_UBUNTU_NOBLE_AMD64_SHA256=" + (noble_sha or "3" * 64),
                )
            )
            + "\n",
        )

    def errors(self) -> list[str]:
        return module.check_repository(self.root)

    def assert_error(self, expected: str) -> None:
        self.assertTrue(
            any(expected in error for error in self.errors()),
            f"missing {expected!r} in {self.errors()!r}",
        )

    def test_accepts_immutable_single_sourced_inputs(self) -> None:
        self.assertEqual([], self.errors())

    def test_rejects_mutable_action(self) -> None:
        path = ".github/workflows/gate.yml"
        self.write(path, self.read(path).replace("@" + "a" * 40, "@v5", 1))
        self.assert_error("action reference is mutable")

    def test_rejects_mutable_dockerfile_base(self) -> None:
        self.write("Dockerfile", self.read("Dockerfile").replace("@sha256:" + "c" * 64, ""))
        self.assert_error("container reference is mutable")

    def test_rejects_mutable_compose_image(self) -> None:
        path = "packaging/compose/compose.public.yml"
        self.write(path, self.read(path).replace("@sha256:" + "f" * 64, ""))
        self.assert_error("container reference is mutable")

    def test_rejects_beamfall_channel_tag_in_deploy_template(self) -> None:
        self.write(
            "packaging/compose/compose.cpu.yml",
            "services:\n  beamfall:\n    image: ghcr.io/beamfall/core:${BEAMFALL_CHANNEL:-stable}\n",
        )
        self.assert_error("container reference is mutable")

    def test_rejects_beamfall_pinned_tag_reference(self) -> None:
        # The old policy whitelisted every ghcr.io/beamfall/core: tag; a tag is
        # mutable no matter whose registry it lives in.
        self.write(
            "packaging/compose/compose.cpu.yml",
            "services:\n  beamfall:\n    image: ghcr.io/beamfall/core:stable\n",
        )
        self.assert_error("container reference is mutable")

    def test_rejects_mutable_truenas_image(self) -> None:
        self.write(
            "packaging/truenas/docker-compose.yaml",
            "services:\n  beamfall:\n    image: ghcr.io/beamfall/core:stable\n",
        )
        self.assert_error("container reference is mutable")

    def test_accepts_digest_pinned_beamfall_image(self) -> None:
        self.write(
            "packaging/compose/compose.cpu.yml",
            "services:\n  beamfall:\n    image: ghcr.io/beamfall/core@sha256:" + "9" * 64 + "\n",
        )
        self.assertEqual([], self.errors())

    def test_rejects_other_fail_closed_variable(self) -> None:
        self.write(
            "packaging/compose/compose.cpu.yml",
            "services:\n  beamfall:\n    image: ${OTHER_IMAGE:?immutable-digest-reference-required}\n",
        )
        self.assert_error("container reference is mutable")

    def test_rejects_unraid_tag_repository(self) -> None:
        self.write(
            "packaging/unraid/beamfall-core.xml",
            "<Container>\n  <Repository>ghcr.io/beamfall/core:stable</Repository>\n</Container>\n",
        )
        self.assert_error("immutable digest reference or the fail-closed placeholder")

    def test_accepts_unraid_digest_repository(self) -> None:
        self.write(
            "packaging/unraid/beamfall-core.xml",
            "<Container>\n  <Repository>ghcr.io/beamfall/core@sha256:"
            + "9" * 64
            + "</Repository>\n</Container>\n",
        )
        self.assertEqual([], self.errors())

    def test_rejects_unraid_missing_repository(self) -> None:
        self.write("packaging/unraid/beamfall-core.xml", "<Container>\n</Container>\n")
        self.assert_error("missing <Repository> element")

    def test_requires_release_workflow_render_and_verify(self) -> None:
        path = ".github/workflows/release.yml"
        for verb in ("render", "verify"):
            with self.subTest(verb=verb):
                content = self.read(path)
                removed = "\n".join(
                    line
                    for line in content.splitlines()
                    if f"release-deploy-manifests.py {verb}" not in line
                )
                self.write(path, removed + "\n")
                self.assert_error(f"must {verb} digest-pinned deployment manifests")
                self.write(path, content)

    def test_rejects_missing_architecture(self) -> None:
        lock = self.read("internal/ffmpeg/ffmpeg.lock")
        self.write(
            "internal/ffmpeg/ffmpeg.lock",
            "\n".join(line for line in lock.splitlines() if "ARM64" not in line) + "\n",
        )
        self.assert_error("missing lock keys")

    def test_rejects_wrong_architecture(self) -> None:
        self.write_lock(amd64_url=self.arm64_url)
        self.assert_error("does not match linux/amd64")

    def test_rejects_wrong_consumer_architecture(self) -> None:
        self.write(
            "Dockerfile",
            self.read("Dockerfile").replace(
                'amd64) ffmpeg_url="$FFMPEG_LINUX_AMD64_URL"',
                'amd64) ffmpeg_url="$FFMPEG_LINUX_ARM64_URL"',
            ),
        )
        self.assert_error("does not map TARGETARCH amd64")

    def test_rejects_toolchain_version_drift(self) -> None:
        self.write("Dockerfile", self.read("Dockerfile").replace("GO_VERSION=1.26.5", "GO_VERSION=1.26.4"))
        self.assert_error("Go version drift")

    def check_toolchain_images(self, go: str = "go1.26.5", node: str = "v22.17.0") -> list[str]:
        def fake_run(command: list[str], **_: object) -> subprocess.CompletedProcess[str]:
            version = go if command[-3:] == ["go", "env", "GOVERSION"] else node
            return subprocess.CompletedProcess(command, 0, version + "\n", "")

        return module._check_toolchain_images(self.root, runner=fake_run)

    def test_accepts_matching_toolchain_image_versions(self) -> None:
        self.assertEqual([], self.check_toolchain_images())

    def test_rejects_golang_digest_with_wrong_version(self) -> None:
        errors = self.check_toolchain_images(go="go1.26.4")
        self.assertTrue(any("reports golang 1.26.4, expected 1.26.5" in error for error in errors))

    def test_rejects_node_digest_with_wrong_major_version(self) -> None:
        errors = self.check_toolchain_images(node="v21.7.3")
        self.assertTrue(any("reports node 21, expected 22" in error for error in errors))

    def test_interruption_removes_started_container(self) -> None:
        commands: list[list[str]] = []

        def interrupted_run(command: list[str], **_: object) -> subprocess.CompletedProcess[str]:
            commands.append(command)
            if command[1] == "run":
                raise KeyboardInterrupt
            return subprocess.CompletedProcess(command, 0, "", "")

        with self.assertRaises(KeyboardInterrupt):
            module._check_toolchain_images(self.root, runner=interrupted_run)
        self.assertEqual(["docker", "rm", "--force"], commands[-1][:3])
        self.assertEqual(commands[0][4], commands[-1][3])

    def test_static_repository_check_never_runs_docker(self) -> None:
        with mock.patch.object(module.subprocess, "run", side_effect=AssertionError("docker invoked")):
            self.assertEqual([], self.errors())

    def test_rejects_duplicate_ffmpeg_coordinate_literal(self) -> None:
        path = ".github/workflows/release-gate.yml"
        self.write(path, self.read(path) + "# " + self.amd64_url + "\n")
        self.assert_error("duplicates FFmpeg artifact coordinates")

    def test_rejects_moving_ffmpeg_url(self) -> None:
        for moving in (
            self.amd64_url.replace("7.x/7.1.4-3", "latest-7.x"),
            self.amd64_url.replace("7.1.4-3", "${FFMPEG_VERSION}"),
            self.amd64_url + "?channel=stable",
        ):
            with self.subTest(moving=moving):
                self.write_lock(amd64_url=moving)
                self.assert_error("is not an immutable versioned URL")

    def test_rejects_unauthenticated_artifact(self) -> None:
        self.write_lock(amd64_sha="not-a-sha")
        self.assert_error("must be a lowercase SHA-256")

    def test_rejects_shell_metacharacters_in_lock(self) -> None:
        for value in ("$(touch/tmp/pwned)", "`touch/tmp/pwned`", "value;false", "value|false"):
            with self.subTest(value=value):
                self.write_lock(amd64_sha=value)
                self.assert_error("unsafe shell metacharacters")

    def test_rejects_shell_sourcing_lock(self) -> None:
        path = "Dockerfile"
        self.write(path, self.read(path).replace("RUN ", "RUN . /tmp/ffmpeg.lock; ", 1))
        self.assert_error("without shell-sourcing")

    def test_requires_installer_validation_before_lock_use(self) -> None:
        path = ".github/install-ffmpeg.sh"
        self.write(path, self.read(path).replace("python3 script/check-release-workflow-pins.py\n", ""))
        self.assert_error("must validate ffmpeg.lock before using")

    def test_requires_ci_workflows_to_use_the_installer(self) -> None:
        for path in (".github/workflows/gate.yml", ".github/workflows/release-gate.yml"):
            with self.subTest(path=path):
                content = self.read(path)
                self.write(path, content.replace("sh .github/install-ffmpeg.sh", "true"))
                self.assert_error("must install FFmpeg through .github/install-ffmpeg.sh")
                self.write(path, content)

    def test_rejects_duplicate_ffmpeg_coordinate_in_installer(self) -> None:
        path = ".github/install-ffmpeg.sh"
        self.write(path, self.read(path) + "# " + self.noble_url + "\n")
        self.assert_error("duplicates FFmpeg artifact coordinates")

    def test_rejects_non_noble_ci_artifact(self) -> None:
        for url in (self.amd64_url, self.noble_url.replace("noble", "jammy")):
            with self.subTest(url=url):
                self.write_lock(noble_url=url)
                self.assert_error("FFMPEG_UBUNTU_NOBLE_AMD64_URL is not an immutable versioned URL")

    def test_rejects_ci_artifact_of_another_ffmpeg_release(self) -> None:
        self.write_lock(noble_url=self.noble_url.replace("7.1.4-3", "7.1.2-1"))
        self.assert_error("FFMPEG_UBUNTU_NOBLE_AMD64_URL ships FFmpeg 7.1.2-1")

    def test_rejects_unauthenticated_ci_artifact(self) -> None:
        self.write_lock(noble_sha="not-a-sha")
        self.assert_error("FFMPEG_UBUNTU_NOBLE_AMD64_SHA256 must be a lowercase SHA-256")

    def test_make_gate_runs_policy_and_planted_red_suite(self) -> None:
        root = MODULE_PATH.parent.parent
        makefile = root.joinpath("Makefile").read_text(encoding="utf-8")
        gate = root.joinpath("tools/gate/gate.sh").read_text(encoding="utf-8")
        self.assertIn(
            'run_step "release workflow pins check" make release-workflow-pins-check',
            gate,
        )
        target = makefile.split("release-workflow-pins-check:", 1)[1].split("\n\n", 1)[0]
        self.assertIn("python3 script/check-release-workflow-pins.py", target)
        self.assertIn("python3 -m unittest script/check_release_workflow_pins_test.py", target)
        self.assertIn("python3 -m unittest script/release_deploy_manifests_test.py", target)

    def test_release_gate_checks_images_without_adding_network_to_merge_gate(self) -> None:
        makefile = MODULE_PATH.parent.parent.joinpath("Makefile").read_text(encoding="utf-8")
        gate = next(line for line in makefile.splitlines() if line.startswith("gate:"))
        release = next(line for line in makefile.splitlines() if line.startswith("release-test-gates:"))
        target = makefile.split("release-toolchain-images-check:", 1)[1].split("\n\n", 1)[0]
        self.assertNotIn("release-toolchain-images-check", gate.split()[1:])
        self.assertIn("release-toolchain-images-check", release.split()[1:])
        self.assertIn("--verify-toolchain-images", target)

    def test_rejects_lock_pin_divergence(self) -> None:
        self.write("internal/ffmpeg/ffmpeg.pin", "7.1.2-Jellyfin\n")
        self.assert_error("does not match ffmpeg.pin")

    def test_rejects_locked_artifact_of_another_ffmpeg_release(self) -> None:
        # Re-hashing the lock against the new pin keeps the binding self-consistent,
        # which is exactly how the 7.1.1-pin/7.1.4-artifact drift reached main: the
        # Dockerfile's version assert was the only thing that noticed, at build time.
        self.write("internal/ffmpeg/ffmpeg.pin", "7.1.2-Jellyfin\n")
        self.write_lock()
        self.assert_error("ships FFmpeg 7.1.4-3, not ffmpeg.pin's 7.1.2")

    def test_rejects_unparseable_pin(self) -> None:
        for pin in ("7.1.4\n", "7.1.4-Jellyfin", "7.1.4-Jellyfin\n# comment\n"):
            with self.subTest(pin=pin):
                self.write("internal/ffmpeg/ffmpeg.pin", pin)
                self.write_lock()
                self.assert_error("must hold exactly one normalized FFmpeg version token")

    def test_requires_dependency_automation(self) -> None:
        self.write(".github/dependabot.yml", 'version: 2\nupdates:\n  - package-ecosystem: "docker"\n')
        self.assert_error("Dependabot must update github-actions")


if __name__ == "__main__":
    unittest.main()
