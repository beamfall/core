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
    source_url = "https://github.com/jellyfin/jellyfin-ffmpeg/archive/refs/tags/v7.1.4-3.tar.gz"
    nvcodec_url = "https://github.com/FFmpeg/nv-codec-headers/archive/refs/tags/n12.0.16.1.tar.gz"
    amf_url = (
        "https://github.com/GPUOpen-LibrariesAndSDKs/AMF/releases/download/v1.4.36/AMF-headers-v1.4.36.tar.gz"
    )

    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        directories = (
            ".github/workflows",
            "internal/ffmpeg",
            "tools/ffmpeg",
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
FROM debian:bookworm@sha256:{debian} AS ffmpeg
COPY internal/ffmpeg/ffmpeg.pin internal/ffmpeg/ffmpeg.lock tools/ffmpeg/build.sh tools/ffmpeg/apt-snapshot.sh /in/
RUN sh /in/build.sh /in/ffmpeg.lock /in/ffmpeg.pin "$TARGETARCH" /out
FROM debian:bookworm@sha256:{debian} AS ffmpeg-runtime
COPY --from=ffmpeg /out/SHA256SUMS /out/*-source.tar /usr/share/beamfall/source/ffmpeg/
FROM ffmpeg-runtime AS runtime
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
        self.write(".github/workflows/gate.yml", workflow)
        self.write(
            ".github/workflows/release-gate.yml",
            workflow
            + "      - run: |\n"
            + "          python3 script/check-release-workflow-pins.py\n"
            + "          docker build --target ffmpeg-runtime -t beamfall-ffmpeg-runtime .\n",
        )
        self.write("tools/ffmpeg/build.sh", 'lock="$1"\nsed -n "s/^$1=//p" "$lock"\n')
        self.write("tools/ffmpeg/apt-snapshot.sh", "sed -n 's/^FFMPEG_DEBIAN_SNAPSHOT=//p' \"$1\"\n")
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
        source_url: str | None = None,
        source_sha: str | None = None,
        nvcodec_url: str | None = None,
        amf_url: str | None = None,
        amf_sha: str | None = None,
        snapshot: str | None = None,
    ) -> None:
        pin = self.read("internal/ffmpeg/ffmpeg.pin")
        self.write(
            "internal/ffmpeg/ffmpeg.lock",
            "\n".join(
                (
                    "FFMPEG_PIN_SHA256="
                    + (pin_sha or hashlib.sha256(pin.encode()).hexdigest()),
                    "FFMPEG_SOURCE_URL=" + (source_url or self.source_url),
                    "FFMPEG_SOURCE_SHA256=" + (source_sha or "3" * 64),
                    "FFMPEG_NVCODEC_URL=" + (nvcodec_url or self.nvcodec_url),
                    "FFMPEG_NVCODEC_SHA256=" + "4" * 64,
                    "FFMPEG_AMF_URL=" + (amf_url or self.amf_url),
                    "FFMPEG_AMF_SHA256=" + (amf_sha or "5" * 64),
                    "FFMPEG_DEBIAN_SNAPSHOT=" + (snapshot or "20261115T000000Z"),
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

    def test_rejects_missing_lock_key(self) -> None:
        lock = self.read("internal/ffmpeg/ffmpeg.lock")
        self.write(
            "internal/ffmpeg/ffmpeg.lock",
            "\n".join(line for line in lock.splitlines() if "AMF" not in line) + "\n",
        )
        self.assert_error("missing lock keys")

    def test_rejects_prebuilt_deb_lock_keys(self) -> None:
        lock = self.read("internal/ffmpeg/ffmpeg.lock")
        self.write("internal/ffmpeg/ffmpeg.lock", lock + "FFMPEG_LINUX_AMD64_URL=" + self.source_url + "\n")
        self.assert_error("unexpected lock keys: FFMPEG_LINUX_AMD64_URL")

    def test_rejects_image_that_does_not_build_from_the_lock(self) -> None:
        self.write("Dockerfile", self.read("Dockerfile").replace("RUN sh /in/build.sh", "RUN sh /in/other.sh"))
        self.assert_error("does not build ffmpeg from internal/ffmpeg/ffmpeg.lock")

    def test_rejects_source_archive_off_the_release_tag(self) -> None:
        self.write_lock(source_url="https://github.com/jellyfin/jellyfin-ffmpeg/archive/refs/heads/jellyfin.tar.gz")
        self.assert_error("FFMPEG_SOURCE_URL is not an immutable release tag archive")

    def test_rejects_header_archives_off_release_tags(self) -> None:
        self.write_lock(nvcodec_url="https://github.com/FFmpeg/nv-codec-headers/archive/refs/heads/master.tar.gz")
        self.assert_error("FFMPEG_NVCODEC_URL is not an immutable release tag archive")
        self.write_lock(amf_url=self.amf_url.replace("AMF-headers-v1.4.36", "AMF-headers-v1.4.35"))
        self.assert_error("FFMPEG_AMF_URL is not an immutable release tag archive")

    def test_rejects_malformed_debian_snapshot(self) -> None:
        self.write_lock(snapshot="20261115")
        self.assert_error("FFMPEG_DEBIAN_SNAPSHOT must be a snapshot.debian.org timestamp")

    def test_rejects_malformed_source_digest(self) -> None:
        self.write_lock(source_sha="ABC")
        self.assert_error("FFMPEG_SOURCE_SHA256 must be a lowercase SHA-256")

    def test_rejects_image_that_omits_corresponding_source(self) -> None:
        self.write("Dockerfile", self.read("Dockerfile").replace("/out/*-source.tar ", ""))
        self.assert_error("does not ship the FFmpeg corresponding source bundle")

    def test_accepts_earlier_stage_as_base(self) -> None:
        self.assertEqual([], [e for e in self.errors() if "container reference is mutable" in e])

    def test_rejects_unnamed_mutable_base(self) -> None:
        self.write("Dockerfile", self.read("Dockerfile").replace("FROM ffmpeg-runtime AS runtime", "FROM ffmpeg-runtimes AS runtime"))
        self.assert_error("container reference is mutable: ffmpeg-runtimes")

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
        self.write(path, self.read(path) + "# " + self.nvcodec_url + "\n")
        self.assert_error("duplicates FFmpeg artifact coordinates")

    def test_rejects_moving_ffmpeg_url(self) -> None:
        for moving in (
            self.source_url.replace("v7.1.4-3", "${FFMPEG_VERSION}"),
            self.source_url + "?channel=stable",
        ):
            with self.subTest(moving=moving):
                self.write_lock(source_url=moving)
                self.assert_error("is not an immutable release tag archive")

    def test_rejects_build_script_that_hardcodes_coordinates(self) -> None:
        path = "tools/ffmpeg/build.sh"
        self.write(path, self.read(path) + "curl " + self.amf_url + "\n")
        self.assert_error("tools/ffmpeg/build.sh: duplicates FFmpeg artifact coordinates")

    def test_rejects_unauthenticated_artifact(self) -> None:
        self.write_lock(amf_sha="not-a-sha")
        self.assert_error("must be a lowercase SHA-256")

    def test_rejects_shell_metacharacters_in_lock(self) -> None:
        for value in ("$(touch/tmp/pwned)", "`touch/tmp/pwned`", "value;false", "value|false"):
            with self.subTest(value=value):
                self.write_lock(amf_sha=value)
                self.assert_error("unsafe shell metacharacters")

    def test_rejects_shell_sourcing_lock(self) -> None:
        path = "Dockerfile"
        self.write(path, self.read(path).replace("RUN sh", "RUN . /in/ffmpeg.lock; sh", 1))
        self.assert_error("without shell-sourcing")

    def test_requires_workflow_validation_before_lock_use(self) -> None:
        path = ".github/workflows/release-gate.yml"
        self.write(path, self.read(path).replace("          python3 script/check-release-workflow-pins.py\n", ""))
        self.assert_error("must validate ffmpeg.lock before building")

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
        self.assert_error("ships FFmpeg 7.1.4, not ffmpeg.pin's 7.1.2")

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
