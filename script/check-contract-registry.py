#!/usr/bin/env python3
import argparse
import json
import pathlib
import sys


def _semver(value):
    """Parse a strict MAJOR.MINOR.PATCH triple, or None if malformed."""
    parts = value.split(".") if isinstance(value, str) else []
    if len(parts) != 3 or not all(
        part.isascii()
        and part.isdecimal()
        and (part == "0" or not part.startswith("0"))
        for part in parts
    ):
        return None
    return tuple(int(part) for part in parts)


# Boundaries the private cloud service produces for private clients. Core neither produces nor
# consumes them, so they live only in the workspace registry (CORE-0902).
PRIVATE_ONLY_PRODUCERS = frozenset({"beamfall-cloud"})
CORE_REPO = "beamfall"
FORBIDDEN_KEYS = ("pinnedVersion", "consumerArtifacts", "deadline")


def _forbidden_key_paths(value, path):
    """Yield every JSON path under value whose key is consumer bookkeeping."""
    if isinstance(value, dict):
        for key, child in value.items():
            here = f"{path}.{key}"
            if key in FORBIDDEN_KEYS:
                yield here
            yield from _forbidden_key_paths(child, here)
    elif isinstance(value, list):
        for i, child in enumerate(value):
            yield from _forbidden_key_paths(child, f"{path}[{i}]")


def _public_registry_failures(name, boundary):
    """CORE-0902 absence rules: core's public registry records producer facts only.

    Consumer pins, consumerArtifacts, drift records and deadlines belong to the private
    workspace registry, which holds and checks them (core-0902-retention_test.js)."""
    failures = [
        f"{name}: {path} is consumer bookkeeping; record it in the workspace registry"
        for path in _forbidden_key_paths(boundary, name)
    ]
    consumers = boundary.get("consumers")
    if isinstance(consumers, list):
        for i, consumer in enumerate(consumers):
            if not isinstance(consumer, str) or not consumer.strip():
                failures.append(f"{name}: consumers[{i}] must be a repo name string, got {consumer!r}")
        names = [c for c in consumers if isinstance(c, str)]
        if len(names) != len(set(names)):
            failures.append(f"{name}: consumers must not repeat a repo")
    elif consumers is not None:
        failures.append(f"{name}: consumers must be an array of repo names")
    if boundary.get("knownDrift") != []:
        failures.append(f"{name}: knownDrift must be [] in core; drift records live in the workspace registry")
    producer = boundary.get("producer")
    if producer in PRIVATE_ONLY_PRODUCERS and CORE_REPO not in (consumers or []):
        failures.append(f"{name}: core neither produces nor consumes this {producer} boundary; it belongs in the workspace registry")
    return failures


def _wire_artifact_drift_failures(repo_root):
    """No published wire-contract artifact in the public tree may carry knownDrift."""
    failures = []
    for artifact in sorted(repo_root.glob("internal/conformance/wire-contract-v*.json")):
        try:
            data = json.loads(artifact.read_text(encoding="utf-8"))
        except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
            failures.append(f"internal/conformance/{artifact.name}: unreadable: {exc}")
            continue
        if isinstance(data, dict) and data.get("knownDrift") not in (None, []):
            failures.append(f"internal/conformance/{artifact.name}: knownDrift must be null; drift records live in the workspace registry")
    return failures


def _published_wire_artifacts(repo_root):
    """Map published SemVer -> artifact path for the versioned wire contracts.

    Published artifacts live at <repo_root>/internal/conformance/wire-contract-v<semver>.json.
    The root is passed in (--repo-root) rather than inferred from the registry's
    location, so a registry read from a fixture or a vendored copy still checks
    the artifacts the caller means."""
    published = {}
    for artifact in repo_root.glob("internal/conformance/wire-contract-v*.json"):
        version = _semver(artifact.name[len("wire-contract-v") : -len(".json")])
        if version is not None:
            published[version] = artifact
    return published


def _wire_publish_sync_failures(boundary, repo_root):
    """Publish-truth rule for client-core-wire (XCONTRACT-14 pin truth): the
    registry must describe the NEWEST published artifact, so publishing
    wire-contract-v<next>.json without updating the registry fails closed.
    Newest is by SemVer ordering, not string sort (1.10.0 > 1.9.0). Only this
    boundary is backed by versioned single-file artifacts; the other boundaries
    point at directories and schema sets and are out of scope."""
    name = boundary.get("boundary", "")
    published = _published_wire_artifacts(repo_root)
    if not published:
        searched = repo_root / "internal" / "conformance"
        return [
            f"{name}: no published internal/conformance/wire-contract-v*.json "
            f"artifact found in {searched}"
        ]
    newest = max(published)
    expected_version = ".".join(str(part) for part in newest)
    expected_artifact = f"internal/conformance/{published[newest].name}"
    expected = (
        ("artifact", expected_artifact),
        ("version", expected_version),
        ("activeArtifactVersion", expected_version),
    )
    return [
        f"{name}: {key} {boundary.get(key)!r} does not match newest published artifact ({want!r})"
        for key, want in expected
        if boundary.get(key) != want
    ]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--registry", default="contracts/registry.json")
    parser.add_argument(
        "--repo-root",
        default=None,
        help="repo root holding internal/conformance/ (default: the registry's grandparent, i.e. <root>/contracts/registry.json)",
    )
    args = parser.parse_args()

    if args.repo_root is not None:
        repo_root = pathlib.Path(args.repo_root)
        if not repo_root.is_dir():
            print(f"--repo-root {args.repo_root!r} is not an existing directory", file=sys.stderr)
            return 1
        repo_root = repo_root.resolve()
    else:
        path = pathlib.Path(args.registry)
        repo_root = path.resolve().parent.parent

    failures = []
    path = pathlib.Path(args.registry)
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
        print("contract registry failed:", file=sys.stderr)
        print(f"  - REGISTRY_JSON /contracts/registry.json: {exc}", file=sys.stderr)
        return 1
    if not isinstance(data, dict):
        print("contract registry failed:", file=sys.stderr)
        print("  - REGISTRY_SHAPE /contracts/registry.json: must be an object", file=sys.stderr)
        return 1
    if data.get("schemaVersion") != 1:
        failures.append("schemaVersion must be 1")

    boundaries = data.get("boundaries")
    if not isinstance(boundaries, list):
        failures.append("REGISTRY_SHAPE /contracts/registry.json/boundaries: must be an array")
        boundaries = []
    seen = set()
    for i, boundary in enumerate(boundaries, 1):
        if not isinstance(boundary, dict):
            failures.append(f"boundary #{i} must be an object")
            continue
        name = boundary.get("boundary", "")
        if not name:
            failures.append(f"boundary #{i} missing boundary")
            continue
        if name in seen:
            failures.append(f"{name}: duplicate boundary")
        seen.add(name)
        for key in ("producer", "artifact", "version", "consumers", "conformanceTest", "knownDrift"):
            if key not in boundary:
                failures.append(f"{name}: missing {key}")
        if not boundary.get("consumers"):
            failures.append(f"{name}: consumers must not be empty")
        failures.extend(_public_registry_failures(name, boundary))
        if name == "client-core-wire":
            failures.extend(_wire_publish_sync_failures(boundary, repo_root))

    if not boundaries:
        failures.append("boundaries must not be empty")
    failures.extend(_wire_artifact_drift_failures(repo_root))

    if failures:
        print("contract registry failed:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1

    print(f"contract registry ok: {len(boundaries)} boundaries")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
