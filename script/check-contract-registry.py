#!/usr/bin/env python3
import argparse
import datetime as dt
import json
import os
import pathlib
import subprocess
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


def _wire_consumer_pin_failures(boundary):
    """Producer-truth rules for client-core-wire (ADR-0122 Amendment A §1):
    each consumer is an object with a unique repo, a strict SemVer
    pinnedVersion sharing the producer's major. Minor lag is valid; malformed,
    duplicate, missing, major-skewed, or bare-string pins fail."""
    name = boundary.get("boundary", "")
    published = _semver(boundary.get("version"))
    if published is None:
        return [f"{name}: version must be MAJOR.MINOR.PATCH"]
    failures = []
    seen = set()
    for consumer in boundary.get("consumers", []):
        if not isinstance(consumer, dict):
            failures.append(f"{name}: consumer must be an object with repo + pinnedVersion")
            continue
        unknown = set(consumer) - {"repo", "pinnedVersion"}
        if unknown:
            failures.append(f"{name}: consumer has unknown fields: {', '.join(sorted(unknown))}")
        repo = consumer.get("repo", "")
        pin = consumer.get("pinnedVersion", "")
        if not _valid_repo_name(repo):
            failures.append(
                f"{name}: consumer repo {repo!r} must be a safe non-empty repository name"
            )
        elif repo in seen:
            failures.append(f"{name}: consumer repo {repo} duplicated")
        else:
            seen.add(repo)
        pinned = _semver(pin)
        if pinned is None:
            failures.append(f"{name}: consumer {repo or '<missing>'} pinnedVersion {pin!r} must be MAJOR.MINOR.PATCH")
        elif pinned[0] != published[0]:
            failures.append(f"{name}: consumer {repo} pin {pin} skews major from published {boundary.get('version')}")
    return failures


def _valid_repo_path(value):
    return (
        isinstance(value, str)
        and bool(value)
        and not value.startswith("/")
        and "\\" not in value
        and ":" not in value
        and all(part not in ("", ".", "..") for part in value.split("/"))
    )


def _valid_repo_name(value):
    return (
        isinstance(value, str)
        and value not in ("", ".", "..")
        and all(char.isascii() and (char.isalnum() or char in "._-") for char in value)
    )


def _wire_consumer_artifact_failures(boundary):
    name = boundary.get("boundary", "")
    consumer_artifacts = boundary.get("consumerArtifacts")
    if not isinstance(consumer_artifacts, dict) or not consumer_artifacts:
        return [f"{name}: consumerArtifacts must be a non-empty object"]

    repos = {
        consumer.get("repo")
        for consumer in boundary.get("consumers", [])
        if isinstance(consumer, dict) and _valid_repo_name(consumer.get("repo"))
    }
    failures = []
    if set(consumer_artifacts) != repos:
        missing = sorted(repos - set(consumer_artifacts))
        unknown = sorted(set(consumer_artifacts) - repos)
        if missing:
            failures.append(f"{name}: consumerArtifacts missing consumers: {', '.join(missing)}")
        if unknown:
            failures.append(f"{name}: consumerArtifacts has unknown consumers: {', '.join(unknown)}")
    for repo, artifacts in consumer_artifacts.items():
        if not isinstance(artifacts, list) or not artifacts:
            failures.append(f"{name}: consumer {repo} artifacts must be a non-empty array")
            continue
        if all(isinstance(artifact, str) for artifact in artifacts) and len(set(artifacts)) != len(artifacts):
            failures.append(f"{name}: consumer {repo} artifacts must be unique")
        for artifact in artifacts:
            if not _valid_repo_path(artifact):
                failures.append(
                    f"{name}: consumer {repo} artifact {artifact!r} must be a repo-relative path"
                )
    return failures


def _workspace_root(repo_root):
    override = os.environ.get("BEAMFALL_WORKSPACE_ROOT")
    if override:
        root = pathlib.Path(override).resolve()
        if not (root / "workspace-repos.json").is_file():
            raise ValueError(
                f"BEAMFALL_WORKSPACE_ROOT {override!r} has no workspace-repos.json"
            )
        return root
    for candidate in (repo_root, *repo_root.parents):
        if (candidate / "workspace-repos.json").is_file():
            return candidate
    return None


def _git(repo, *args):
    completed = subprocess.run(
        ["git", "-C", str(repo), *args],
        capture_output=True,
        check=False,
    )
    if completed.returncode != 0:
        detail = completed.stderr.decode("utf-8", errors="replace").strip()
        raise ValueError(detail.splitlines()[0] if detail else "git command failed")
    return completed.stdout


def _wire_consumer_truth_failures(boundary, workspace):
    """Cross-check recorded pins against consumer descriptors committed on main."""
    name = boundary.get("boundary", "")
    consumer_artifacts = boundary.get("consumerArtifacts")
    if not isinstance(consumer_artifacts, dict):
        return []
    try:
        manifest = json.loads((workspace / "workspace-repos.json").read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        return [f"{name}: cannot read workspace-repos.json: {exc}"]
    if not isinstance(manifest, dict) or not isinstance(manifest.get("repositories"), list):
        return [f"{name}: workspace-repos.json repositories must be an array"]

    entries = {}
    for entry in manifest.get("repositories", []):
        if not isinstance(entry, dict) or not isinstance(entry.get("name"), str):
            continue
        repo = entry["name"]
        if repo in entries:
            return [f"{name}: workspace-repos.json duplicates repo {repo}"]
        entries[repo] = entry

    failures = []
    for consumer in boundary.get("consumers", []):
        if not isinstance(consumer, dict):
            continue
        repo = consumer.get("repo")
        artifacts = consumer_artifacts.get(repo)
        if not _valid_repo_name(repo):
            continue
        if not isinstance(artifacts, list) or not artifacts:
            continue
        if not all(_valid_repo_path(artifact) for artifact in artifacts):
            continue

        entry = entries.get(repo)
        if entry is None:
            failures.append(f"{name}: consumer {repo} is absent from workspace-repos.json")
            continue
        if entry.get("status") != "canonical":
            failures.append(
                f"{name}: consumer {repo} is not canonical in workspace-repos.json"
            )
            continue

        repo_dir = workspace / repo
        try:
            revision = _git(
                repo_dir, "rev-parse", "--verify", "refs/heads/main^{commit}"
            ).decode().strip()
        except ValueError as exc:
            failures.append(f"{name}: consumer {repo} has no readable local main: {exc}")
            continue

        for artifact in artifacts:
            try:
                raw = _git(repo_dir, "show", f"{revision}:{artifact}")
            except ValueError as exc:
                failures.append(
                    f"{name}: consumer {repo} vendored artifact {artifact} is absent on main: {exc}"
                )
                continue
            try:
                descriptor = json.loads(raw.decode("utf-8"))
            except (UnicodeDecodeError, json.JSONDecodeError) as exc:
                failures.append(
                    f"{name}: consumer {repo} vendored artifact {artifact} is not valid JSON: {exc}"
                )
                continue
            vendored = descriptor.get("artifactVersion")
            pinned = consumer.get("pinnedVersion")
            if vendored != pinned:
                failures.append(
                    f"{name}: consumer {repo} pin {pinned!r} disagrees with "
                    f"vendored {artifact} artifactVersion {vendored!r}"
                )
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
    parser.add_argument("--today", default=dt.date.today().isoformat())
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
    try:
        today = dt.date.fromisoformat(args.today)
    except ValueError:
        print("--today must be YYYY-MM-DD", file=sys.stderr)
        return 1
    try:
        workspace = _workspace_root(repo_root)
    except ValueError as exc:
        workspace = None
        failures.append(str(exc))
    consumer_truth_not_verified = workspace is None and not failures

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
        if name == "client-core-wire":
            failures.extend(_wire_consumer_pin_failures(boundary))
            failures.extend(_wire_consumer_artifact_failures(boundary))
            failures.extend(_wire_publish_sync_failures(boundary, repo_root))
            if workspace is not None:
                failures.extend(_wire_consumer_truth_failures(boundary, workspace))
        drift_records = boundary.get("knownDrift", [])
        if not isinstance(drift_records, list):
            failures.append(f"{name}: knownDrift must be an array")
            drift_records = []
        for drift in drift_records:
            if not isinstance(drift, dict):
                failures.append(f"{name}: knownDrift entry must be an object")
                continue
            ticket = drift.get("ticket", "")
            deadline = drift.get("deadline", "")
            if not ticket:
                failures.append(f"{name}: knownDrift missing ticket")
            try:
                expires = dt.date.fromisoformat(deadline)
            except ValueError:
                failures.append(f"{name}: knownDrift {ticket or '<missing>'} deadline must be YYYY-MM-DD")
                continue
            if expires < today:
                failures.append(f"{name}: knownDrift {ticket} expired on {deadline}")

    if not boundaries:
        failures.append("boundaries must not be empty")

    if failures:
        print("contract registry failed:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1

    if consumer_truth_not_verified:
        print("contract consumer pin truth NOT VERIFIED: workspace-repos.json not found")
    print(f"contract registry ok: {len(boundaries)} boundaries")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
