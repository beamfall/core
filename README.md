<p align="center">
  <img src=".github/brand/beamfall-logo.png" alt="Beamfall" width="96" height="96">
</p>

# Beamfall Core

**A self-hosted media server for the library you already own.** Beamfall Core scans your films
and shows, music, podcasts, audiobooks, photos, family archives, and karaoke tracks, and streams them to your
browser or a Beamfall app: direct play first, and transcoding only when a device needs it.

It is free and open source under [AGPL-3.0](LICENSE). It needs no account, and it has no
telemetry.

> **Status: pre-release.** Core runs end to end today: it scans, identifies, organizes, and
> streams. No release is tagged yet, so for now [build it from source](#from-source).

## Why Beamfall Core

- **Direct play first.** When a device can play a file as-is, Core streams it untouched. When it
  can't, Core transcodes to HLS with hardware acceleration: Intel Quick Sync and VA-API, NVIDIA
  NVENC, or Apple VideoToolbox.
- **One server for every kind of library.** Films & TV, music, podcasts, audiobooks, photos,
  family archives, and karaoke each get their own browse experience. That experience comes from plugins that
  ship with the server.
- **Private by construction.**
  - No telemetry, and no Beamfall account.
  - The update check is opt-in, and it sends nothing about your server.
  - Remote access stays off until you turn it on. It works through direct HTTPS with an ACME
    certificate, or through an outbound connection to a relay you host yourself.
- **Mistakes are recoverable.** Deletes go to a server-side Trash that is on by default, and you
  can restore from it. `beamfall backup` writes consistent SQLite snapshots, and `beamfall restore`
  brings them back.
- **Built for a household.** Each Profile gets its own libraries, rating ceilings, stream limit,
  transcode permission, and audio and subtitle language preferences. The server enforces them,
  not the client.
- **Organized the way you think.**
  - Collections, Tags, Markers, saved filters, and smart collections.
  - Full-text search and duplicate detection.
  - Skip detection for intros, recaps, and credits.
  - Scrubbing previews.
  - Watch folders that pick up new files as they arrive.
- **Bring your history with you.** The Migration Wizard imports from:
  - Plex, Emby, Jellyfin, Trakt, and NFO sidecars (films and TV);
  - Last.fm, ListenBrainz, and Navidrome (music);
  - OPML, Overcast, and AntennaPod (podcasts);
  - Audiobookshelf (audiobooks);
  - Immich (photos).
- **Built to be integrated.** The versioned `/v1` Developer API
  ([OpenAPI](docs/api/openapi.json)) comes with:
  - scoped API keys;
  - a server-sent event stream;
  - webhooks;
  - [home-automation recipes](docs/api/home-automation.md).
- **Fast at real scale.** The query layer runs against a benchmark library of 100,000 media items,
  10,000 entities, and about 2 million relations. Each benchmarked query has a P95 latency budget. For
  example, "recently added" must answer within 1 ms. `make bench` enforces these budgets, and it
  blocks releases.
- **Engineered to be trusted.**
  - The gate runs conformance tests for architecture, vocabulary, goldens, schema freeze, and the
    wire contract.
  - The release pipeline builds cosign-signed container images for amd64 and arm64, with SBOM and
    provenance attestations.
  - The [Verification Claims Register](docs/security/CLAIMS.md) records exactly what has been
    checked, and at which revision.

## Watch it anywhere

Core serves its own **Web UI** at `/web/`. It covers first-run setup, the Beamfall Console for
administration, library management, and a browser player. The Web UI lives in this repository
under the same free license.

The native Beamfall apps, including Apple TV and Mac, are separate, proprietary products. They
connect to Core over its network API. Core never needs them to run.

## Get started

### Docker Compose

The reference deployment lives in [`packaging/compose`](packaging/compose/README.md).

1. Pick the overlay that matches your transcode hardware:
   - CPU-only;
   - Intel VA-API/QSV;
   - NVIDIA.
2. Optionally, layer on Caddy for public HTTPS.
3. Start it:

   ```sh
   cd packaging/compose
   BEAMFALL_IMAGE=ghcr.io/beamfall/core@sha256:<digest-from-release> \
   BEAMFALL_MEDIA_DIR=/srv/media \
     docker compose -f compose.cpu.yml up -d
   ```

4. Open `http://127.0.0.1:8800/` and finish setup in the Beamfall Console.

The templates accept only a digest-pinned image, so you run exactly what the release signed.
[Unraid](packaging/unraid/beamfall-core.xml) and [TrueNAS](packaging/truenas/README.md) templates
are included.

### From source

You need:

- Go, at the version in [`go.mod`](go.mod), which is the version authority;
- Node.js and npm, to build the embedded Web UI;
- FFmpeg on `PATH`. [`NOTICE`](NOTICE) names the FFmpeg build the container image uses.

Then run:

```sh
cp .env.example .env
make dev-run
```

The server listens on `http://127.0.0.1:8090` and keeps its data in `/tmp/beamfall-core-dev`.

For a container smoke run:

```sh
docker compose -f docker-compose.dev.yml up --build
```

Use `.env` for development only. Staging should use production-shaped hosts with disposable data.
Production secrets come from the host secret manager.

## Build / test

```sh
make gate     # everything that must be green before a change ships
make orient   # a one-page map of the codebase
make bench    # the 100k-item latency budgets and performance floors (release blocker)
```

`make gate` runs:

- gofmt and `go vet`;
- the build;
- the Go test suite, including the architecture, vocabulary, and golden conformance tests;
- the Web UI checks: app-dist freshness, typecheck, and Vitest.

The Web UI source is `internal/web/app`. Its committed production bundle is
`internal/web/app-dist`, which `npm --prefix internal/web/app run build` generates and the
server embeds at `/web/`.

## How it's built

Beamfall Core is a from-scratch Go modular monolith: `cmd/beamfall` plus
`internal/<capability>`. See [`docs/architecture/ARCHITECTURE.md`](docs/architecture/ARCHITECTURE.md).
It is one binary with SQLite storage and FFmpeg for media work, so there is no database server to
run.

- **The core is domain-agnostic.** It serves playable Media organized by Folders, Tags, Markers,
  and Collections.
- **Plugins bring the domain.** They supply vocabulary, browse sections, artwork slots, and
  navigation against small, fixed vocabularies. They never add core schema types or client
  rendering code.
- **Seven first-party plugin bundles are compiled into the binary:** film/TV, music, podcasts,
  photos, audiobook, family archive, and karaoke. Operators can activate or deactivate each one.
  This release does not load third-party plugin modules at runtime.
- **Two APIs.** Clients use a hand-rolled GraphQL contract. Integrations use the stable `/v1`
  Developer API.

> Domain language: [CONTEXT.md](CONTEXT.md). This codebase has **no lineage** to any predecessor
> server; it is a from-scratch implementation. Plugins are built on the public
> [plugin SDK](https://github.com/beamfall/plugin-sdk).

The Core Web UI is separate from the Beamfall website, which owns pricing, Beamfall Accounts,
checkout, and device-link approval. Core never handles accounts or payments.

## Documentation

Start at [`docs/README.md`](docs/README.md). It has task-oriented paths for:

- operation and recovery;
- API integration;
- architecture;
- plugin development;
- design contracts;
- contribution.

Bounded, revision-specific checks live in the
[Verification Claims Register](docs/security/CLAIMS.md).

## Contributing and security

Read [`CONTRIBUTING.md`](CONTRIBUTING.md) and the [`CLA`](CLA.md) before opening a change. This
project follows a [Code of Conduct](CODE_OF_CONDUCT.md). Report vulnerabilities privately, as
[`SECURITY.md`](SECURITY.md) describes, never in a public issue.

## License

[AGPL-3.0](LICENSE). The server and the embedded Web UI are free software. The Beamfall native
apps are separate, proprietary products that talk to Core over its network API.

- Third-party notices are in [`NOTICE`](NOTICE).
- Use of the Beamfall name and logo is covered by [`TRADEMARKS.md`](TRADEMARKS.md).
- The project is published under the **`github.com/beamfall`** organization
  (`github.com/beamfall/core`).
