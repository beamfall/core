#!/usr/bin/env python3
"""Focused tests for script/check-contract-registry.py.

Covers:
  (a) the checker passes the repo's real contracts/registry.json
  (b) a boundary missing owner/artifact/consumer fields reds it
  (d) script/release-readiness-contract.sh reports registry state
  (e) the registry must describe the newest
      published internal/conformance/wire-contract-v*.json artifact
  (f) --repo-root states where those
      artifacts live; its default is the registry's grandparent
  (g) CORE-0902: core's public registry holds producer facts only -- no consumer
      pins, consumerArtifacts, knownDrift records, deadlines, cloud-only rows or
      drift-carrying wire artifacts; those live in the workspace registry
Fixture registries live in tempdirs; the real contracts/registry.json is only
ever read, never written.
"""
import json
import os
import pathlib
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


def publish_wire_artifacts(tmp, versions):
    conformance = tmp / "internal" / "conformance"
    conformance.mkdir(parents=True, exist_ok=True)
    for version in versions:
        (conformance / f"wire-contract-v{version}.json").write_text("{}", encoding="utf-8")


def checker_env():
    """The checker must not depend on a workspace checkout (CORE-0902)."""
    env = os.environ.copy()
    env.pop("BEAMFALL_WORKSPACE_ROOT", None)
    return env


def run_checker(registry_path, repo_root=None):
    args = ["python3", str(CHECKER), "--registry", str(registry_path)]
    if repo_root:
        args += ["--repo-root", str(repo_root)]
    return subprocess.run(args, text=True, capture_output=True, env=checker_env())


def test_checker_passes_current_repo_registry():
    real_registry = ROOT / "contracts" / "registry.json"
    data = json.loads(real_registry.read_text(encoding="utf-8"))
    proc = run_checker(real_registry)
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


def assert_red(data, expect, tmp, published=None):
    path = write_registry(tmp, data)
    if published is not None:
        publish_wire_artifacts(tmp, published)
    proc = run_checker(path)
    assert proc.returncode == 1, f"expected red for {expect!r}, got {proc.stdout}"
    assert expect in proc.stderr, proc.stderr


def test_consumer_pin_objects_red():
    """CORE-0902: consumer pins live in the workspace registry, never in core."""
    with tempfile.TemporaryDirectory() as d:
        data = valid_registry()
        data["boundaries"][0]["consumers"] = [{"repo": "beamfall-web", "pinnedVersion": "1.0.0"}]
        assert_red(data, "test-boundary.consumers[0].pinnedVersion is consumer bookkeeping", pathlib.Path(d))
        data["boundaries"][0]["consumers"] = [{"repo": "beamfall-web"}]
        assert_red(data, "consumers[0] must be a repo name string", pathlib.Path(d))
        data["boundaries"][0]["consumers"] = ["beamfall-web", "beamfall-web"]
        assert_red(data, "consumers must not repeat a repo", pathlib.Path(d))
        data["boundaries"][0]["consumers"] = "beamfall-web"
        assert_red(data, "consumers must be an array of repo names", pathlib.Path(d))


def test_consumer_artifacts_red():
    with tempfile.TemporaryDirectory() as d:
        data = valid_registry()
        data["boundaries"][0]["consumerArtifacts"] = {"beamfall-web": ["wire.json"]}
        assert_red(data, "test-boundary.consumerArtifacts is consumer bookkeeping", pathlib.Path(d))


def test_known_drift_must_be_empty():
    """Drift records, and their deadlines, belong to the workspace registry."""
    with tempfile.TemporaryDirectory() as d:
        data = valid_registry()
        data["boundaries"][0]["knownDrift"] = [{"ticket": "WEB-1", "reason": "lag"}]
        assert_red(data, "test-boundary: knownDrift must be [] in core", pathlib.Path(d))
        data["boundaries"][0]["knownDrift"] = None
        assert_red(data, "test-boundary: knownDrift must be [] in core", pathlib.Path(d))


def test_deadline_key_anywhere_reds():
    with tempfile.TemporaryDirectory() as d:
        data = valid_registry()
        data["boundaries"][0]["knownDrift"] = [{"ticket": "WEB-1", "deadline": "2099-01-01"}]
        assert_red(data, "test-boundary.knownDrift[0].deadline is consumer bookkeeping", pathlib.Path(d))
        data = valid_registry()
        data["boundaries"][0]["notes"] = {"deadline": "2099-01-01"}
        assert_red(data, "test-boundary.notes.deadline is consumer bookkeeping", pathlib.Path(d))


def test_private_only_producer_boundary_reds():
    """A cloud-produced boundary core does not consume belongs to the workspace registry."""
    with tempfile.TemporaryDirectory() as d:
        data = valid_registry()
        data["boundaries"][0]["producer"] = "beamfall-cloud"
        data["boundaries"][0]["consumers"] = ["beamfall-web", "beamfall-apple"]
        assert_red(data, "core neither produces nor consumes this beamfall-cloud boundary", pathlib.Path(d))
        data["boundaries"][0]["consumers"] = ["beamfall"]
        assert run_checker(write_registry(pathlib.Path(d), data)).returncode == 0


def test_wire_artifact_with_known_drift_reds():
    """No published wire artifact in the public tree may carry drift records."""
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        path = write_wire_case(tmp, VALID_PINS, version="1.1.0", published=["1.0.0", "1.1.0"])
        assert run_checker(path).returncode == 0
        artifact = tmp / "internal" / "conformance" / "wire-contract-v1.0.0.json"
        artifact.write_text(json.dumps({"knownDrift": [{"ticket": "WEB-1"}]}), encoding="utf-8")
        proc = run_checker(path)
        assert proc.returncode == 1, proc.stdout
        assert "wire-contract-v1.0.0.json: knownDrift must be null" in proc.stderr, proc.stderr
        artifact.write_text(json.dumps({"knownDrift": None}), encoding="utf-8")
        assert run_checker(path).returncode == 0


def test_real_wire_artifacts_carry_no_drift():
    for artifact in sorted((ROOT / "internal" / "conformance").glob("wire-contract-v*.json")):
        data = json.loads(artifact.read_text(encoding="utf-8"))
        assert data.get("knownDrift") is None, artifact.name


def wire_registry(consumers, version="1.1.0"):
    data = valid_registry()
    data["boundaries"][0] = {
        "boundary": "client-core-wire",
        "producer": "beamfall",
        "artifact": f"internal/conformance/wire-contract-v{version}.json",
        "version": version,
        "activeArtifactVersion": version,
        "consumers": list(consumers),
        "conformanceTest": "consumer wire drift checks",
        "knownDrift": [],
    }
    return data


def write_wire_case(tmp, consumers, version="1.1.0", published=None):
    """Registry declaring `version`, alongside the `published` artifact set."""
    path = write_registry(tmp, wire_registry(consumers, version=version))
    publish_wire_artifacts(tmp, published if published is not None else [version])
    return path


VALID_PINS = ["beamfall-web", "beamfall-apple"]


def test_major_bump_note_is_documented():
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
        foreign = tmp / "test-fixtures" / "client-core-wire" / "copy-of-registry.json"
        foreign.parent.mkdir(parents=True, exist_ok=True)
        foreign.write_text(json.dumps(data), encoding="utf-8")

        proc = subprocess.run(
            [str(READINESS_CONTRACT), str(foreign)],
            text=True,
            capture_output=True,
            env=checker_env(),
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
    wrapper_proc = subprocess.run(
        [str(READINESS_CONTRACT), str(real_registry)],
        text=True,
        capture_output=True,
        env=checker_env(),
    )
    direct_proc = run_checker(real_registry)

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
    test_consumer_pin_objects_red()
    test_consumer_artifacts_red()
    test_known_drift_must_be_empty()
    test_deadline_key_anywhere_reds()
    test_private_only_producer_boundary_reds()
    test_wire_artifact_with_known_drift_reds()
    test_real_wire_artifacts_carry_no_drift()
    test_major_bump_note_is_documented()
    test_newer_published_artifact_than_registry_reds()
    test_newest_published_artifact_uses_semver_order()
    test_registry_ahead_of_published_artifacts_reds()
    test_missing_published_artifacts_reds()
    test_missing_published_artifacts_error_names_searched_directory()
    test_publish_sync_rule_is_scoped_to_client_core_wire()
    test_explicit_repo_root_validates_a_registry_read_from_elsewhere()
    test_default_repo_root_is_the_registry_grandparent()
    test_make_gate_runs_the_checker_unconditionally()
    test_release_readiness_reports_registry_state()
    test_wrapper_forwards_its_own_repo_root_for_a_foreign_registry_path()
    test_wrapper_default_behavior_is_unchanged()
    test_explicit_repo_root_must_be_existing_directory()
    print("ok")
