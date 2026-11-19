# First-party build catalog in a Core release

A production Core knows which native client builds are first-party only from the catalog linked
into its binary at release time (ADR-0063 build provenance). A Core released without one treats
every native client (Mac Manager, iPhone and iPad, Apple TV, Android) as an unknown build and
refuses it; only the embedded web UI stays known. Every Core release path therefore requires the
catalog and fails without it.

## The manifest

The catalog is a `beamfall.first-party-builds/v1` manifest, parsed strictly by
`internal/capabilities` (`ParseFirstPartyBuildManifest`):

```json
{
  "schema": "beamfall.first-party-builds/v1",
  "builds": [
    {
      "buildID": "ios-viewer-1.4.0-412",
      "surface": "ios-viewer",
      "artifactHash": "<lowercase hex SHA-256 of the shipped artifact>",
      "sequence": 412,
      "distributions": ["appStore"],
      "roles": ["viewer"]
    }
  ],
  "minimumSequence": {"ios-viewer": 400}
}
```

- `surface`: a native surface (`macos-manager`, `ios-viewer`, `ipad-manager`, `tvos-viewer`,
  `android-viewer`); `core-web-ui` and other non-native surfaces are refused.
- `buildID`: 1-256 characters, unique across the whole manifest.
- `sequence`: the surface's positive release ordinal; `minimumSequence` is an optional rollback
  floor per surface.
- `distributions`: `direct` and/or `appStore`. `roles`: `viewer`, `manager`, `api-key`,
  `admin-token`.
- Unknown fields, an unknown schema, a malformed digest and trailing data are refused, never
  dropped. A manifest never carries enrollment bindings; those stay server-held.

## How each client pipeline contributes its record

Each client release pipeline (apple-ui, android and any other native client) writes one **build
record file** per release: a `beamfall.first-party-builds/v1` manifest holding only the builds that
pipeline ships, plus, if it owns one, the rollback floor of its own surface. It should:

1. Write the record after the artifact is final, taking `artifactHash` from the exact bytes it
   publishes, `buildID` from the value the client sends in its client contract, and a `sequence`
   that increases with every release of that surface.
2. Check the record with Core's own parser:
   `beamfall release build-catalog --manifest first-party-build.<surface>.json`.
3. Publish it with the release as `first-party-build.<surface>.json` (one file per pipeline, or
   per surface where a pipeline ships several).

## Assembling the Core release manifest

For a Core release, collect the record files of every client release that ships with it into one
directory and assemble them:

```sh
beamfall release build-catalog --assemble records/ > first-party-builds.json
```

`--assemble` reads every `*.json` file in the directory in name order (other files are ignored)
and prints the combined manifest. It refuses an empty directory, a record file that fails the
strict parser or contributes no builds, a `buildID` recorded twice (within or across files) and a
surface floor set by two files. Two pipelines claiming the same build or surface is a release
error to resolve at the source, not something the assembly picks a winner for.

## How the manifest reaches the binaries

| Path | Input | Without it |
|---|---|---|
| `script/release.sh` | `FIRST_PARTY_BUILDS=<manifest path>`; validated and linked with `build-catalog --manifest` | Fails before any build work. `BEAMFALL_ALLOW_NO_FIRST_PARTY_BUILDS=1` downgrades this to a warning for a local or test build, and is refused for a signed release. |
| `.github/workflows/release.yml` | Repository variable `FIRST_PARTY_BUILDS_MANIFEST` (the assembled manifest JSON; public build metadata, not a secret). The `attest` job validates it, passes it to `release.sh` and hands the encoded catalog to the `image` job. | Both jobs fail. |
| `docker-bake.hcl` | `FIRST_PARTY_BUILDS_CATALOG` (the output of `build-catalog --manifest`), forwarded as the Dockerfile's `BEAMFALL_FIRST_PARTY_BUILDS` | Empty is accepted only for local development images; `release.yml` refuses to bake without a catalog. |
| `Dockerfile` | `ARG BEAMFALL_FIRST_PARTY_BUILDS`; a non-empty value is validated with `build-catalog --catalog` before it is linked | A malformed value, or one with no builds, fails the image build instead of panicking the server at boot or refusing every native client. |

`build-catalog --manifest` and `--catalog` both refuse a catalog that parses but lists no builds:
a server linked with it would refuse every native client, just as an unset one does.

To release: assemble the manifest from the client records, set it as `FIRST_PARTY_BUILDS_MANIFEST`,
then push the version tag.

## Local debug builds on a running server

A debug client build is never in a release catalog: its artifact hash changes with every commit.
An operator can admit one on a running server through the runtime **dev build file** named by
`BEAMFALL_DEV_BUILDS_FILE` (the Unraid steps are in
[`packaging/unraid/README.md`](../../packaging/unraid/README.md)). It is the same
`beamfall.first-party-builds/v1` manifest, parsed with tighter rules:

- every `buildID` ends in `-dev`, the suffix only debug identities carry;
- `distributions` is exactly `["direct"]`;
- `roles` are `viewer` and/or `manager`;
- no `minimumSequence`: rollback floors stay release policy.

The server re-reads the file when it changes, logs every load with the admitted build IDs, and
adds the records to the installed catalog without replacing any record already there. Any change
advances the decision generation. Every dev record goes through the provenance check with one
exception (CORE-2573): once a device has verified a dev build with the listed artifact hash and
holds the mapping to it, that device keeps working after the build is rebuilt with a new hash.
Any other hash mismatch is still refused: a device without the mapping, a new build ID, a
malformed hash or any release record. The device's mapping outlives the record: removing the
record refuses the device until the same build ID is listed again, with any hash. To shut a device
out for good, unpair it. A missing or invalid file admits no dev builds.
Release builds and the linked catalog are unaffected; leave the variable unset in production.

Two security assumptions bound this:

- **The trust boundary is the paired device credential plus the record the operator admitted.**
  The artifact hash is a claim the client makes about itself (ADR-0063). Admitting a rebuilt
  dev build does not prove that the binary is a debug build, built from the listed commit, or
  untampered. It proves only that a device the operator paired, and that once verified the
  listed hash, is presenting that build ID again. Role, rollback floor, pinned surface and the
  exact build ID are still checked.
- **`BEAMFALL_DEV_BUILDS_FILE` turns this on for any server, production included.** Setting it
  is an operator opt-in, and nothing restricts it to development hosts. Set it only on a server
  whose paired devices you trust to run your own debug builds, and unset it everywhere else.
