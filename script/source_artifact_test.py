#!/usr/bin/env python3
import json
import pathlib
import shutil
import subprocess
import tarfile
import tempfile


ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "script" / "source-artifact.py"


def run(*args):
    subprocess.run([str(SCRIPT), *args], check=True)


def test_write_and_check_roundtrip():
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        repo = tmp / "repo"
        dist = tmp / "dist"
        work = dist / "beamfall-core_v1_stable_linux_amd64"
        repo.mkdir()
        dist.mkdir()
        work.mkdir()
        shutil.copy(ROOT / "go.mod", repo / "go.mod")
        binary = work / "beamfall"
        binary.write_bytes(b"binary\n")
        out = work / "source.json"
        sidecar = dist / "beamfall-core_v1_stable_linux_amd64.source.json"
        run(
            "write",
            "--root", str(repo),
            "--binary", str(binary),
            "--out", str(out),
            "--variant", "beamfall-core_v1_stable_linux_amd64",
            "--version", "v1",
            "--channel", "stable",
            "--git-tag", "v1",
            "--git-commit", "abc123",
            "--build-date", "2026-06-27T00:00:00Z",
            "--goos", "linux",
            "--goarch", "amd64",
            "--offer-url", "https://github.com/beamfall/core/releases/download/v1/beamfall-core_v1_stable_linux_amd64.source.json",
        )
        shutil.copy(out, sidecar)
        with tarfile.open(dist / "beamfall-core_v1_stable_linux_amd64.tar.gz", "w:gz") as tar:
            tar.add(work, arcname=work.name)
        run("check", str(dist))
        doc = json.loads(sidecar.read_text(encoding="utf-8"))
        assert doc["sourceSnapshot"]["goMod"]["contents"]
        assert "goWork" not in doc["sourceSnapshot"]
        assert doc["sourceSnapshot"]["requirements"]


def test_check_rejects_missing_offer():
    with tempfile.TemporaryDirectory() as d:
        tmp = pathlib.Path(d)
        dist = tmp / "dist"
        work = dist / "beamfall-core_v1_stable_linux_amd64"
        dist.mkdir()
        work.mkdir()
        (work / "beamfall").write_bytes(b"binary\n")
        doc = {
            "variant": work.name,
            "binary": {"sha256": "bad"},
            "correspondingSourceOfferURL": "",
            "sourceSnapshot": {"goMod": {"contents": "x"}},
        }
        (work / "source.json").write_text(json.dumps(doc), encoding="utf-8")
        (dist / f"{work.name}.source.json").write_text(json.dumps(doc), encoding="utf-8")
        with tarfile.open(dist / f"{work.name}.tar.gz", "w:gz") as tar:
            tar.add(work, arcname=work.name)
        proc = subprocess.run([str(SCRIPT), "check", str(dist)], text=True, stderr=subprocess.PIPE)
        assert proc.returncode != 0


if __name__ == "__main__":
    test_write_and_check_roundtrip()
    test_check_rejects_missing_offer()
