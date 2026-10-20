#!/usr/bin/env python3
"""Render and verify digest-pinned deployment manifests for a release.

`render` substitutes the release's pushed image digest into the fail-closed
source templates and writes a binding manifest tying every rendered file to
that digest — the same digest cosign signs, so the bundle is bound to release
provenance. `verify` recomputes the binding fail-closed: wrong, missing, or
substituted digests, tag rollback, leftover placeholders, and tampered files
are all rejected (SECAUDIT-20260817-DEPLOY-DIGEST-PINS-1).
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
from pathlib import Path


IMAGE_REPO = "ghcr.io/beamfall/core"
FAIL_CLOSED_IMAGE = "${BEAMFALL_IMAGE:?immutable-digest-reference-required}"
UNRAID_PLACEHOLDER = IMAGE_REPO + "@sha256:RELEASE_DIGEST_REQUIRED"
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
BINDING_NAME = "deploy-manifests-binding.json"
BINDING_KIND = "beamfall-deploy-manifests-binding/v1"
PINNED_REF_RE = re.compile(r"ghcr\.io/beamfall/core@sha256:[0-9A-Za-z_]+")
TAG_REF_RE = re.compile(r"ghcr\.io/beamfall/core:[^@\s\"'<]")

# Rendered-bundle name -> fail-closed source template. Each source template
# holds exactly one placeholder for the release image reference.
TEMPLATES: dict[str, tuple[str, str]] = {
    "compose.cpu.yml": ("packaging/compose/compose.cpu.yml", FAIL_CLOSED_IMAGE),
    "compose.nvidia.yml": ("packaging/compose/compose.nvidia.yml", FAIL_CLOSED_IMAGE),
    "compose.intel-vaapi.yml": ("packaging/compose/compose.intel-vaapi.yml", FAIL_CLOSED_IMAGE),
    "truenas-docker-compose.yaml": ("packaging/truenas/docker-compose.yaml", FAIL_CLOSED_IMAGE),
    "unraid-beamfall-core.xml": ("packaging/unraid/beamfall-core.xml", UNRAID_PLACEHOLDER),
}


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def render(digest: str, version: str, out: Path, root: Path) -> list[str]:
    errors: list[str] = []
    if not DIGEST_RE.fullmatch(digest):
        return [f"malformed image digest (want sha256:<64 lowercase hex>): {digest}"]
    pinned = f"{IMAGE_REPO}@{digest}"
    out.mkdir(parents=True, exist_ok=True)
    manifests: dict[str, dict[str, str]] = {}
    for name, (template, placeholder) in TEMPLATES.items():
        source = root / template
        try:
            content = source.read_text(encoding="utf-8")
        except OSError as exc:
            errors.append(f"{source}: cannot read source template: {exc}")
            continue
        if content.count(placeholder) != 1:
            errors.append(f"{source}: expected exactly one fail-closed image placeholder")
            continue
        rendered = content.replace(placeholder, pinned)
        (out / name).write_text(rendered, encoding="utf-8")
        manifests[name] = {
            "source": template,
            "sha256": _sha256(rendered.encode("utf-8")),
        }
    if errors:
        return errors
    binding = {
        "kind": BINDING_KIND,
        "image": pinned,
        "digest": digest,
        "version": version,
        "manifests": manifests,
    }
    (out / BINDING_NAME).write_text(
        json.dumps(binding, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    return errors


def _verify_manifest(directory: Path, name: str, entry: dict[str, str], pinned: str) -> list[str]:
    path = directory / name
    try:
        content = path.read_text(encoding="utf-8")
    except OSError as exc:
        return [f"{path}: missing rendered manifest: {exc}"]
    errors: list[str] = []
    if _sha256(content.encode("utf-8")) != entry.get("sha256"):
        errors.append(f"{path}: rendered manifest does not match its recorded binding hash")
    if FAIL_CLOSED_IMAGE in content or UNRAID_PLACEHOLDER in content:
        errors.append(f"{path}: leftover fail-closed placeholder in rendered manifest")
    if TAG_REF_RE.search(content):
        errors.append(f"{path}: mutable tag reference in rendered manifest")
    references = PINNED_REF_RE.findall(content)
    if not references:
        errors.append(f"{path}: no {IMAGE_REPO} digest reference in rendered manifest")
    for reference in references:
        if reference != pinned:
            errors.append(f"{path}: image reference is not the release digest: {reference}")
    return errors


def verify(digest: str, directory: Path) -> list[str]:
    if not DIGEST_RE.fullmatch(digest):
        return [f"malformed image digest (want sha256:<64 lowercase hex>): {digest}"]
    pinned = f"{IMAGE_REPO}@{digest}"
    binding_path = directory / BINDING_NAME
    try:
        binding = json.loads(binding_path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        return [f"{binding_path}: cannot read binding manifest: {exc}"]

    errors: list[str] = []
    if binding.get("kind") != BINDING_KIND:
        errors.append(f"{binding_path}: unexpected binding kind: {binding.get('kind')!r}")
    if binding.get("digest") != digest:
        errors.append(
            f"{binding_path}: binding digest {binding.get('digest')!r} is not the"
            f" release digest {digest}"
        )
    if binding.get("image") != pinned:
        errors.append(f"{binding_path}: binding image {binding.get('image')!r} is not {pinned}")
    manifests = binding.get("manifests")
    if not isinstance(manifests, dict) or sorted(manifests) != sorted(TEMPLATES):
        errors.append(f"{binding_path}: binding must record exactly {sorted(TEMPLATES)}")
        return errors
    for name, entry in manifests.items():
        errors += _verify_manifest(directory, name, entry, pinned)
    return errors


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    render_cmd = commands.add_parser("render", help="render digest-pinned manifests")
    render_cmd.add_argument("--digest", required=True)
    render_cmd.add_argument("--version", default="")
    render_cmd.add_argument("--out", required=True, type=Path)
    render_cmd.add_argument("--root", default=Path("."), type=Path)
    verify_cmd = commands.add_parser("verify", help="verify rendered manifests fail-closed")
    verify_cmd.add_argument("--digest", required=True)
    verify_cmd.add_argument("--dir", required=True, type=Path)
    args = parser.parse_args(argv)

    if args.command == "render":
        errors = render(args.digest, args.version, args.out, args.root)
        checked = f"rendered {len(TEMPLATES)} deployment manifests to {args.out}"
    else:
        errors = verify(args.digest, args.dir)
        checked = f"verified {len(TEMPLATES)} deployment manifests in {args.dir}"
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(f"release deploy manifests: OK ({checked})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
