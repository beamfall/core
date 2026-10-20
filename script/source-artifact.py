#!/usr/bin/env python3
import argparse
import hashlib
import json
import pathlib
import re
import sys
import tarfile
import urllib.parse


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def file_snapshot(root, rel):
    path = root / rel
    text = path.read_text(encoding="utf-8")
    return {
        "path": rel,
        "sha256": hashlib.sha256(text.encode("utf-8")).hexdigest(),
        "contents": text,
    }


def requirements(go_mod_text):
    reqs = []
    in_block = False
    for raw in go_mod_text.splitlines():
        line = raw.strip()
        if line == "require (":
            in_block = True
            continue
        if in_block and line == ")":
            in_block = False
            continue
        if line.startswith("require "):
            line = line.removeprefix("require ").strip()
        elif not in_block:
            continue
        parts = line.split()
        if len(parts) >= 2 and re.match(r"^[\w./-]+\.[\w./-]+|^[\w.-]+/[\w./-]+", parts[0]):
            reqs.append({
                "module": parts[0],
                "version": parts[1],
                "indirect": "//" in parts and "indirect" in parts,
            })
    return reqs


def write(args):
    root = pathlib.Path(args.root).resolve()
    binary = pathlib.Path(args.binary).resolve()
    go_mod = file_snapshot(root, "go.mod")
    doc = {
        "schemaVersion": 1,
        "variant": args.variant,
        "version": args.version,
        "channel": args.channel,
        "gitTag": args.git_tag,
        "gitCommit": args.git_commit,
        "buildDate": args.build_date,
        "target": {"goos": args.goos, "goarch": args.goarch},
        "binary": {"path": "beamfall", "sha256": sha256_file(binary)},
        "correspondingSourceOfferURL": args.offer_url,
        "sourceSnapshot": {
            "goMod": go_mod,
            "requirements": requirements(go_mod["contents"]),
        },
    }
    if (root / "go.work").is_file():
        doc["sourceSnapshot"]["goWork"] = file_snapshot(root, "go.work")
    out = pathlib.Path(args.out)
    out.write_text(json.dumps(doc, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def load_json_member(tar, member):
    f = tar.extractfile(member)
    if f is None:
        raise ValueError(f"{member.name}: cannot read")
    return json.loads(f.read().decode("utf-8"))


def valid_offer(url, sidecar_name):
    parsed = urllib.parse.urlparse(url)
    return parsed.scheme == "https" and parsed.netloc and pathlib.PurePosixPath(parsed.path).name == sidecar_name


def check(args):
    dist = pathlib.Path(args.dist)
    archives = sorted(dist.glob("*.tar.gz"))
    if not archives:
        raise SystemExit(f"{dist}: no release archives found")
    for archive in archives:
        variant = archive.name.removesuffix(".tar.gz")
        sidecar_name = variant + ".source.json"
        sidecar = dist / sidecar_name
        if not sidecar.exists():
            raise SystemExit(f"{archive.name}: missing {sidecar_name}")
        sidecar_doc = json.loads(sidecar.read_text(encoding="utf-8"))
        with tarfile.open(archive, "r:gz") as tar:
            names = set(tar.getnames())
            source_member = f"{variant}/source.json"
            binary_member = f"{variant}/beamfall"
            if source_member not in names:
                raise SystemExit(f"{archive.name}: missing source.json")
            if binary_member not in names:
                raise SystemExit(f"{archive.name}: missing beamfall binary")
            embedded_doc = load_json_member(tar, tar.getmember(source_member))
            if embedded_doc != sidecar_doc:
                raise SystemExit(f"{archive.name}: embedded source.json differs from sidecar")
            binary = tar.extractfile(tar.getmember(binary_member))
            got_sha = hashlib.sha256(binary.read()).hexdigest()
        if sidecar_doc.get("variant") != variant:
            raise SystemExit(f"{sidecar_name}: variant mismatch")
        if sidecar_doc.get("binary", {}).get("sha256") != got_sha:
            raise SystemExit(f"{sidecar_name}: binary sha256 mismatch")
        if not valid_offer(sidecar_doc.get("correspondingSourceOfferURL", ""), sidecar_name):
            raise SystemExit(f"{sidecar_name}: non-resolving source offer URL")
        snapshot = sidecar_doc.get("sourceSnapshot", {})
        if not snapshot.get("goMod", {}).get("contents"):
            raise SystemExit(f"{sidecar_name}: missing go.mod snapshot")
    print(f"source-artifact: PASS {len(archives)} variant(s)")


def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="cmd", required=True)
    w = sub.add_parser("write")
    w.add_argument("--root", required=True)
    w.add_argument("--binary", required=True)
    w.add_argument("--out", required=True)
    w.add_argument("--variant", required=True)
    w.add_argument("--version", required=True)
    w.add_argument("--channel", required=True)
    w.add_argument("--git-tag", required=True)
    w.add_argument("--git-commit", required=True)
    w.add_argument("--build-date", required=True)
    w.add_argument("--goos", required=True)
    w.add_argument("--goarch", required=True)
    w.add_argument("--offer-url", required=True)
    c = sub.add_parser("check")
    c.add_argument("dist")
    args = parser.parse_args()
    if args.cmd == "write":
        write(args)
    else:
        check(args)


if __name__ == "__main__":
    main()
