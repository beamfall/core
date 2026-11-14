#!/usr/bin/env python3
"""Negative controls for script/consumer-pin-freshness.py.

Each control builds a THROWAWAY workspace whose consumer repos are synthesised
from the real producer artifacts, so every fixture starts fresh by construction
and the perturbation is the only variable. Real sibling checkouts are never
touched -- planting a red in a shared checkout would corrupt another lane.

The point of this file is that the check is observed RED once per artifact. A
freshness check that only ever passes is exactly the blind spot the check exists
to remove, so "it passed" is never evidence on its own.
"""

from __future__ import annotations

import json
import os
import runpy
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

CORE = Path(__file__).resolve().parent.parent
CHECK = CORE / "script" / "consumer-pin-freshness.py"
REGISTRY = json.loads(
    (CORE / "script" / "consumer-pin-obligations.json").read_text(encoding="utf-8")
)
WORKSPACE_MANIFEST = "workspace-repos.json"

FAILURES: list[str] = []
FIXTURE_CONSUMERS = {
    "apple-ui-wire-contract-pin": "beamfall-apple-ui",
    "apple-core-goldens": "beamfall-apple",
    "apple-core-corpus": "beamfall-apple",
}
FIXTURE_CLASSES = {
    "beamfall-apple": "client-shell",
    "beamfall-apple-ui": "client-library",
}


def consumer_name(obligation: dict) -> str:
    return FIXTURE_CONSUMERS[obligation["id"]]


def build_workspace(root: Path) -> None:
    """A fresh-by-construction workspace: every vendored copy taken from core."""
    repos: dict[str, dict] = {}
    for obligation in REGISTRY["obligations"]:
        name = consumer_name(obligation)
        entry = repos.setdefault(
            name,
            {
                "name": name,
                "class": FIXTURE_CLASSES[name],
                "coreContractObligations": [],
            },
        )
        entry["coreContractObligations"].append(obligation["id"])
    repos["unrelated-absent-shell"] = {
        "name": "unrelated-absent-shell",
        "class": "client-shell",
    }
    (root / WORKSPACE_MANIFEST).write_text(
        json.dumps(
            {
                "schemaVersion": 1,
                "repositories": [repos[name] for name in sorted(repos)],
            }
        ),
        encoding="utf-8",
    )
    for obligation in REGISTRY["obligations"]:
        consumer = root / consumer_name(obligation)
        probe = consumer / obligation["consumerProbe"]
        probe.parent.mkdir(parents=True, exist_ok=True)
        probe.write_text(obligation["assertedBy"] + "\n", encoding="utf-8")
        vendor_byte_copies(obligation, consumer)
        vendor_digest_files(obligation, consumer)
        vendor_digest_manifest(obligation, consumer)


def vendor_byte_copies(obligation: dict, consumer: Path) -> None:
    for pair in obligation.get("byteCopies", []):
        destination = consumer / pair["consumer"]
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(CORE / pair["producer"], destination)


def vendor_digest_manifest(obligation: dict, consumer: Path) -> None:
    spec = obligation.get("digestManifest")
    if not spec:
        return
    producer_dir = CORE / spec["producerDir"]
    digests = {key: sha256_of(producer_dir / key) for key in spec["keys"]}
    destination = consumer / spec["consumer"]
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(render_digests(spec["format"], digests), encoding="utf-8")


def vendor_digest_files(obligation: dict, consumer: Path) -> None:
    spec = obligation.get("digestManifest")
    if not spec:
        return
    destination_dir = consumer / Path(spec["consumer"]).parent
    for key in spec["keys"]:
        destination = destination_dir / key
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(CORE / spec["producerDir"] / key, destination)


def render_digests(fmt: str, digests: dict[str, str]) -> str:
    if fmt == "json-map":
        return json.dumps(digests, indent=2, sort_keys=True) + "\n"
    return "".join(f"{digest}  {name}\n" for name, digest in digests.items())


def sha256_of(path: Path) -> str:
    import hashlib

    return hashlib.sha256(path.read_bytes()).hexdigest()


def run_check(root: Path, *args: str, require_consumers: bool = False):
    env = {
        "PATH": "/usr/bin:/bin:/usr/sbin:/sbin",
        "HOME": str(root),
        "BEAMFALL_WORKSPACE_ROOT": str(root),
    }
    if require_consumers:
        env["BEAMFALL_REQUIRE_CONSUMERS"] = "1"
    return subprocess.run(
        [sys.executable, str(CHECK), *args],
        capture_output=True,
        text=True,
        env=env,
        check=False,
    )


def expect(label: str, condition: bool, detail: str = "") -> None:
    if condition:
        print(f"ok   - {label}")
        return
    FAILURES.append(label)
    print(f"FAIL - {label}")
    if detail:
        print("       " + detail.replace("\n", "\n       "))


def expect_red(label: str, completed, *needles: str) -> None:
    output = completed.stdout + completed.stderr
    missing = [needle for needle in needles if needle not in output]
    expect(
        label,
        completed.returncode == 1 and not missing,
        f"rc={completed.returncode} missing={missing}\n{output}",
    )


def obligation_by_id(obligation_id: str) -> dict:
    for obligation in REGISTRY["obligations"]:
        if obligation["id"] == obligation_id:
            return obligation
    raise SystemExit(f"test fixture drift: no obligation {obligation_id}")


def control_all_current(root: Path) -> None:
    build_workspace(root)
    completed = run_check(root, "--all")
    expect(
        "all three artifacts current -> PASS",
        completed.returncode == 0 and "PASS" in completed.stdout,
        completed.stdout + completed.stderr,
    )


def control_corpus_registry_covers_all_files(root: Path) -> None:
    obligation = obligation_by_id("apple-core-corpus")
    spec = obligation["digestManifest"]
    producer_prefix = spec["producerDir"] + "/"
    consumer_prefix = str(Path(spec["consumer"]).parent) + "/"
    producer_keys = {
        pair["producer"].removeprefix(producer_prefix)
        for pair in obligation["byteCopies"]
    }
    consumer_keys = {
        pair["consumer"].removeprefix(consumer_prefix)
        for pair in obligation["byteCopies"]
    }
    expected = set(spec["keys"])
    expect(
        "all twelve corpus manifest entries have byte-copy comparisons",
        len(expected) == 12 and producer_keys == expected and consumer_keys == expected,
        f"expected={sorted(expected)} producer={sorted(producer_keys)} "
        f"consumer={sorted(consumer_keys)}",
    )


def control_stale_byte_pin(root: Path) -> None:
    build_workspace(root)
    obligation = obligation_by_id("apple-ui-wire-contract-pin")
    consumer = consumer_name(obligation)
    pair = obligation["byteCopies"][0]
    target = root / consumer / pair["consumer"]
    target.write_bytes(target.read_bytes() + b"\n")
    expect_red(
        "stale apple-ui byte pin -> RED naming repo, artifact and refresh",
        run_check(root, "--all"),
        consumer,
        pair["consumer"],
        "make regen-wire-goldens",
    )


def control_stale_goldens_digest(root: Path) -> None:
    build_workspace(root)
    obligation = obligation_by_id("apple-core-goldens")
    spec = obligation["digestManifest"]
    target = root / consumer_name(obligation) / spec["consumer"]
    stale = target.read_text(encoding="utf-8").replace(
        sha256_of(CORE / spec["producerDir"] / spec["keys"][0]), "0" * 64
    )
    target.write_text(stale, encoding="utf-8")
    expect_red(
        "stale core-goldens.sha256 -> RED naming the sync script",
        run_check(root, "--all"),
        "core-goldens.sha256",
        "sync_core_goldens.sh",
    )


def control_malformed_goldens_manifest(root: Path) -> None:
    obligation = obligation_by_id("apple-core-goldens")
    spec = obligation["digestManifest"]
    cases = {
        "uppercase digest": f"{'A' * 64}  {spec['keys'][0]}\n",
        "normalized duplicate": (
            f"{'a' * 64}  {spec['keys'][0]}\n"
            f"{'b' * 64}  ./{spec['keys'][0]}\n"
        ),
        "traversal": f"{'a' * 64}  ../{spec['keys'][0]}\n",
        "unsafe separator": f"{'a' * 64}  dir\\{spec['keys'][0]}\n",
    }
    for label, content in cases.items():
        build_workspace(root)
        target = root / consumer_name(obligation) / spec["consumer"]
        target.write_text(content, encoding="utf-8")
        expect_red(
            f"malformed core-goldens manifest ({label}) -> RED",
            run_check(root, "--all"),
            "unparseable digest manifest",
        )


def control_stale_golden_bytes(root: Path) -> None:
    build_workspace(root)
    obligation = obligation_by_id("apple-core-goldens")
    pair = obligation["byteCopies"][0]
    target = root / consumer_name(obligation) / pair["consumer"]
    target.write_bytes(b"{}\n")
    expect_red(
        "stale vendored golden bytes -> RED",
        run_check(root, "--all"),
        pair["consumer"],
        "byte drift",
    )


def control_stale_corpus_entry(root: Path) -> None:
    build_workspace(root)
    obligation = obligation_by_id("apple-core-corpus")
    spec = obligation["digestManifest"]
    target = root / consumer_name(obligation) / spec["consumer"]
    digests = json.loads(target.read_text(encoding="utf-8"))
    digests[spec["keys"][0]] = "1" * 64
    target.write_text(json.dumps(digests, indent=2, sort_keys=True), encoding="utf-8")
    expect_red(
        "one stale corpus hash -> RED naming the corpus manifest and the manual path",
        run_check(root, "--all"),
        "corpus.manifest.json",
        spec["keys"][0],
        "NO SYNC SCRIPT EXISTS",
    )


def control_stale_corpus_bytes(root: Path) -> None:
    build_workspace(root)
    obligation = obligation_by_id("apple-core-corpus")
    consumer = consumer_name(obligation)
    spec = obligation["digestManifest"]
    relative = Path(spec["consumer"]).parent / spec["keys"][0]
    target = root / consumer / relative
    target.write_bytes(target.read_bytes() + b"\n")
    expect_red(
        "stale vendored corpus bytes -> RED naming repo, file and manual refresh",
        run_check(root, "--all"),
        consumer,
        str(relative),
        "byte drift",
        "NO SYNC SCRIPT EXISTS",
    )


def control_absent_consumer(root: Path) -> None:
    build_workspace(root)
    absent = consumer_name(obligation_by_id("apple-core-corpus"))
    shutil.rmtree(root / absent)
    completed = run_check(root, "--all")
    expect(
        "absent consumer repo -> SKIPPED, never reported as a pass",
        completed.returncode == 0
        and "SKIPPED" in completed.stdout
        and "INCOMPLETE" in completed.stdout
        and "PASS" not in completed.stdout,
        completed.stdout + completed.stderr,
    )
    expect_red(
        "absent consumer repo under BEAMFALL_REQUIRE_CONSUMERS=1 -> RED",
        run_check(root, "--all", require_consumers=True),
        "SKIPPED",
    )


def control_no_workspace_manifest(root: Path) -> None:
    build_workspace(root)
    (root / WORKSPACE_MANIFEST).unlink()
    completed = run_check(root, "--all")
    expect(
        "no workspace manifest -> everything SKIPPED, never a pass",
        completed.returncode == 0
        and "INCOMPLETE" in completed.stdout
        and "NOTHING was verified" in completed.stdout
        and "PASS" not in completed.stdout,
        completed.stdout + completed.stderr,
    )
    expect_red(
        "no workspace manifest under BEAMFALL_REQUIRE_CONSUMERS=1 -> RED",
        run_check(root, "--all", require_consumers=True),
        "NOTHING was verified",
    )


def control_linked_worktree_outside_workspace(root: Path) -> None:
    """CORE-1473: `gate run` strips BEAMFALL_WORKSPACE_ROOT, so a linked
    worktree outside the workspace finds it through the shared .git."""
    workspace = root / "workspace"
    main_checkout = workspace / "beamfall"
    main_checkout.mkdir(parents=True)
    (workspace / WORKSPACE_MANIFEST).write_text('{"repositories":[]}\n', encoding="utf-8")
    git(main_checkout, "init", "-q")
    commit_file(main_checkout, "README", "base")
    outside = root / "elsewhere" / "core-wt"
    git(main_checkout, "worktree", "add", "-q", "--detach", str(outside))
    module = runpy.run_path(str(CHECK), run_name="consumer_pin_freshness")
    saved = os.environ.pop("BEAMFALL_WORKSPACE_ROOT", None)
    try:
        found = module["workspace_root"](outside)
        loose = root / "loose"
        loose.mkdir()
        none_found = module["workspace_root"](loose)
    finally:
        if saved is not None:
            os.environ["BEAMFALL_WORKSPACE_ROOT"] = saved
    expect(
        "linked worktree outside the workspace resolves it via the git common dir",
        found is not None and found.resolve() == workspace.resolve(),
        f"found={found}",
    )
    expect("a non-git directory with no manifest above it resolves to None", none_found is None,
           f"found={none_found}")


def control_unknown_consumer_repo(root: Path) -> None:
    build_workspace(root)
    manifest = root / WORKSPACE_MANIFEST
    kept = json.loads(manifest.read_text(encoding="utf-8"))
    dropped = kept["repositories"].pop(0)["name"]
    manifest.write_text(json.dumps(kept), encoding="utf-8")
    expect_red(
        "consumer repo missing from the workspace manifest -> RED, not skipped",
        run_check(root, "--all"),
        dropped,
        WORKSPACE_MANIFEST,
    )


def control_manifest_drives_consumer_name(root: Path) -> None:
    build_workspace(root)
    obligation = obligation_by_id("apple-ui-wire-contract-pin")
    original = consumer_name(obligation)
    renamed = "renamed-consumer"
    (root / original).rename(root / renamed)
    manifest = root / WORKSPACE_MANIFEST
    data = json.loads(manifest.read_text(encoding="utf-8"))
    for repo in data["repositories"]:
        if repo["name"] == original:
            repo["name"] = renamed
    manifest.write_text(json.dumps(data), encoding="utf-8")
    completed = run_check(root, "--all")
    expect(
        "consumer identity follows workspace-repos.json, not the obligation registry",
        completed.returncode == 0
        and f"[FRESH  ] {renamed}:" in completed.stdout
        and f"[FRESH  ] {original}:" not in completed.stdout,
        completed.stdout + completed.stderr,
    )


def control_declared_owner_probe_cannot_be_masked(root: Path) -> None:
    build_workspace(root)
    obligation = obligation_by_id("apple-core-corpus")
    owner = consumer_name(obligation)
    (root / owner / obligation["consumerProbe"]).unlink()
    expect_red(
        "missing declared-owner probe -> RED despite an unrelated absent peer",
        run_check(root, "--all"),
        owner,
        obligation["consumerProbe"],
        "consumer assertion absent",
    )


def control_diff_guard(root: Path) -> None:
    build_workspace(root)
    obligation = obligation_by_id("apple-ui-wire-contract-pin")
    consumer = consumer_name(obligation)
    pair = obligation["byteCopies"][0]
    target = root / consumer / pair["consumer"]
    target.write_bytes(target.read_bytes() + b"\n")

    unrelated = root / "unrelated-change.txt"
    unrelated.write_text("docs/adr/0122-example.md\ninternal/api/router.go\n", encoding="utf-8")
    completed = run_check(root, "--changed-paths", str(unrelated))
    expect(
        "no contract source in the diff -> guard does not trip the check",
        completed.returncode == 0 and "not triggered" in completed.stdout,
        completed.stdout + completed.stderr,
    )

    contract = root / "contract-change.txt"
    contract.write_text(REGISTRY["contractSourcePrefixes"][0] + "testdata/wire-contract.json\n", encoding="utf-8")
    expect_red(
        "contract source in the diff -> guard trips and the stale artifact REDs",
        run_check(root, "--changed-paths", str(contract)),
        pair["consumer"],
    )

    for label, source in (
        ("graph source", "internal/graph/schema.graphqls"),
        ("server source", "internal/server/server.go"),
    ):
        contract.write_text(source + "\n", encoding="utf-8")
        expect_red(
            f"{label} in the diff -> guard trips and the stale artifact REDs",
            run_check(root, "--changed-paths", str(contract)),
            pair["consumer"],
        )



def git(repo: Path, *args: str) -> str:
    completed = subprocess.run(
        ["git", "-C", str(repo), "-c", "user.name=fixture", "-c", "user.email=fixture@invalid",
         "-c", "commit.gpgsign=false", *args],
        capture_output=True,
        text=True,
        check=True,
    )
    return completed.stdout.strip()


def commit_file(repo: Path, rel: str, message: str) -> None:
    path = repo / rel
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(message + "\n", encoding="utf-8")
    git(repo, "add", rel)
    git(repo, "commit", "-q", "-m", message)


def control_stale_local_main(root: Path) -> None:
    """CORE-1299: a local main behind origin/main must not widen the diff.

    The shared checkout's local main lags origin/main because lanes land by PR.
    A web-only lane cut from origin/main must see only its own change, not the
    contract-source commits origin/main gained after local main's tip.
    """
    repo = root / "producer"
    (repo / "script").mkdir(parents=True)
    for name in ("consumer-pin-freshness.py", "sha256-manifest.py", "consumer-pin-obligations.json"):
        shutil.copy2(CORE / "script" / name, repo / "script" / name)
    git(repo, "init", "-q", "-b", "main")
    git(repo, "add", "script")
    git(repo, "commit", "-q", "-m", "base")
    # origin/main gains a contract-source commit the stale local main never saw.
    git(repo, "checkout", "-q", "-b", "upstream")
    commit_file(repo, "internal/server/landed.go", "landed elsewhere by PR")
    git(repo, "update-ref", "refs/remotes/origin/main", "HEAD")
    git(repo, "checkout", "-q", "-b", "lane")
    commit_file(repo, "internal/web/app/golden.png", "web-only lane change")
    git(repo, "branch", "-q", "-D", "upstream")

    def check(**extra: str):
        env = {"PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "HOME": str(root), **extra}
        return subprocess.run(
            [sys.executable, str(repo / "script" / "consumer-pin-freshness.py")],
            capture_output=True, text=True, env=env, check=False,
        )

    default = check()
    expect(
        "stale local main: default base is origin/main, web-only lane not triggered",
        default.returncode == 0 and "not triggered" in default.stdout,
        default.stdout + default.stderr,
    )
    stale = check(BEAMFALL_PIN_FRESHNESS_BASE="main")
    expect(
        "stale local main: explicit base=main still sees main's own commits (control)",
        "not triggered" not in stale.stdout and "producer=" in stale.stdout,
        stale.stdout + stale.stderr,
    )
    git(repo, "update-ref", "-d", "refs/remotes/origin/main")
    fallback = check()
    expect(
        "no origin/main: falls back to local main and says so",
        "not triggered" not in fallback.stdout and "no origin/main ref" in fallback.stderr,
        fallback.stdout + fallback.stderr,
    )

def control_consumer_ahead_behind_matching(root: Path) -> None:
    """CORE-1442: a consumer already on a pending core publication is advisory, not stale.

    The fixture producer publishes wire-contract v0 then v1 on main; a pending core
    stack (another ref) carries v2. The consumer vendors each in turn.
    """
    repo = root / "producer"
    (repo / "script").mkdir(parents=True)
    for name in ("consumer-pin-freshness.py", "sha256-manifest.py"):
        shutil.copy2(CORE / "script" / name, repo / "script" / name)
    obligation = obligation_by_id("apple-ui-wire-contract-pin")
    (repo / "script" / "consumer-pin-obligations.json").write_text(
        json.dumps({**REGISTRY, "obligations": [obligation]}), encoding="utf-8"
    )
    artifact = obligation["byteCopies"][0]["producer"]
    git(repo, "init", "-q", "-b", "main")
    git(repo, "add", "script")
    git(repo, "commit", "-q", "-m", "tooling")
    commit_file(repo, artifact, "wire contract v0")
    commit_file(repo, artifact, "wire contract v1")
    git(repo, "update-ref", "refs/remotes/origin/main", "HEAD")
    git(repo, "checkout", "-q", "-b", "pending")
    commit_file(repo, artifact, "wire contract v2")
    git(repo, "update-ref", "refs/remotes/origin/stack/pending", "HEAD")
    pending_time = int(git(repo, "log", "-1", "--format=%ct"))
    git(repo, "checkout", "-q", "main")
    git(repo, "branch", "-q", "-D", "pending")
    git(repo, "checkout", "-q", "-b", "lane")
    commit_file(repo, "internal/server/unrelated.go", "server change that leaves the artifact alone")

    workspace = root / "workspace"
    consumer = workspace / "beamfall-apple-ui"
    (consumer / obligation["consumerProbe"]).parent.mkdir(parents=True)
    (consumer / obligation["consumerProbe"]).write_text("probe\n", encoding="utf-8")
    (workspace / WORKSPACE_MANIFEST).write_text(json.dumps({"schemaVersion": 1, "repositories": [
        {"name": "beamfall-apple-ui", "class": "client-library",
         "coreContractObligations": [obligation["id"]]}]}), encoding="utf-8")
    vendored = consumer / obligation["byteCopies"][0]["consumer"]
    vendored.parent.mkdir(parents=True, exist_ok=True)

    def check(content: str, **extra: str):
        vendored.write_text(content + "\n", encoding="utf-8")
        env = {"PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "HOME": str(root),
               "BEAMFALL_WORKSPACE_ROOT": str(workspace), **extra}
        return subprocess.run(
            [sys.executable, str(repo / "script" / "consumer-pin-freshness.py"), "--all"],
            capture_output=True, text=True, env=env, check=False,
        )

    matching = check("wire contract v1")
    expect("consumer matching this branch -> PASS",
           matching.returncode == 0 and "PASS — all 1" in matching.stdout,
           matching.stdout + matching.stderr)
    ahead = check("wire contract v2")
    expect("consumer ahead on a young pending publication, artifact unchanged -> advisory PASS",
           ahead.returncode == 0 and "[AHEAD  ]" in ahead.stdout and "ADVISORY" in ahead.stdout
           and "refs/remotes/origin/stack/pending" in ahead.stdout,
           ahead.stdout + ahead.stderr)
    expect_red("consumer ahead past the window -> RED telling the lander to land the core pin",
               check("wire contract v2", BEAMFALL_PIN_AHEAD_NOW=str(pending_time + 4 * 86400)),
               "beamfall-apple-ui", "window has been open", "land the core pin")
    expect_red("consumer ahead with the window disabled -> RED",
               check("wire contract v2", BEAMFALL_PIN_AHEAD_MAX_DAYS="0"), "byte drift")
    for bad in ("nan", "inf"):
        expect_red(f"non-finite window {bad} falls back to the default and still expires -> RED",
                   check("wire contract v2", BEAMFALL_PIN_AHEAD_MAX_DAYS=bad,
                         BEAMFALL_PIN_AHEAD_NOW=str(pending_time + 4 * 86400)),
                   "window has been open", "land the core pin")
    expect_red("consumer behind (an older core publication) -> RED",
               check("wire contract v0"), "byte drift", "make regen-wire-goldens")
    expect_red("consumer copy core never published -> RED", check("hand-edited"), "byte drift")
    # Only branches and remote-tracking refs are publications; a stray ref such as a
    # stash or scratch ref is shared across worktrees but is not (CORE-1442 review).
    git(repo, "checkout", "-q", "-b", "scratch")
    commit_file(repo, artifact, "wire contract scratch")
    git(repo, "update-ref", "refs/scratch/abandoned", "HEAD")
    git(repo, "checkout", "-q", "lane")
    git(repo, "branch", "-q", "-D", "scratch")
    expect_red("consumer matching bytes only on a non-branch ref -> RED",
               check("wire contract scratch"), "byte drift")
    commit_file(repo, artifact, "wire contract v3 from this branch")
    expect_red("branch changes the artifact while the consumer is on v2 -> RED",
               check("wire contract v2"), "byte drift")


CONTROLS = [
    control_all_current,
    control_corpus_registry_covers_all_files,
    control_stale_byte_pin,
    control_stale_goldens_digest,
    control_malformed_goldens_manifest,
    control_stale_golden_bytes,
    control_stale_corpus_entry,
    control_stale_corpus_bytes,
    control_absent_consumer,
    control_no_workspace_manifest,
    control_linked_worktree_outside_workspace,
    control_unknown_consumer_repo,
    control_manifest_drives_consumer_name,
    control_declared_owner_probe_cannot_be_masked,
    control_diff_guard,
    control_stale_local_main,
    control_consumer_ahead_behind_matching,
]


def main() -> int:
    for control in CONTROLS:
        with tempfile.TemporaryDirectory(prefix="beamfall-pin-freshness-") as tmp:
            control(Path(tmp))
    if FAILURES:
        print(f"consumer_pin_freshness_test: {len(FAILURES)} control(s) failed")
        return 1
    print(f"consumer_pin_freshness_test: {len(CONTROLS)} controls green")
    return 0


if __name__ == "__main__":
    sys.exit(main())
