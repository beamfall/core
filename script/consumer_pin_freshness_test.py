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
    control_unknown_consumer_repo,
    control_manifest_drives_consumer_name,
    control_declared_owner_probe_cannot_be_masked,
    control_diff_guard,
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
