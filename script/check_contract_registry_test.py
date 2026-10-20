#!/usr/bin/env python3
"""Focused tests for script/check-contract-registry.py.

Covers the ticket's four acceptance behaviors:
  (a) the checker passes the repo's real contracts/registry.json
  (b) a boundary missing owner/artifact/consumer fields reds it
  (c) an expired or malformed knownDrift entry reds it
  (d) script/release-readiness-contract.sh reports registry state
  (e) the registry must describe the newest
      published internal/conformance/wire-contract-v*.json artifact
  (f) --repo-root states where those
      artifacts live; its default is the registry's grandparent
  (g) every recorded consumer pin exactly
      matches every vendored descriptor's committed-main artifactVersion
Fixture registries live in tempdirs; the real contracts/registry.json is only
ever read, never written.
"""
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile


ROOT = pathlib.Path(__file__).resolve().parents[1]
CHECKER = ROOT / "script" / "check-contract-registry.py"
READINESS_CONTRACT = ROOT / "script" / "release-readiness-contract.sh"


def valid_registry():
    return {
        "schemaVersion": 1,
        "boundaries": [
            {
                "boundary": "test-boundary",
                "producer": "beamfall",
                "artifact": "internal/conformance/test-contract.json",
                "version": "1",
                "consumers": ["beamfall-web"],
                "conformanceTest": "test conformance",
                "knownDrift": [],
            }
        ],
    }


def write_registry(tmp, data):
    """Mirror the real layout (<root>/contracts/registry.json) so the checker
    derives <root>/internal/conformance/ for the publish-freshness rule."""
    path = tmp / "contracts" / "registry.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data), encoding="utf-8")
    return path


def file_sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def publish_wire_artifacts(tmp, versions):
    conformance = tmp / "internal" / "conformance"
    conformance.mkdir(parents=True, exist_ok=True)
    for version in versions:
        (conformance / f"wire-contract-v{version}.json").write_text("{}", encoding="utf-8")


def checker_env(workspace_root=None):
    env = os.environ.copy()
    env.pop("BEAMFALL_WORKSPACE_ROOT", None)
    if workspace_root is not None:
        env["BEAMFALL_WORKSPACE_ROOT"] = str(workspace_root)
    return env


def run_checker(registry_path, today=None, repo_root=None, workspace_root=None):
    args = ["python3", str(CHECKER), "--registry", str(registry_path)]
    if today:
        args += ["--today", today]
    if repo_root:
        args += ["--repo-root", str(repo_root)]
    return subprocess.run(
        args,
        text=True,
        capture_output=True,
        env=checker_env(workspace_root),
    )


def client_wire_boundary(data):
    return next(
        boundary
        for boundary in data["boundaries"]
        if boundary["boundary"] == "client-core-wire"
    )


def write_workspace_manifest(workspace, consumers, status="canonical"):
    workspace.mkdir(parents=True, exist_ok=True)
    repositories = [
        {"name": consumer["repo"], "status": status}
        for consumer in consumers
        if consumer.get("repo")
    ]
    (workspace / "workspace-repos.json").write_text(
        json.dumps({"repositories": repositories}), encoding="utf-8"
    )


def git(repo, *args):
    return subprocess.run(
        ["git", "-C", str(repo), *args],
        check=True,
        capture_output=True,
        text=True,
    )


def commit_descriptor(repo, artifact, version, message="fixture descriptor"):
    path = repo / artifact
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({"artifactVersion": version}), encoding="utf-8")
    git(repo, "add", artifact)
    git(repo, "commit", "-m", message)


def seed_consumer_repo(workspace, consumer, version=None, omit=()):
    repo = workspace / consumer["repo"]
    repo.mkdir(parents=True, exist_ok=True)
    git(repo, "init", "-q", "-b", "main")
    git(repo, "config", "user.email", "contract-fixture@example.invalid")
    git(repo, "config", "user.name", "Contract Fixture")
    committed = False
    for artifact in consumer["artifacts"]:
        if artifact not in omit:
            commit_descriptor(
                repo,
                artifact,
                version or consumer["pinnedVersion"],
                f"add {artifact}",
            )
            committed = True
    if not committed:
        git(repo, "commit", "--allow-empty", "-m", "empty fixture")
    return repo


def seed_workspace(workspace, consumers, versions=None, consumer_artifacts=None):
    write_workspace_manifest(workspace, consumers)
    versions = versions or {}
    consumer_artifacts = consumer_artifacts or {}
    for consumer in consumers:
        fixture_consumer = {
            **consumer,
            "artifacts": consumer.get("artifacts", consumer_artifacts.get(consumer["repo"], [])),
        }
        seed_consumer_repo(workspace, fixture_consumer, versions.get(consumer["repo"]))


def test_checker_passes_current_repo_registry():
    real_registry = ROOT / "contracts" / "registry.json"
    data = json.loads(real_registry.read_text(encoding="utf-8"))
    with tempfile.TemporaryDirectory() as d:
        workspace = pathlib.Path(d)
        boundary = client_wire_boundary(data)
        seed_workspace(
            workspace,
            boundary["consumers"],
            consumer_artifacts=boundary["consumerArtifacts"],
        )
        proc = run_checker(real_registry, workspace_root=workspace)
        assert proc.returncode == 0, proc.stderr
        assert f"contract registry ok: {len(data['boundaries'])} boundaries" in proc.stdout


def test_missing_producer_artifact_consumers_red():
    for missing_key, expect in (
        ("producer", "missing producer"),
        ("artifact", "missing artifact"),
        ("consumers", "consumers must not be empty"),
    ):
        with tempfile.TemporaryDirectory() as d:
            data = valid_registry()
            del data["boundaries"][0][missing_key]
            path = write_registry(pathlib.Path(d), data)
            proc = run_checker(path)
            assert proc.returncode == 1, f"{missing_key}: expected red, got {proc.stdout}"
            assert expect in proc.stderr, f"{missing_key}: {proc.stderr!r}"


def test_expired_known_drift_reds():
    with tempfile.TemporaryDirectory() as d:
        data = valid_registry()
        data["boundaries"][0]["knownDrift"] = [
            {"ticket": "OLD-1", "deadline": "2020-01-01", "reason": "stale waiver"}
        ]
        path = write_registry(pathlib.Path(d), data)
        proc = run_checker(path, today="2026-07-07")
        assert proc.returncode == 1, proc.stdout
        assert "OLD-1 expired on 2020-01-01" in proc.stderr


def test_malformed_known_drift_deadline_reds():
    with tempfile.TemporaryDirectory() as d:
        data = valid_registry()
        data["boundaries"][0]["knownDrift"] = [
            {"ticket": "BAD-1", "deadline": "not-a-date", "reason": "malformed"}
        ]
        path = write_registry(pathlib.Path(d), data)
        proc = run_checker(path)
        assert proc.returncode == 1, proc.stdout
        assert "BAD-1 deadline must be YYYY-MM-DD" in proc.stderr


def wire_registry(consumers, version="1.1.0"):
    data = valid_registry()
    pins = []
    consumer_artifacts = {}
    for consumer in consumers:
        if not isinstance(consumer, dict):
            pins.append(consumer)
            continue
        pin = dict(consumer)
        artifacts = pin.pop("artifacts", None)
        pins.append(pin)
        repo = pin.get("repo")
        if repo is not None:
            consumer_artifacts[repo] = artifacts or ["wire.json"]
    data["boundaries"][0] = {
        "boundary": "client-core-wire",
        "producer": "beamfall",
        "artifact": f"internal/conformance/wire-contract-v{version}.json",
        "version": version,
        "activeArtifactVersion": version,
        "consumers": pins,
        "consumerArtifacts": consumer_artifacts,
        "conformanceTest": "consumer wire drift checks",
        "knownDrift": [],
    }
    return data


def write_wire_case(tmp, consumers, version="1.1.0", published=None):
    """Registry declaring `version`, alongside the `published` artifact set."""
    path = write_registry(tmp, wire_registry(consumers, version=version))
    publish_wire_artifacts(tmp, published if published is not None else [version])
    return path


VALID_PINS = [
    {"repo": "beamfall-web", "pinnedVersion": "1.0.0", "artifacts": ["wire.json"]},
    {"repo": "beamfall-apple", "pinnedVersion": "1.1.0", "artifacts": ["wire.json"]},
]


def test_client_core_wire_minor_lag_passes():
    with tempfile.TemporaryDirectory() as d:
        path = write_wire_case(pathlib.Path(d), VALID_PINS)
        proc = run_checker(path)
        assert proc.returncode == 0, proc.stderr


def test_major_bump_requires_coordinated_publish_and_is_documented():
    with tempfile.TemporaryDirectory() as d:
        path = write_wire_case(pathlib.Path(d), VALID_PINS, version="2.0.0")
        proc = run_checker(path)
        assert proc.returncode == 1, proc.stdout
        assert "beamfall-web pin 1.0.0 skews major from published 2.0.0" in proc.stderr
        assert "beamfall-apple pin 1.1.0 skews major from published 2.0.0" in proc.stderr
        repinned = [{**consumer, "pinnedVersion": "2.0.0"} for consumer in VALID_PINS]
        path = write_wire_case(pathlib.Path(d), repinned, version="2.0.0")
        assert run_checker(path).returncode == 0

    makefile = ROOT.joinpath("Makefile").read_text(encoding="utf-8")
    recipe = makefile.split("\npublish-wire-contract:", 1)[1].split("\n\n", 1)[0]
    note = "NOTE: before a MAJOR bump"
    assert note in recipe
    assert recipe.index(note) < recipe.index("mktemp")


def test_newer_published_artifact_than_registry_reds():
    """The publish-sync fault: v1.2.0 is published but the
    registry still declares 1.1.0 as artifact/version/activeArtifactVersion."""
    with tempfile.TemporaryDirectory() as d:
        path = write_wire_case(
            pathlib.Path(d), VALID_PINS, version="1.1.0", published=["1.0.0", "1.1.0", "1.2.0"]
        )
        proc = run_checker(path)
        assert proc.returncode == 1, proc.stdout
        for key in ("artifact", "version", "activeArtifactVersion"):
            assert f"{key} " in proc.stderr, proc.stderr
        assert "wire-contract-v1.2.0.json" in proc.stderr, proc.stderr


def test_newest_published_artifact_uses_semver_order():
    """1.10.0 is newer than 1.9.0 — string sort would pick 1.9.0 and pass."""
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        path = write_wire_case(tmp, VALID_PINS, version="1.9.0", published=["1.9.0", "1.10.0"])
        proc = run_checker(path)
        assert proc.returncode == 1, proc.stdout
        assert "wire-contract-v1.10.0.json" in proc.stderr, proc.stderr

        reconciled = write_wire_case(tmp, VALID_PINS, version="1.10.0", published=["1.9.0", "1.10.0"])
        assert run_checker(reconciled).returncode == 0


def test_registry_ahead_of_published_artifacts_reds():
    """Declaring a version that was never published is equally untrue."""
    with tempfile.TemporaryDirectory() as d:
        path = write_wire_case(pathlib.Path(d), VALID_PINS, version="1.3.0", published=["1.1.0"])
        proc = run_checker(path)
        assert proc.returncode == 1, proc.stdout
        assert "does not match newest published artifact" in proc.stderr


def test_missing_published_artifacts_reds():
    with tempfile.TemporaryDirectory() as d:
        path = write_wire_case(pathlib.Path(d), VALID_PINS, published=[])
        proc = run_checker(path)
        assert proc.returncode == 1, proc.stdout
        assert "no published internal/conformance/wire-contract-v*.json" in proc.stderr


def test_missing_published_artifacts_error_names_searched_directory():
    """when artifacts are not found,
    the error message must name the directory that was searched, not just say
    'no artifact found' with no context."""
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        path = write_wire_case(tmp, VALID_PINS, published=[])
        proc = run_checker(path)
        assert proc.returncode == 1, proc.stdout
        searched = tmp.resolve() / "internal" / "conformance"
        assert f"found in {searched}" in proc.stderr, (
            f"searched dir {searched} not in error: {proc.stderr}"
        )


def test_publish_sync_rule_is_scoped_to_client_core_wire():
    """Boundaries backed by directories/schema sets keep passing with no
    versioned artifacts on disk."""
    with tempfile.TemporaryDirectory() as d:
        path = write_registry(pathlib.Path(d), valid_registry())
        proc = run_checker(path)
        assert proc.returncode == 0, proc.stderr


def test_explicit_repo_root_validates_a_registry_read_from_elsewhere():
    """a registry pointed at from a fixture
    or vendored copy is not under <root>/contracts/, so guessing the grandparent
    globs the wrong tree and reports a spurious missing-artifact failure.
    --repo-root states the dependency instead of inferring it."""
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        publish_wire_artifacts(tmp, ["1.1.0"])
        vendored = tmp / "vendor" / "beamfall" / "copy-of-registry.json"
        vendored.parent.mkdir(parents=True, exist_ok=True)
        vendored.write_text(json.dumps(wire_registry(VALID_PINS)), encoding="utf-8")

        guessed = run_checker(vendored)
        assert guessed.returncode == 1, guessed.stdout
        assert "no published internal/conformance/wire-contract-v*.json" in guessed.stderr

        stated = run_checker(vendored, repo_root=tmp)
        assert stated.returncode == 0, stated.stderr
        assert "contract registry ok: 1 boundaries" in stated.stdout


def test_default_repo_root_is_the_registry_grandparent():
    """Existing callers pass no --repo-root, so the default must stay identical
    to naming <registry>/../.. explicitly — on both the green and red paths."""
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        for published, expect_rc in ((["1.1.0"], 0), (["1.1.0", "1.2.0"], 1)):
            path = write_wire_case(tmp, VALID_PINS, version="1.1.0", published=published)
            default = run_checker(path)
            explicit = run_checker(path, repo_root=tmp)
            assert default.returncode == expect_rc, default.stdout + default.stderr
            assert (default.returncode, default.stdout, default.stderr) == (
                explicit.returncode,
                explicit.stdout,
                explicit.stderr,
            )


def test_client_core_wire_strict_rejections():
    cases = {
        "major skew": [
            {"repo": "beamfall-web", "pinnedVersion": "2.0.0", "artifacts": ["wire.json"]}
        ],
        "duplicate repo": [
            {"repo": "beamfall-web", "pinnedVersion": "1.0.0", "artifacts": ["one.json"]},
            {"repo": "beamfall-web", "pinnedVersion": "1.0.0", "artifacts": ["two.json"]},
        ],
        "missing repo": [{"pinnedVersion": "1.0.0", "artifacts": ["wire.json"]}],
        "non-string repo": [
            {"repo": 7, "pinnedVersion": "1.0.0", "artifacts": ["wire.json"]}
        ],
        "unsafe repo": [
            {"repo": "../outside", "pinnedVersion": "1.0.0", "artifacts": ["wire.json"]}
        ],
        "missing pin": [{"repo": "beamfall-web", "artifacts": ["wire.json"]}],
        "malformed semver": [
            {"repo": "beamfall-web", "pinnedVersion": "1.0", "artifacts": ["wire.json"]}
        ],
        "negative semver": [
            {"repo": "beamfall-web", "pinnedVersion": "-1.0.0", "artifacts": ["wire.json"]}
        ],
        "zero-padded semver": [
            {"repo": "beamfall-web", "pinnedVersion": "01.0.0", "artifacts": ["wire.json"]}
        ],
        "bare string": ["beamfall-web"],
        "extra field": [
            {
                "repo": "beamfall-web",
                "pinnedVersion": "1.0.0",
                "artifacts": ["wire.json"],
                "extra": True,
            }
        ],
    }
    for label, consumers in cases.items():
        with tempfile.TemporaryDirectory() as d:
            path = write_wire_case(pathlib.Path(d), consumers)
            proc = run_checker(path)
            assert proc.returncode == 1, f"{label}: expected red, got {proc.stdout}"
            assert "pinnedVersion" in proc.stderr or "consumer" in proc.stderr, f"{label}: {proc.stderr!r}"


def test_client_core_wire_requires_artifacts_for_every_consumer():
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        data = wire_registry(VALID_PINS)
        del data["boundaries"][0]["consumerArtifacts"]["beamfall-web"]
        path = write_registry(tmp, data)
        publish_wire_artifacts(tmp, ["1.1.0"])
        proc = run_checker(path, repo_root=tmp)
        assert proc.returncode == 1, proc.stdout
        assert "consumerArtifacts missing consumers: beamfall-web" in proc.stderr


def test_malformed_consumer_artifacts_is_structured_red_in_workspace():
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        core = tmp / "beamfall"
        data = wire_registry(VALID_PINS)
        data["boundaries"][0]["consumerArtifacts"] = []
        path = write_registry(core, data)
        publish_wire_artifacts(core, ["1.1.0"])
        workspace = tmp / "workspace"
        seed_workspace(workspace, VALID_PINS)
        proc = run_checker(path, repo_root=core, workspace_root=workspace)
        assert proc.returncode == 1, proc.stdout
        assert "consumerArtifacts must be a non-empty object" in proc.stderr
        assert "Traceback" not in proc.stderr


def consumer_truth_case(tmp, artifacts=None):
    consumer = {
        "repo": "beamfall-web",
        "pinnedVersion": "1.0.0",
        "artifacts": artifacts or ["one.json", "two.json"],
    }
    core = tmp / "beamfall"
    registry = write_wire_case(core, [consumer])
    workspace = tmp / "workspace"
    write_workspace_manifest(workspace, [consumer])
    return consumer, core, registry, workspace


def test_vendored_pin_divergence_reds_then_correction_passes():
    with tempfile.TemporaryDirectory() as d:
        consumer, core, registry, workspace = consumer_truth_case(pathlib.Path(d))
        repo = seed_consumer_repo(workspace, consumer)
        divergent = consumer["artifacts"][1]
        commit_descriptor(repo, divergent, "1.1.0", "deliberate divergence")

        red = run_checker(registry, repo_root=core, workspace_root=workspace)
        assert red.returncode == 1, red.stdout
        assert "pin '1.0.0' disagrees" in red.stderr, red.stderr
        assert divergent in red.stderr, red.stderr

        commit_descriptor(repo, divergent, "1.0.0", "correct divergence")
        green = run_checker(registry, repo_root=core, workspace_root=workspace)
        assert green.returncode == 0, green.stderr
        assert "contract registry ok: 1 boundaries" in green.stdout


def test_consumer_truth_ignores_dirty_worktree_bytes():
    with tempfile.TemporaryDirectory() as d:
        consumer, core, registry, workspace = consumer_truth_case(pathlib.Path(d), ["wire.json"])
        repo = seed_consumer_repo(workspace, consumer)
        (repo / "wire.json").write_text(
            json.dumps({"artifactVersion": "9.9.9"}), encoding="utf-8"
        )
        proc = run_checker(registry, repo_root=core, workspace_root=workspace)
        assert proc.returncode == 0, proc.stderr


def test_consumer_truth_missing_repo_and_artifact_red():
    with tempfile.TemporaryDirectory() as d:
        consumer, core, registry, workspace = consumer_truth_case(pathlib.Path(d), ["wire.json"])
        missing_repo = run_checker(registry, repo_root=core, workspace_root=workspace)
        assert missing_repo.returncode == 1, missing_repo.stdout
        assert "has no readable local main" in missing_repo.stderr, missing_repo.stderr

        seed_consumer_repo(workspace, consumer, omit={"wire.json"})
        missing_artifact = run_checker(registry, repo_root=core, workspace_root=workspace)
        assert missing_artifact.returncode == 1, missing_artifact.stdout
        assert "vendored artifact wire.json is absent on main" in missing_artifact.stderr


def test_consumer_truth_malformed_descriptor_red():
    with tempfile.TemporaryDirectory() as d:
        consumer, core, registry, workspace = consumer_truth_case(pathlib.Path(d), ["wire.json"])
        repo = seed_consumer_repo(workspace, consumer)
        (repo / "wire.json").write_text("{", encoding="utf-8")
        git(repo, "add", "wire.json")
        git(repo, "commit", "-m", "malformed descriptor")
        proc = run_checker(registry, repo_root=core, workspace_root=workspace)
        assert proc.returncode == 1, proc.stdout
        assert "vendored artifact wire.json is not valid JSON" in proc.stderr


def test_consumer_truth_requires_local_main_branch():
    with tempfile.TemporaryDirectory() as d:
        consumer, core, registry, workspace = consumer_truth_case(pathlib.Path(d), ["wire.json"])
        repo = seed_consumer_repo(workspace, consumer)
        git(repo, "branch", "-m", "trunk")
        git(repo, "tag", "main")
        proc = run_checker(registry, repo_root=core, workspace_root=workspace)
        assert proc.returncode == 1, proc.stdout
        assert "has no readable local main" in proc.stderr


def test_consumer_truth_requires_canonical_manifest_entry():
    with tempfile.TemporaryDirectory() as d:
        consumer, core, registry, workspace = consumer_truth_case(pathlib.Path(d), ["wire.json"])
        write_workspace_manifest(workspace, [])
        absent = run_checker(registry, repo_root=core, workspace_root=workspace)
        assert absent.returncode == 1, absent.stdout
        assert "is absent from workspace-repos.json" in absent.stderr

        write_workspace_manifest(workspace, [consumer], status="transitional")
        seed_consumer_repo(workspace, consumer)
        proc = run_checker(registry, repo_root=core, workspace_root=workspace)
        assert proc.returncode == 1, proc.stdout
        assert "is not canonical in workspace-repos.json" in proc.stderr


def test_standalone_checker_reports_consumer_truth_not_verified():
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        registry = write_wire_case(tmp, VALID_PINS)
        proc = run_checker(registry, repo_root=tmp)
        assert proc.returncode == 0, proc.stderr
        assert "consumer pin truth NOT VERIFIED" in proc.stdout


def test_make_gate_runs_the_checker_unconditionally():
    """A checker nothing invokes is not a gate; the supervised body must run it
    unconditionally because its script suite is scope-gated."""
    gate = ROOT.joinpath("tools/gate/gate.sh").read_text(encoding="utf-8")
    invocation = 'run_step "contract registry check" python3 script/check-contract-registry.py'
    assert gate.count(invocation) == 1, gate


def test_release_readiness_reports_registry_state():
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        data = valid_registry()
        good = write_registry(tmp, data)
        pass_proc = subprocess.run(
            [str(READINESS_CONTRACT), str(good)], text=True, capture_output=True
        )
        assert "-- contract registry --" in pass_proc.stdout
        assert "PASS contract registry" in pass_proc.stdout

        bad_data = data
        del bad_data["boundaries"][0]["producer"]
        bad = tmp / "bad-registry.json"
        bad.write_text(json.dumps(bad_data), encoding="utf-8")
        fail_proc = subprocess.run(
            [str(READINESS_CONTRACT), str(bad)], text=True, capture_output=True
        )
        assert "-- contract registry --" in fail_proc.stdout
        assert "FAIL contract registry" in fail_proc.stdout


def test_wrapper_forwards_its_own_repo_root_for_a_foreign_registry_path():
    """the wrapper always forwards --repo-root
    as its own repo root (where internal/conformance/ actually lives), regardless
    of where the registry argument points. Before the fix, the wrapper derived
    --repo-root from the registry path's own grandparent; a registry read from a
    foreign location (fixture/vendored copy) then globbed the wrong tree and
    reported a spurious 'no published internal/conformance/wire-contract-v*.json
    artifact found' failure even though the real artifacts exist. This regression
    calls the wrapper with a single foreign-registry argument -- no second
    repo-root argument -- and proves that trap is gone."""
    real_registry = ROOT / "contracts" / "registry.json"
    data = json.loads(real_registry.read_text(encoding="utf-8"))
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        workspace = tmp / "workspace"
        boundary = client_wire_boundary(data)
        seed_workspace(
            workspace,
            boundary["consumers"],
            consumer_artifacts=boundary["consumerArtifacts"],
        )
        foreign = tmp / "test-fixtures" / "client-core-wire" / "copy-of-registry.json"
        foreign.parent.mkdir(parents=True, exist_ok=True)
        foreign.write_text(json.dumps(data), encoding="utf-8")

        proc = subprocess.run(
            [str(READINESS_CONTRACT), str(foreign)],
            text=True,
            capture_output=True,
            env=checker_env(workspace),
        )
        assert proc.returncode == 0, proc.stderr
        assert "no published internal/conformance/wire-contract-v*.json" not in proc.stderr
        assert "PASS contract registry" in proc.stdout
        assert f"contract registry ok: {len(data['boundaries'])} boundaries" in proc.stdout


def test_wrapper_default_behavior_is_unchanged():
    """for the conventional
    <root>/contracts/registry.json layout, the wrapper's own repo root and the
    checker's default (registry-grandparent) inference are the same directory,
    so calling the wrapper with just the real registry must produce output
    byte-identical to invoking the checker directly with no --repo-root at all --
    compared exactly, not by substring."""
    real_registry = ROOT / "contracts" / "registry.json"
    data = json.loads(real_registry.read_text(encoding="utf-8"))
    with tempfile.TemporaryDirectory() as d:
        workspace = pathlib.Path(d)
        boundary = client_wire_boundary(data)
        seed_workspace(
            workspace,
            boundary["consumers"],
            consumer_artifacts=boundary["consumerArtifacts"],
        )
        wrapper_proc = subprocess.run(
            [str(READINESS_CONTRACT), str(real_registry)],
            text=True,
            capture_output=True,
            env=checker_env(workspace),
        )
        direct_proc = run_checker(real_registry, workspace_root=workspace)

        assert wrapper_proc.returncode == 0
        assert direct_proc.returncode == 0
        assert wrapper_proc.stdout == (
            f"-- contract registry --\n{direct_proc.stdout}PASS contract registry\n"
        )
        assert wrapper_proc.stderr == direct_proc.stderr


def test_explicit_repo_root_must_be_existing_directory():
    """A missing or non-directory --repo-root fails early and names the bad
    root instead of silently producing the generic missing-artifact message."""
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        path = write_registry(tmp, valid_registry())
        regular_file = tmp / "not-a-directory"
        regular_file.write_text("", encoding="utf-8")
        for bad_root in (tmp / "missing", regular_file):
            proc = run_checker(path, repo_root=bad_root)
            assert proc.returncode == 1, f"{bad_root}: expected failure, got {proc.stdout}"
            assert str(bad_root) in proc.stderr, (
                f"bad root path {bad_root!r} not in error: {proc.stderr}"
            )
            assert "not an existing directory" in proc.stderr, proc.stderr
            assert "no published" not in proc.stderr, proc.stderr


if __name__ == "__main__":
    test_checker_passes_current_repo_registry()
    test_missing_producer_artifact_consumers_red()
    test_expired_known_drift_reds()
    test_malformed_known_drift_deadline_reds()
    test_client_core_wire_minor_lag_passes()
    test_major_bump_requires_coordinated_publish_and_is_documented()
    test_newer_published_artifact_than_registry_reds()
    test_newest_published_artifact_uses_semver_order()
    test_registry_ahead_of_published_artifacts_reds()
    test_missing_published_artifacts_reds()
    test_missing_published_artifacts_error_names_searched_directory()
    test_publish_sync_rule_is_scoped_to_client_core_wire()
    test_explicit_repo_root_validates_a_registry_read_from_elsewhere()
    test_default_repo_root_is_the_registry_grandparent()
    test_client_core_wire_strict_rejections()
    test_client_core_wire_requires_artifacts_for_every_consumer()
    test_malformed_consumer_artifacts_is_structured_red_in_workspace()
    test_vendored_pin_divergence_reds_then_correction_passes()
    test_consumer_truth_ignores_dirty_worktree_bytes()
    test_consumer_truth_missing_repo_and_artifact_red()
    test_consumer_truth_malformed_descriptor_red()
    test_consumer_truth_requires_local_main_branch()
    test_consumer_truth_requires_canonical_manifest_entry()
    test_standalone_checker_reports_consumer_truth_not_verified()
    test_make_gate_runs_the_checker_unconditionally()
    test_release_readiness_reports_registry_state()
    test_wrapper_forwards_its_own_repo_root_for_a_foreign_registry_path()
    test_wrapper_default_behavior_is_unchanged()
    test_explicit_repo_root_must_be_existing_directory()
    print("ok")
