# AGENTS.md — Beamfall Core

The Beamfall media server: a generic, domain-agnostic media platform written from scratch in Go,
licensed under the GNU AGPL v3. Domain behavior (film and TV, music, podcasts, photos, audiobooks,
family archives, karaoke) lives in plugins built on
[`github.com/beamfall/plugin-sdk`](https://github.com/beamfall/plugin-sdk).

> **This file is canonical.** `CLAUDE.md` is a symlink to it — edit `AGENTS.md`, never maintain a
> second copy.

Use normal repo-local CLI and shell tools.

## Layout

- `cmd/beamfall` — the server binary; `cmd/beamfall/bundles.go` lists the compiled-in plugin bundles.
- `internal/` — the modular monolith (app composition root, graph API, store and migrations,
  playback and FFmpeg, ingest, auth, backup, remote access, web UI under `internal/web/app`).
- `internal/conformance` — architecture, vocabulary, wire-contract and golden-corpus enforcement.
  If docs and tests disagree, the tests win.
- `contracts/` — cross-repository contract registry and published artifacts.
- `docs/` — architecture, API, glossary, design, operations and security docs; start at
  [`docs/README.md`](docs/README.md). Vocabulary lives in [`CONTEXT.md`](CONTEXT.md).
- `tools/gate/`, `Makefile`, `.github/workflows/` — gate and CI.

## Build and test

- `go build ./...` and `go test ./...` — Go 1.27 with `GOWORK=off`; no `replace` directives.
- `make web-build` — builds the embedded web UI (run `npm --prefix internal/web/app ci` first).
- `make gate` — the full local gate (gofmt, vet, build, tests including architecture, vocabulary and
  golden checks, plus web lint, build and tests). Some cross-repository steps expect sibling client
  checkouts and are skipped or waived when they are absent.

## Hard invariants

1. **The core platform is domain-agnostic.** Domain vocabulary lives in plugins only; the
   vocabulary firewall in `internal/conformance` enforces it.
2. **The wire entity is `Media`.** Domain names such as "Episode" or "Movie" are plugin display
   labels, never schema identifiers.
3. **Plugins contribute data against small fixed vocabularies** (archetypes, artwork slots,
   sections, sources, theme tokens). Plugins never add schema types, query strings or rendering code.
4. **Secrets live in platform secure stores or `0600` files, never in plaintext. No telemetry**
   unless explicitly opt-in and local-first.
5. **Destruction is recoverable by default.** Server-side trash ships on; all deletes route through
   audited server mutations.
6. **No entitlement, licensing or account code in the AGPL server.**
7. **Applied migrations are append-only.** Never edit files under `internal/store/migrate/`; their
   checksums are frozen and verified at startup and in tests.

## Conventions

- Format with `gofmt`; keep `go vet ./...` clean.
- Plugins are consumed as tagged module versions in `go.mod`; an uncommitted `go.work` is fine for
  local cross-repo work.
- Update `internal/conformance` goldens only through their documented regeneration commands.

## Corvint context

Before editing, read `corvint context "<task>"` or `corvint impact <path>...`; before choosing tests,
run `corvint affected --base <origin/main sha>`. **Verify:** `make gate` (or `go test ./...` for a
focused change).

## Contributing and license

- Licensed under the GNU AGPL v3 (`LICENSE`). Preserve license headers and third-party notices
  (`NOTICE`); trademark use is covered by `TRADEMARKS.md`.
- External contributions need an accepted Beamfall Contributor License Agreement first: see
  `CONTRIBUTING.md` and `CLA.md`. Report vulnerabilities as described in `SECURITY.md`, not in
  public issues.
