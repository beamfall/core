#!/usr/bin/env python3
"""Validate mutation policy schema, source inventory, and ratchet history."""

from __future__ import annotations

import json
import os
from pathlib import Path
import re
import subprocess
import sys
from typing import Any, Optional


POLICY = "docs/testing/mutation-budget.json"
COUNTERS = ("measured_killed", "measured_lived", "measured_not_covered")


class PolicyError(RuntimeError):
    pass


def git(repo: Path, *args: str, check: bool = True) -> str:
    result = subprocess.run(["git", "-C", str(repo), *args], text=True, capture_output=True)
    if check and result.returncode != 0:
        raise PolicyError(result.stderr.strip() or "git command failed")
    return result.stdout


def parse_policy(data: str, *, current: bool) -> dict[str, Any]:
    try:
        policy = json.loads(data)
    except json.JSONDecodeError as error:
        raise PolicyError(f"invalid mutation policy JSON: {error}") from error
    packages = policy.get("packages")
    if not isinstance(packages, dict) or not packages:
        raise PolicyError("mutation policy packages must be a nonempty object")
    for package, budget in packages.items():
        if not isinstance(package, str) or not package.startswith("./") or not isinstance(budget, dict):
            raise PolicyError(f"invalid mutation package entry: {package!r}")
        for field in ("max_surviving", *COUNTERS):
            value = budget.get(field)
            if type(value) is not int or value < 0:
                raise PolicyError(f"{package} {field} must be a nonnegative integer")
        if budget["measured_lived"] > budget["max_surviving"]:
            raise PolicyError(f"{package} measured_lived exceeds max_surviving")
    if current:
        if not re.fullmatch(r"\d+\.\d+\.\d+", str(policy.get("tool_version", ""))):
            raise PolicyError("tool_version must be an exact semantic version")
        exclusions = policy.get("source_exclusions")
        if not isinstance(exclusions, dict) or set(exclusions) != set(packages):
            raise PolicyError("source_exclusions keys must exactly match packages")
        if not isinstance(policy.get("migrations"), list):
            raise PolicyError("migrations must be an array")
    return policy


def source_inventory(repo: Path, policy: dict[str, Any]) -> None:
    for package, entries in policy["source_exclusions"].items():
        package_dir = repo / package[2:]
        if not package_dir.is_dir():
            raise PolicyError(f"mutation package directory does not exist: {package}")
        sources = {path.name: path for path in package_dir.glob("*.go") if not path.name.endswith("_test.go")}
        if not sources:
            raise PolicyError(f"mutation package has no production Go sources: {package}")
        if not isinstance(entries, list):
            raise PolicyError(f"source_exclusions for {package} must be an array")
        excluded: set[str] = set()
        for entry in entries:
            if not isinstance(entry, dict) or set(entry) != {"path", "reason", "kind"}:
                raise PolicyError(f"invalid source exclusion for {package}: {entry!r}")
            path = entry["path"]
            if path not in sources:
                raise PolicyError(f"stale or unknown source exclusion: {package}/{path}")
            if path in excluded:
                raise PolicyError(f"duplicate source exclusion: {package}/{path}")
            if entry["kind"] not in ("build-tag", "cgo", "source") or not str(entry["reason"]).strip():
                raise PolicyError(f"invalid source exclusion metadata: {package}/{path}")
            excluded.add(path)
        for name, path in sources.items():
            text = path.read_text(encoding="utf-8")
            special = "build-tag" if "//go:build " in text.split("package", 1)[0] else "cgo" if 'import "C"' in text else ""
            if special and name not in excluded:
                raise PolicyError(f"{package}/{name} requires an explicit {special} exclusion")


def policy_at(repo: Path, commit: str) -> Optional[dict[str, Any]]:
    result = subprocess.run(
        ["git", "-C", str(repo), "show", f"{commit}:{POLICY}"], text=True, capture_output=True
    )
    if result.returncode != 0:
        return None
    return parse_policy(result.stdout, current=False)


def migrations(policy: Optional[dict[str, Any]]) -> set[tuple[str, str, str]]:
    result: set[tuple[str, str, str]] = set()
    if not policy:
        return result
    for entry in policy.get("migrations", []):
        if not isinstance(entry, dict):
            raise PolicyError(f"invalid migration: {entry!r}")
        kind = entry.get("kind")
        if kind == "rename" and set(entry) == {"kind", "from", "to"}:
            result.add((kind, entry["from"], entry["to"]))
        elif kind in ("reintroduce", "remove") and set(entry) == {"kind", "package"}:
            result.add((kind, entry["package"], entry["package"]))
        else:
            raise PolicyError(f"invalid migration: {entry!r}")
    return result


def validate_history(repo: Path, current: dict[str, Any], ref: str = "HEAD") -> None:
    if git(repo, "rev-parse", "--is-shallow-repository").strip() != "false":
        raise PolicyError("mutation policy history validation requires a full, non-shallow checkout")
    lines = git(
        repo,
        "rev-list",
        "--full-history",
        "--reverse",
        "--topo-order",
        "--parents",
        ref,
        "--",
        POLICY,
    ).splitlines()
    policies: dict[str, Optional[dict[str, Any]]] = {}
    parents: dict[str, list[str]] = {}
    seen: dict[str, set[str]] = {}
    identity_edges: list[tuple[str, str]] = []
    historical_caps: dict[str, list[int]] = {}
    for line in lines:
        commit, *commit_parents = line.split()
        parents[commit] = commit_parents
        policy = policy_at(repo, commit)
        policies[commit] = policy
        packages = set(policy.get("packages", {})) if policy else set()
        commit_seen = set(packages)
        commit_migrations = migrations(policy)
        for kind, source, destination in commit_migrations:
            if kind == "rename":
                identity_edges.append((source, destination))
        for parent in commit_parents:
            parent_policy = policies.get(parent)
            parent_packages = set(parent_policy.get("packages", {})) if parent_policy else set()
            parent_seen = seen.get(parent, set()) | parent_packages
            commit_seen |= parent_seen
            for package in packages - parent_packages:
                if package in parent_seen and ("reintroduce", package, package) not in commit_migrations:
                    raise PolicyError(f"{commit}: {package} reappeared without a reintroduce migration")
            diff = git(repo, "diff-tree", "-r", "-M", "--name-status", parent, commit, "--").splitlines()
            for row in diff:
                fields = row.split("\t")
                if len(fields) == 3 and fields[0].startswith("R"):
                    old_dir = "./" + str(Path(fields[1]).parent).replace(os.sep, "/")
                    new_dir = "./" + str(Path(fields[2]).parent).replace(os.sep, "/")
                    if old_dir in parent_packages and new_dir in packages and old_dir != new_dir:
                        if ("rename", old_dir, new_dir) not in commit_migrations:
                            raise PolicyError(f"{commit}: {old_dir} -> {new_dir} renamed without a migration")
            renamed_sources = {source for kind, source, _ in commit_migrations if kind == "rename"}
            for package in parent_packages - packages:
                if package not in renamed_sources and ("remove", package, package) not in commit_migrations:
                    raise PolicyError(f"{commit}: {package} removed without an explicit migration")
        seen[commit] = commit_seen
        if policy:
            for package, budget in policy["packages"].items():
                historical_caps.setdefault(package, []).append(budget["max_surviving"])

    ref_commit = git(repo, "rev-parse", "--verify", f"{ref}^{{commit}}").strip()
    ref_policy = policies.get(ref_commit)
    ref_packages = set(ref_policy.get("packages", {})) if ref_policy else set()
    current_packages = set(current["packages"])
    current_migrations = migrations(current)
    for package in current_packages - ref_packages:
        if package in seen.get(ref_commit, set()) and ("reintroduce", package, package) not in current_migrations:
            raise PolicyError(f"working tree: {package} reappeared without a reintroduce migration")
    for row in git(repo, "diff", "--name-status", "-M", ref, "--").splitlines():
        fields = row.split("\t")
        if len(fields) == 3 and fields[0].startswith("R"):
            old_dir = "./" + str(Path(fields[1]).parent).replace(os.sep, "/")
            new_dir = "./" + str(Path(fields[2]).parent).replace(os.sep, "/")
            if old_dir in ref_packages and new_dir in current_packages and old_dir != new_dir:
                if ("rename", old_dir, new_dir) not in current_migrations:
                    raise PolicyError(f"working tree: {old_dir} -> {new_dir} renamed without a migration")
    removed_packages = ref_packages - current_packages
    added_packages = current_packages - ref_packages
    for old_dir in removed_packages:
        old_path = old_dir[2:]
        old_files = git(repo, "ls-tree", "-r", "--name-only", ref, "--", old_path).splitlines()
        for new_dir in added_packages:
            renamed_content = False
            for old_file in old_files:
                relative = Path(old_file).relative_to(old_path)
                new_file = repo / new_dir[2:] / relative
                if not new_file.is_file():
                    continue
                old_data = subprocess.run(
                    ["git", "-C", str(repo), "show", f"{ref}:{old_file}"], capture_output=True
                )
                if old_data.returncode == 0 and old_data.stdout == new_file.read_bytes():
                    renamed_content = True
                    break
            if renamed_content and ("rename", old_dir, new_dir) not in current_migrations:
                raise PolicyError(f"working tree: {old_dir} -> {new_dir} renamed without a migration")
    renamed_sources = {source for kind, source, _ in current_migrations if kind == "rename"}
    for package in removed_packages:
        if package not in renamed_sources and ("remove", package, package) not in current_migrations:
            raise PolicyError(f"working tree: {package} removed without an explicit migration")

    groups: dict[str, set[str]] = {}
    for package in set(historical_caps) | set(current["packages"]):
        groups[package] = {package}
    for source, destination in identity_edges:
        union = groups.setdefault(source, {source}) | groups.setdefault(destination, {destination})
        for package in union:
            groups[package] = union
    for package, budget in current["packages"].items():
        caps = [cap for identity in groups.get(package, {package}) for cap in historical_caps.get(identity, [])]
        if caps and budget["max_surviving"] > min(caps):
            raise PolicyError(
                f"{package} max_surviving {budget['max_surviving']} exceeds ancestral minimum {min(caps)}"
            )
        if not caps and budget["max_surviving"] != budget["measured_lived"]:
            raise PolicyError(f"new package {package} must seed max_surviving from measured_lived")


def main() -> int:
    repo = Path(__file__).resolve().parent.parent
    try:
        current = parse_policy((repo / POLICY).read_text(encoding="utf-8"), current=True)
        source_inventory(repo, current)
        if "--schema-only" not in sys.argv[1:]:
            raw_ref = os.environ.get("GATE_HEAD_SHA", "")
            ref = raw_ref if re.fullmatch(r"[0-9a-f]{40}", raw_ref) and set(raw_ref) != {"0"} else "HEAD"
            try:
                git(repo, "rev-parse", "--verify", f"{ref}^{{commit}}")
            except PolicyError:
                ref = "HEAD"
            validate_history(repo, current, ref)
    except (OSError, PolicyError) as error:
        print(f"mutation policy: {error}", file=sys.stderr)
        return 1
    print("mutation policy: valid")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
