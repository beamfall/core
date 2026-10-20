#!/usr/bin/env python3
"""Reject mutable or divergent release and container inputs."""

from __future__ import annotations

import hashlib
import os
import re
import signal
import subprocess
import sys
from pathlib import Path
from urllib.parse import urlsplit


USES_RE = re.compile(r"^\s*-?\s*uses:\s*([^\s#]+)")
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
FROM_RE = re.compile(r"^\s*FROM(?:\s+--[^\s]+)*\s+([^\s#]+)", re.IGNORECASE)
IMAGE_RE = re.compile(r"^\s*image:\s*([^\s#]+)")
GO_RE = re.compile(r"^go\s+(\d+\.\d+\.\d+)\s*$", re.MULTILINE)
GO_ARG_RE = re.compile(r"^ARG GO_VERSION=(\d+\.\d+\.\d+)\s*$", re.MULTILINE)
NODE_IMAGE_RE = re.compile(r"^FROM(?:\s+--[^\s]+)*\s+node:(\d+)[^\s@]*@sha256:", re.MULTILINE | re.IGNORECASE)
TOOLCHAIN_IMAGE_RE = re.compile(
    r"^FROM(?:\s+--[^\s]+)*\s+((node|golang):([^\s@]+)@sha256:[0-9a-f]{64})",
    re.MULTILINE | re.IGNORECASE,
)
NODE_WORKFLOW_RE = re.compile(r"^\s*node-version:\s*['\"]?(\d+)['\"]?\s*(?:#.*)?$", re.MULTILINE)
LOCK_KEYS = {
    "FFMPEG_PIN_SHA256",
    "FFMPEG_LINUX_AMD64_URL",
    "FFMPEG_LINUX_AMD64_SHA256",
    "FFMPEG_LINUX_ARM64_URL",
    "FFMPEG_LINUX_ARM64_SHA256",
}
PIN_RE = re.compile(r"^(\d+\.\d+\.\d+)-Jellyfin\n$")
LOCK_LINE_RE = re.compile(r"^([A-Z0-9_]+)=([^\s#]+)$")
LOCK_VALUE_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:/?-]*$")
LOCK_SOURCE_RE = re.compile(r"(?:^|\s)(?:\.|source)\s+\S*ffmpeg\.lock\b", re.MULTILINE)
FAIL_CLOSED_IMAGE = "${BEAMFALL_IMAGE:?immutable-digest-reference-required}"
UNRAID_PLACEHOLDER = "ghcr.io/beamfall/core@sha256:RELEASE_DIGEST_REQUIRED"
UNRAID_REPOSITORY_RE = re.compile(r"<Repository>([^<]*)</Repository>")
FFMPEG_URL_RE = re.compile(
    r"^https://repo\.jellyfin\.org/files/ffmpeg/debian/\d+\.x/"
    r"(\d+\.\d+\.\d+-\d+)/(amd64|arm64)/"
    r"jellyfin-ffmpeg\d+_\1-bookworm_\2\.deb$"
)


def check_file(path: Path) -> list[str]:
    errors: list[str] = []
    for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        match = USES_RE.match(line)
        if not match:
            continue
        action = match.group(1)
        if action.startswith("./"):
            continue
        if "@" not in action:
            errors.append(f"{path}:{line_number}: action reference has no immutable SHA: {action}")
            continue
        ref = action.rsplit("@", 1)[1]
        if not SHA_RE.fullmatch(ref):
            errors.append(f"{path}:{line_number}: action reference is mutable: {action}")
    return errors


def _read(path: Path, errors: list[str]) -> str:
    try:
        return path.read_text(encoding="utf-8")
    except OSError as exc:
        errors.append(f"{path}: cannot read required supply-chain input: {exc}")
        return ""


def _check_container(path: Path, line_number: int, image: str, errors: list[str]) -> None:
    image = image.strip("'\"")
    if image == "scratch" or image == FAIL_CLOSED_IMAGE:
        return
    if not re.search(r"@sha256:[0-9a-f]{64}$", image):
        errors.append(f"{path}:{line_number}: container reference is mutable: {image}")


def _check_containers(root: Path) -> list[str]:
    errors: list[str] = []
    dockerfile = root / "Dockerfile"
    for line_number, line in enumerate(_read(dockerfile, errors).splitlines(), 1):
        if match := FROM_RE.match(line):
            _check_container(dockerfile, line_number, match.group(1), errors)

    compose_paths = set(root.glob("docker-compose*.yml")) | set(root.glob("docker-compose*.yaml"))
    compose_paths |= set((root / "packaging/compose").glob("*.yml"))
    compose_paths |= set((root / "packaging/compose").glob("*.yaml"))
    compose_paths |= set((root / "packaging/truenas").glob("*.yml"))
    compose_paths |= set((root / "packaging/truenas").glob("*.yaml"))
    workflow_paths = set((root / ".github/workflows").glob("*.yml"))
    workflow_paths |= set((root / ".github/workflows").glob("*.yaml"))
    for path in sorted(compose_paths | workflow_paths):
        for line_number, line in enumerate(_read(path, errors).splitlines(), 1):
            if match := IMAGE_RE.match(line):
                _check_container(path, line_number, match.group(1), errors)
    return errors


def _check_deploy_manifests(root: Path) -> list[str]:
    # Released deployment artifacts must select an immutable digest; the
    # committed source templates must fail closed instead of defaulting to a
    # mutable channel tag (SECAUDIT-20260817-DEPLOY-DIGEST-PINS-1).
    errors: list[str] = []
    unraid = root / "packaging/unraid/beamfall-core.xml"
    match = UNRAID_REPOSITORY_RE.search(_read(unraid, errors))
    if match:
        repository = match.group(1)
        pinned = re.fullmatch(r"ghcr\.io/beamfall/core@sha256:[0-9a-f]{64}", repository)
        if not pinned and repository != UNRAID_PLACEHOLDER:
            errors.append(
                f"{unraid}: Repository must be an immutable digest reference or the"
                f" fail-closed placeholder, not: {repository}"
            )
    else:
        errors.append(f"{unraid}: missing <Repository> element")

    release = root / ".github/workflows/release.yml"
    content = _read(release, errors)
    for verb in ("render", "verify"):
        if f"script/release-deploy-manifests.py {verb}" not in content:
            errors.append(f"{release}: must {verb} digest-pinned deployment manifests")
    return errors


def _parse_lock(path: Path, errors: list[str]) -> dict[str, str]:
    values: dict[str, str] = {}
    for line_number, raw in enumerate(_read(path, errors).splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        match = LOCK_LINE_RE.fullmatch(line)
        if not match:
            errors.append(f"{path}:{line_number}: malformed lock entry")
            continue
        key, value = match.groups()
        if not LOCK_VALUE_RE.fullmatch(value):
            errors.append(f"{path}:{line_number}: lock value contains unsafe shell metacharacters")
        if key in values:
            errors.append(f"{path}:{line_number}: duplicate lock key: {key}")
        values[key] = value
    missing = sorted(LOCK_KEYS - values.keys())
    unexpected = sorted(values.keys() - LOCK_KEYS)
    if missing:
        errors.append(f"{path}: missing lock keys: {', '.join(missing)}")
    if unexpected:
        errors.append(f"{path}: unexpected lock keys: {', '.join(unexpected)}")
    return values


def _check_ffmpeg_lock(root: Path) -> list[str]:
    errors: list[str] = []
    pin_path = root / "internal/ffmpeg/ffmpeg.pin"
    lock_path = root / "internal/ffmpeg/ffmpeg.lock"
    pin_text = _read(pin_path, errors)
    pin = pin_text.encode()
    lock = _parse_lock(lock_path, errors)
    if not LOCK_KEYS.issubset(lock):
        return errors

    # Hashing the pin binds the lock to a pin *file*; it cannot tell that the
    # locked artifacts carry the version the pin names. The Dockerfile asserts
    # the installed binary's version against the pin, so a lock that authenticates
    # the wrong release is a build that always fails.
    pin_match = PIN_RE.fullmatch(pin_text)
    if not pin_match:
        errors.append(f"{pin_path}: must hold exactly one normalized FFmpeg version token")
        return errors
    pin_version = pin_match.group(1)

    pin_sha = lock["FFMPEG_PIN_SHA256"]
    if not SHA256_RE.fullmatch(pin_sha):
        errors.append(f"{lock_path}: FFMPEG_PIN_SHA256 must be a lowercase SHA-256")
    elif pin_sha != hashlib.sha256(pin).hexdigest():
        errors.append(f"{lock_path}: FFMPEG_PIN_SHA256 does not match ffmpeg.pin")

    for arch in ("amd64", "arm64"):
        prefix = f"FFMPEG_LINUX_{arch.upper()}"
        url = lock[f"{prefix}_URL"]
        digest = lock[f"{prefix}_SHA256"]
        parsed = urlsplit(url)
        match = FFMPEG_URL_RE.fullmatch(url)
        if parsed.query or parsed.fragment or "$" in url or not match:
            errors.append(f"{lock_path}: {prefix}_URL is not an immutable versioned URL")
        elif match.group(2) != arch:
            errors.append(f"{lock_path}: {prefix}_URL does not match linux/{arch}")
        elif match.group(1).rsplit("-", 1)[0] != pin_version:
            errors.append(f"{lock_path}: {prefix}_URL ships FFmpeg {match.group(1)}, not ffmpeg.pin's {pin_version}")
        if not SHA256_RE.fullmatch(digest):
            errors.append(f"{lock_path}: {prefix}_SHA256 must be a lowercase SHA-256")

    consumers = [root / "Dockerfile", root / ".github/workflows/release-gate.yml"]
    coordinate_literals = [value for key, value in lock.items() if key != "FFMPEG_PIN_SHA256"]
    coordinate_literals += [Path(urlsplit(lock[key]).path).name for key in lock if key.endswith("_URL")]
    for path in consumers:
        content = _read(path, errors)
        if "internal/ffmpeg/ffmpeg.lock" not in content and "/tmp/ffmpeg.lock" not in content:
            errors.append(f"{path}: does not consume internal/ffmpeg/ffmpeg.lock")
        if LOCK_SOURCE_RE.search(content):
            errors.append(f"{path}: must validate and parse ffmpeg.lock without shell-sourcing it")
        if "repo.jellyfin.org/files/ffmpeg" in content or any(value in content for value in coordinate_literals):
            errors.append(f"{path}: duplicates FFmpeg artifact coordinates outside ffmpeg.lock")
        if path.name == "Dockerfile":
            for arch in ("amd64", "arm64"):
                block = re.search(rf"\b{arch}\)(.*?);;", content, re.DOTALL)
                prefix = f"FFMPEG_LINUX_{arch.upper()}"
                if not block or any(f"${prefix}_{field}" not in block.group(1) for field in ("URL", "SHA256")):
                    errors.append(f"{path}: does not map TARGETARCH {arch} to its locked artifact")
        else:
            policy = content.find("python3 script/check-release-workflow-pins.py")
            use = content.find("$FFMPEG_LINUX_AMD64_URL")
            if policy < 0 or use < 0 or policy > use:
                errors.append(f"{path}: must validate ffmpeg.lock before using the locked linux/amd64 artifact")
            if any(f"$FFMPEG_LINUX_AMD64_{field}" not in content for field in ("URL", "SHA256")):
                errors.append(f"{path}: does not use the locked linux/amd64 artifact")
    return errors


def _check_toolchains(root: Path) -> list[str]:
    errors: list[str] = []
    go_mod = _read(root / "go.mod", errors)
    dockerfile = _read(root / "Dockerfile", errors)
    go_version = GO_RE.search(go_mod)
    docker_go = GO_ARG_RE.search(dockerfile)
    if not go_version or not docker_go or go_version.group(1) != docker_go.group(1):
        errors.append(f"{root / 'Dockerfile'}: Go version drift from go.mod")

    node_image = NODE_IMAGE_RE.search(dockerfile)
    if not node_image:
        errors.append(f"{root / 'Dockerfile'}: missing digest-pinned Node toolchain image")
        return errors
    expected_node = node_image.group(1)
    for path in sorted((root / ".github/workflows").glob("*.y*ml")):
        for version in NODE_WORKFLOW_RE.findall(_read(path, errors)):
            if version != expected_node:
                errors.append(f"{path}: Node version {version} drifts from Docker Node {expected_node}")
    return errors


def _check_toolchain_images(root: Path, runner=None) -> list[str]:
    errors: list[str] = []
    go_mod = _read(root / "go.mod", errors)
    dockerfile = _read(root / "Dockerfile", errors)
    go_version = GO_RE.search(go_mod)
    go_arg = GO_ARG_RE.search(dockerfile)
    images = TOOLCHAIN_IMAGE_RE.findall(dockerfile)
    expected = {"golang": go_version.group(1) if go_version else "", "node": ""}
    run = runner or subprocess.run
    seen: set[str] = set()

    for image, tool, tag in images:
        tool = tool.lower()
        seen.add(tool)
        if tool == "golang":
            if not go_arg:
                errors.append(f"{root / 'Dockerfile'}: missing ARG GO_VERSION for pinned golang image")
                continue
            image = image.replace("${GO_VERSION}", go_arg.group(1))
            command = ("go", "env", "GOVERSION")
        else:
            expected["node"] = tag.split("-", 1)[0]
            command = ("node", "--version")
        container = f"beamfall-{tool}-toolchain-check-{os.getpid()}"
        try:
            result = run(
                ["docker", "run", "--rm", "--name", container, image, *command],
                capture_output=True,
                text=True,
                timeout=300,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            errors.append(f"{image}: cannot inspect pinned toolchain image: {exc}")
            continue
        finally:
            try:
                run(
                    ["docker", "rm", "--force", container],
                    capture_output=True,
                    text=True,
                    timeout=30,
                )
            except (OSError, subprocess.TimeoutExpired):
                pass
        if result.returncode:
            errors.append(f"{image}: cannot inspect pinned toolchain image: {result.stderr.strip()}")
            continue
        actual = result.stdout.strip().removeprefix("go").removeprefix("v")
        if tool == "node":
            actual = actual.split(".", 1)[0]
        if actual != expected[tool]:
            errors.append(f"{image}: reports {tool} {actual}, expected {expected[tool]}")
    for tool in sorted(expected.keys() - seen):
        errors.append(f"{root / 'Dockerfile'}: missing digest-pinned {tool} toolchain image")
    return errors


def _check_dependabot(root: Path) -> list[str]:
    errors: list[str] = []
    path = root / ".github/dependabot.yml"
    content = _read(path, errors)
    for ecosystem in ("github-actions", "docker"):
        if not re.search(rf"package-ecosystem:\s*['\"]?{ecosystem}['\"]?", content):
            errors.append(f"{path}: Dependabot must update {ecosystem}")
    return errors


def check_repository(root: Path) -> list[str]:
    workflows = sorted((root / ".github/workflows").glob("*.yml"))
    workflows += sorted((root / ".github/workflows").glob("*.yaml"))
    errors = [error for path in workflows for error in check_file(path)]
    errors += _check_containers(root)
    errors += _check_deploy_manifests(root)
    errors += _check_ffmpeg_lock(root)
    errors += _check_toolchains(root)
    errors += _check_dependabot(root)
    return errors


def main(argv: list[str]) -> int:
    if argv == ["--verify-toolchain-images"]:
        previous_sigterm = signal.signal(signal.SIGTERM, signal.default_int_handler)
        try:
            errors = _check_toolchain_images(Path("."))
        except KeyboardInterrupt:
            print("pinned toolchain image check interrupted", file=sys.stderr)
            return 130
        finally:
            signal.signal(signal.SIGTERM, previous_sigterm)
        checked = "pinned toolchain images"
    elif argv:
        paths = [Path(value) for value in argv]
        errors = [error for path in paths for error in check_file(path)]
        checked = f"{len(paths)} workflow files"
    else:
        errors = check_repository(Path("."))
        checked = "repository inputs"
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(f"release supply-chain pins: OK ({checked})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
