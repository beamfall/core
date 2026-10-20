<p align="center">
  <img src=".github/brand/beamfall-logo.png" alt="Beamfall" width="96" height="96">
</p>

# Beamfall Core

Beamfall Core is a self-hosted media server for the library you already own. It scans your films
and shows, music, podcasts, audiobooks, photos, family archives, and karaoke tracks, and streams
them to a browser or a client app: direct play when the device can handle the file, HLS
transcoding only when it can't.

It is one Go binary with embedded SQLite, using FFmpeg for media work. There is no database
server, no Beamfall account, and no telemetry. It is free software under the [AGPL-3.0](LICENSE).

> [!IMPORTANT]
> **Status: pre-release.** This repository was published on 2026-10-20 as a single curated
> commit. No version has been tagged and no release artifacts or container images have been
> published from it yet, so today the way to run Core is to [build it from source](#from-source).
> See [Project status](#project-status) for exactly what is and isn't in place.

## Features

### Playback

When a device can play a file as-is, Core streams it untouched. When it can't, Core transcodes
to HLS, probing for hardware encoders: Intel Quick Sync and VA-API, NVIDIA NVENC, and Apple
VideoToolbox.

### Library types

Seven first-party plugin bundles are compiled into the binary: film/TV, music, podcasts,
audiobooks, photos, family archive, and karaoke. Each brings its own vocabulary and browse
sections, and operators can activate or deactivate each one. The core itself stays
domain-agnostic.

### Privacy and remote access

- No telemetry and no account.
- The update check is opt-in and off by default, and it checks a signed release manifest.
- Remote access is off until you configure it: either direct HTTPS with an ACME certificate, or
  an outbound connection to a relay endpoint you run yourself.

### Trash and backups

Deletes go to a server-side Trash that is on by default. `beamfall backup` requests a
server-side backup, and `beamfall restore` restores one while the server is stopped.

### Profiles

Each Profile has its own library access, rating ceilings, concurrent stream limit, transcode
permission, and preferred audio and subtitle languages, stored and enforced on the server.

### Organizing and browsing

Collections, Tags, Markers, saved filters, and smart collections; full-text search;
byte-identical duplicate detection; intro, recap, and credits skip detection; trickplay
scrubbing previews; and watch folders that queue a scan when files change.

### Importers

The bundled plugins provide migration importers for Plex, Emby, Jellyfin, Trakt, and NFO
sidecars (film/TV); Last.fm, ListenBrainz, and Navidrome (music); OPML, Overcast, and AntennaPod
(podcasts); Audiobookshelf (audiobooks); and Immich (photos).

### Developer API

A versioned `/v1` Developer API ([OpenAPI 3.1](docs/api/openapi.json)) with scoped bearer keys,
a server-sent event stream at `/v1/events`, an HMAC-signed webhook emitter, and
[home-automation examples](docs/api/home-automation.md).

### Query latency budgets

`make bench` builds a synthetic library of 100,000 media items and 10,000 entities and asserts a
P95 budget for each query in its suite, for example 1 ms for "recently added" and 60 ms for
full-text search. The release gate runs it.

### Tests and release signing

The gate runs architecture, vocabulary, golden, and wire-contract conformance tests. The
tag-triggered release workflow is built to produce signed archives with an SBOM and SLSA
provenance, and a cosign-signed amd64/arm64 container image. The
[Verification Claims Register](docs/security/CLAIMS.md) records what has been checked, at which
revision, and what each check does *not* prove.

## Project status

As of the initial public commit:

| Area | Status |
|---|---|
| Releases | None yet. There is no `v*` tag, and [`release.yml`](.github/workflows/release.yml) runs only on such a tag, so no signed archives, deploy bundles, or `ghcr.io/beamfall/core` images have been published from this repository. |
| Server | Builds from source with the Go version in [`go.mod`](go.mod) and runs locally via `make dev-run`. |
| CI | Being brought up. At publication the [gate workflow](.github/workflows/gate.yml) failed to parse and some date-dependent waivers had expired; the fixes are in [#3](https://github.com/beamfall/core/pull/3). Treat CI as not yet green. |
| Cross-repo checks | Some checks need sibling Beamfall repositories that are not public. The consumer-pin and relay-vector freshness checks report a loud skip, never a pass, when the sibling checkout is absent, and the cross-repo wire-conformance check clones a consumer repository. These are meant to run in the maintainers' multi-repo workspace. |
| Tracked drift | [`contracts/registry.json`](contracts/registry.json) lists each contract boundary with its consumers. It records nine `knownDrift` entries across eight boundaries, each with a ticket and a deadline: for example, native client executors for the visual behavior matrix, and full plugin-SDK conformance wiring. |
| Container and NAS deployment | Templates are included, but they accept only a digest-pinned image from a release, so they become usable with the first release. |
| Supported versions | Before the first stable release, security reports should name a `main` revision ([`SECURITY.md`](SECURITY.md)). |

## Get started

### From source

You need:

- Go, at the version in [`go.mod`](go.mod), which is the version authority;
- FFmpeg on `PATH`, for probing and transcoding;
- Node.js and npm only if you change the Web UI. The production bundle in
  `internal/web/app-dist` is committed and embedded, so a plain Go build serves the UI.

Build the binary:

```sh
go build -o beamfall ./cmd/beamfall
./beamfall --help
```

Or run a development server:

```sh
make dev-run
```

`dev-run` reads an optional `.env` (start from `.env.example`), listens on
`http://127.0.0.1:8090`, keeps its data in `/tmp/beamfall-core-dev`, and sets a development-only
admin password (see the `dev-run` target in the [`Makefile`](Makefile)). Open the address and the
root redirects to the Web UI at `/web/`.

The CLI covers day-to-day operation:

| Command | What it does |
|---|---|
| `beamfall serve` | Run the server (the default command). |
| `beamfall scan` | Queue a library scan. |
| `beamfall backup` / `beamfall restore` | Request a server-side backup; restore one while the server is stopped. |
| `beamfall token` | Manage local API tokens. |
| `beamfall export-config` / `apply-config` | Export or apply a Console config document. |
| `beamfall reset-admin-password` | Set the local admin password while the server is stopped. |
| `beamfall capabilities` / `version` | Print runtime capabilities or build metadata. |

### Docker Compose (after the first release)

The reference deployment lives in [`packaging/compose`](packaging/compose/README.md), with overlays
for CPU-only, Intel VA-API/QSV, and NVIDIA transcoding, plus an optional Caddy overlay for public
HTTPS. The templates fail closed unless `BEAMFALL_IMAGE` is an immutable
`ghcr.io/beamfall/core@sha256:…` digest taken from a release, so the image you run is the one the
release signed. [Unraid](packaging/unraid/beamfall-core.xml) and [TrueNAS](packaging/truenas/README.md)
templates follow the same contract. None of them can be used until a release is published.

## Build / test

| Command | What it runs |
|---|---|
| `make build` | `go build ./...` |
| `make test` | `go test ./...` |
| `make gate-preflight` | The cheap fail-fast subset of the gate: selftests and static data lints. It is not a merge signal. |
| `make gate` | The full pre-merge gate: gofmt, golangci-lint, the Go test suite with a coverage ratchet, conformance tests, race checks, the Web UI build, typecheck, Vitest, and Playwright smoke tests, plus repository policy checks. It is long-running and needs Python 3, Node.js and npm, `golangci-lint`, and a Playwright Chromium. |
| `make bench` | The 100k-item query latency budgets plus scan, direct-play, and transcode performance floors. It is not part of `make gate`; the release gate runs it. |
| `make orient` | A one-page map of the codebase. |
| `make coverage` | The feature-coverage ledger: `testing/features.yaml` against the markers in tests. |

The Web UI source is `internal/web/app`. Its committed production bundle is
`internal/web/app-dist`, built by `npm --prefix internal/web/app run build` and embedded by the
server at `/web/`. `make web-build` runs the UI's checks and typecheck before building.

Nightly workflows for fuzzing, race detection, mutation testing, vulnerability scanning, the scale
fixture, and the UI catalog are defined in [`.github/workflows`](.github/workflows). Like the gate,
they are being brought up on this repository.

## How it's built

Beamfall Core is a from-scratch Go modular monolith: `cmd/beamfall` plus `internal/<capability>`.
See [`docs/architecture/ARCHITECTURE.md`](docs/architecture/ARCHITECTURE.md). Where that document
and the architecture test in `internal/conformance` disagree, the test wins.

- The core is domain-agnostic. It serves playable Media organized by Folders, Tags, Markers, and
  Collections. A vocabulary conformance test keeps domain words out of the core.
- The first-party domain plugins are separate public Go modules
  (`github.com/beamfall/plugin-<id>`) built on the public
  [plugin SDK](https://github.com/beamfall/plugin-sdk) and composed into the binary in
  [`cmd/beamfall/bundles.go`](cmd/beamfall/bundles.go).
- Third-party integration code runs out of process. Integration Workers run as separate processes,
  verified by hash and signing key, behind a narrow local Integration Host API that enforces a
  closed permission vocabulary.
- There are two APIs. Clients use a hand-written GraphQL contract in `internal/graph`, checked by
  conformance tests rather than generated from a schema. Integrations use the stable `/v1`
  Developer API.
- Each cross-repository wire boundary is registered in
  [`contracts/registry.json`](contracts/registry.json) with its artifact, consumers, pinned
  versions, conformance test, and any known drift.

> Domain language: [CONTEXT.md](CONTEXT.md). This codebase has no lineage to any predecessor
> server.

### Clients

Core serves its own Web UI at `/web/`: first-run setup, the Beamfall Console for
administration, library management, and a browser player. It lives in this repository under the
same license. Native Beamfall apps are separate products and are not part of this repository.
They talk to Core over its network API, and Core never needs them to run.

## Documentation

Start at [`docs/README.md`](docs/README.md). It has task-oriented paths for operation and
recovery, API integration, architecture, plugin development, design contracts, and contribution.
Revision-specific checks live in the [Verification Claims Register](docs/security/CLAIMS.md).

## Contributing and security

Read [`CONTRIBUTING.md`](CONTRIBUTING.md) and the [CLA](CLA.md) before opening a change. This
project follows a [Code of Conduct](CODE_OF_CONDUCT.md).

Report vulnerabilities privately, never in a public issue: email security@beamfall.com or use
GitHub's [private vulnerability reporting](https://github.com/beamfall/core/security/advisories/new).
[`SECURITY.md`](SECURITY.md) describes what to include and the scope.

## License

[AGPL-3.0](LICENSE). The server and the embedded Web UI are free software.

- The `LICENSE` file ends with a trademark notice under AGPL-3.0 section 7(e): the Beamfall name
  and logo are not licensed with the code. See [`TRADEMARKS.md`](TRADEMARKS.md).
- Third-party notices are in [`NOTICE`](NOTICE).
- The project is published under the `github.com/beamfall` organization
  (`github.com/beamfall/core`).
