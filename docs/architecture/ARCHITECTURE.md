# Beamfall Core — implementation architecture (converged)

Status: **canonical**. Where this document and the arch test
(`internal/conformance`) disagree, **the test wins** (ADR-0016: claims must not silently
drift).

Governing inputs (frozen): ADR-0001…0016 plus ADR-0020 where it adds qBittorrent ingest,
CONTEXT.md vocabulary, AGENTS.md hard invariants.
Module: `github.com/beamfall/core`, single Go module, Go 1.26. School: idiomatic-Go modular
monolith (ADR-0016) — interfaces only at the four real seams, defined at the consumer; no
hexagonal layers, no repository pattern.

The GraphQL contract is hand-written Go in `internal/graph` and is checked by conformance tests.
There is no schema-codegen pipeline.

## 0. Canonical name table

The single source for every path, package, tag, and test name that any tool, skill, gate,
or suite references. **Nothing may hard-code a divergent spelling**; the arch test asserts
the names that are mechanically checkable.

| Thing | Canonical name |
|---|---|
| Storage package | `internal/store` (subpackages `migrate`, `storetest`, `foreignimport`, `seedart`, `seedmedia`) |
| Migrations | `internal/store/migrate/catalog/*.sql`, `internal/store/migrate/events/*.sql` |
| GraphQL package | `internal/graph` |
| FFmpeg boundary package | `internal/ffmpeg` |
| FFmpeg version pin | **Implemented** (V-14): `internal/ffmpeg/ffmpeg.pin`, package-local so `go:embed` works; one text file read by the Dockerfile, regen tool, golden tests, and FFmpeg integration tier. See §5.3. |
| Golden probe fixtures | `internal/ffmpeg/testdata/*.json`, inventoried by `internal/ffmpeg/testdata/probe/manifest.json` |
| Probe fixture manifest | `internal/ffmpeg/testdata/probe/manifest.json` |
| Build tags | `ffmpeg` (FFmpeg shim integration), `io`, `io_e2e`, `io_live` — no others |
| First-party Domain Plugin modules | external `github.com/beamfall/plugin-<id>` modules in sibling `beamfall-plugin-*` repositories, composed by `cmd/beamfall` (ADR-0114); Core has no domain-plugin subtree |
| Workflow Plugin package home | `internal/workflow/<id>` — compiled-in, runtime-gated seam orchestration; never a `plugin.Bundle` and never housed in a Domain Plugin module |
| Vocab-scan exemption | `conformance.VocabExemptPrefixes` is intentionally empty; domain vocabulary has zero core exemptions |
| Conformance home | `internal/conformance` (`testdata/`; narrow route and vocabulary exceptions are self-pruning inline Go maps) |
| Reveal surface registry | `internal/reveal` (stdlib-only; instance-scoped `Registry`, NO package-level state) |
| Event-fanout choke point | `internal/server` (`/api/events` and `/v1/events` SSE over the store outbox) |
| Auth/session package | `internal/auth` (owns `Session`, `Scope`) |
| Reveal-filtering types | `auth.Scope` → `store.LibraryFilter` (conversion at the store call boundary) |
| Stream tokens | `internal/streamtoken` (`Service`, `Grant`, `Target`) |
| Source spec (canonical declarative type) | `plugin.SourceSpec` + the one validator `plugin.ValidateSource` (V-12) |
| FFmpeg test substitution point | none — `app.New` constructs the real lazy `ffmpeg.Runner` |
| Byte-serving HTTP | `internal/mediahttp` |
| Job substrate | `internal/jobs` + `internal/governor` (separate leaf) |
| Composition roots | `internal/app` (`app.New`) wires Core services; `cmd/beamfall/hooks*.go` composes external plugin modules into neutral `app.Hooks` |
| Gate runner | `tools/gate/gate.sh` (= `make gate`) |
| Gate test names | `TestArch`, `TestVocab` (in `./internal/conformance`) |
| Other canonical test names | `TestEveryRouteRegistered`, `TestEveryActionHasBehaviorTest`, `TestEveryCommandRouteHasBehaviorTest`, `TestSchemaManifest`, `TestNestedSchema`, `TestWireContract`, `TestPinnedVersionAndFixtureManifest` |
| Orientation | `tools/orient/orient.sh` (= `make orient`) |

## 1. Repo top level

```
cmd/beamfall/          the one binary: serve + thin API-client subcommands
internal/             all server code (§3)
internal/web/         Core Web UI source + build output; web.go does //go:embed for /web/
internal/ioe2e/       tagged I/O E2E journeys and fast I/O checks
tools/                gate/, orient/, golden-hashes/, nested-schema/, schema-manifest/, ui-catalog/
docs/                 ADRs, this document, TESTING.md, AGENT-TOOLING.md
```

(The FFmpeg version pin lives at `internal/ffmpeg/ffmpeg.pin`, not at the repo top level,
because `go:embed` cannot reach outside the package directory; see §5.3 / V-14.)

No `pkg/`. Nothing is importable from outside the module except by forking — this is a
product, not a library.

## 2. cmd layout and the composition root

```
cmd/beamfall/
  main.go        subcommand dispatch: serve (default) | scan | backup | restore |
                 jobs | export-config | apply-config   (ADR-0013 §5)
  serve.go       thin: config.Load → app.New(ctx, cfg, logger, firstPartyBundles(), firstPartyHooks()) → app.Run(ctx)
  hooks*.go      build-tagged external plugin-module composition into neutral app.Hooks
  http_client.go online administrative subcommands = HTTP clients of the admin API
                 using an admin-scoped token; scan and backup never open the database
  backup.go      restore is offline recovery: it refuses a live server lock before
                 replacing the data directory from a verified backup manifest
```

**Resolution (composition root, HIGH):** Core service wiring lives in **`internal/app`** so the
conformance harness and route meta-tests can boot the production surface set in-process. External
plugin-module construction lives in `cmd/beamfall`: `bundles*.go` supplies declarations and
`hooks*.go` supplies compiled behavior through Core-owned neutral seams. `internal/app` may import
`plugin-sdk`, but no domain plugin module. `serve.go` remains the thin caller that passes both inputs.

```go
package app // internal/app — the one place everything meets

// New: store.Open (placement guard, backup, migrate) → capability constructors →
// plugin activation over bundles → job-handler registration → surface registration →
// server wiring. No DI container, no globals, no init() registration.
func New(ctx context.Context, cfg config.Config, logger *slog.Logger,
    bundles []plugin.Bundle, hooks ...Hooks) (*App, error)

// Zero Hooks values supports declaration-only harnesses; production passes exactly
// one firstPartyHooks() value. More than one is rejected before store initialization.

// app.New constructs the real FFmpeg runner. ffmpeg.New resolves and validates
// binaries lazily (first use), so app.New boots on machines with no FFmpeg
// present; probe/transcode calls then return the configuration error.

type App struct{ /* capability handles, unexported */ }
func (a *App) Run(ctx context.Context) error      // errgroup over everything with Run(ctx)
func (a *App) Handler() http.Handler              // the real router (conformance and E2E)
func (a *App) Surfaces() *reveal.Registry         // the populated registry (meta-tests)
func (a *App) Routes() []server.Route             // derived route table (meta-test 1)
```

Conformance and E2E helpers call `app.New` with invented-vocabulary test bundles;
production calls it with the first-party literal. One constructor, no drift.

## 3. Package map (`internal/`)

Every package traces to an ADR commitment; there are no reserve packages. (Notably absent,
on purpose: no `internal/webhooks` — webhooks/deviceBus are *deferred Core* per ADR-0002's
triage; when built they bind through the store outbox and the surface registry. No
`internal/search` — FTS lives wholly inside `store` (§5.1), the search surface is a graph
resolver over `store` typed methods. No `internal/util`, no `internal/model`, no
`internal/serving` — superseded by `internal/reveal`.)

| package | responsibility (one line) | ADR |
|---|---|---|
| `app` | Importable composition root (§2) | 0016 |
| `apptest` | In-process E2E test harness over the production composition root | 0121 |
| `artwork` | Artwork Slots, candidates, derivative buckets, fallback chain; consumer-defined `FrameGrabber` seam slice | 0003 §5, 0009 §8 |
| `auth` | Device keys + pairing codes, Device Roles, Sessions, Admin Sessions, admin API tokens, brute-force limits; computes `auth.Scope` | 0012 |
| `compat` | Jellyfin compatibility route group (`/compat/jellyfin/`): default-off toggle, CIDR-bound, relay-excluded, panic-contained; integration-session auth (JFCOMPAT-2/3) | 0171 §2.2, §2.4 |
| `backup` | Zero-knowledge off-site backup envelope and bucket-client seam | 0115 |
| `beam` | Session handoff: opaque single-use offer tokens between enrolled same-Profile sessions at canonical timeline positions; rechecks Reveal/source session on claim | 0023 §7 |
| `capabilities` | Compile/runtime capability self-report: small fixed capability vocabulary, build tags, commit metadata, and CI manifest assertion source | 0124 |
| `causation` | Integration Worker loop containment: server-global causation budget, per-worker action rate, authenticated tokens, bounded/coalesced abort feed | 0125 |
| `conductor` | Ambient Group Conductor registry and clock-drift math | 0035 §8 |
| `config` | Bootstrap config: flags > env (`BEAMFALL_*`) > TOML — ports, dirs, admin bootstrap, TLS/ACME, FFmpeg path; plus the `0600` secrets file store | 0013 §5 |
| `conformance` | Test-only: conformance suites, committed data files, and wire/route/golden checks | 0016 |
| `decodeverify` | Decode-verify sampling job service over store/jobs with a consumer-side decode seam | 0009 §9 |
| `discovery` | LAN/server discovery helpers | 0012 |
| `ffmpeg` | The ONE FFmpeg boundary: probe (`ParseProbe` pure), frame grab, sprites, subtitle extract, HLS transcode procs, hw detect, and invocation hardening | 0010, 0012 §10, 0013 §4 |
| `filmtvintegration` | ORG-52 core-side integration test only (no production code) for Film & TV migration sources now owned by the plugin module | 0028 §6, 0037 §2.1, 0114 |
| `genassets` | Derivative asset jobs: trickplay sprites, thumbnails, loudness, silence maps, and asset sweeps | 0009, 0013 |
| `governor` | Shared resource arbiter: interactive transcode slots, hw-encoder slots, background CPU budget, preemption | 0013 §1–2 |
| `graph` | Hand-written GraphQL contract: query/mutation dispatch, auth-tier checks, generic Media/Entity fields, My Data, and surface registrations | 0001, 0011 §8 |
| `graphquery` | Pure GraphQL operation selection and top-level field parsing for the hand-written graph dispatcher | 0001, 0011 §8 |
| `graphshape` | Generic JSON-shape reflection walker used by graph's nested schema artifact | 0011 §8 |
| `identify` | Identify/apply pipeline, confidence thresholds, Unmatched queue, match pinning, and per-field source precedence | 0003 §4, 0009 §8 |
| `ingest` | Subscription feed poller and enclosure ingest through the hardened scrape/scan path | 0030 §7, 0020 |
| `integrationhost` | Third-party integration worker runtime and Host API permission gate | 0058 |
| `ioe2e` | Tagged I/O E2E journey package | 0121 |
| `jobs` | Durable job substrate: job rows, queues, concurrency classes, checkpoints/resume, cancel, progress events | 0013 §1–2 |
| `jsonstate` | Atomic JSON file helper for private file-backed state stores | 0012, 0039 |
| `libencrypt` | Per-Encrypted-Library key model: DEK wraps, Sealed/Unsealed state, and provision/seal/unseal operations | 0045 §2.4 |
| `manifest` | Navigation Manifest composition (Core + active plugins, reveal-filtered, per-Profile overrides); Layout/Section/Theme-Token serving | 0001, 0003 §7 |
| `mediahttp` | Every byte-serving HTTP route: direct-play ranges, HLS, trickplay, artwork, sidecars — all token-gated, all registered into the shared reveal Registry | 0010 §3 |
| `migrate` | Migration Wizard import job handler and path-based match chain | 0037, 0028 §6, 0009 §7 |
| `moderation` | App Store Guideline 1.2 UGC substrate: file-backed report/block store plus published moderation/contact path | apple 0003 §4, apple 0004 §4 |
| `nodehost` | Origin-Node enrollment lifecycle, compatibility handshake, revoke/rotate | 0123 §5, 0063 |
| `nodehostd` | Node-facing mTLS SESSION transport for `nodehost` (node dials the main; SPKI pinned both directions) | 0129 §5, 0123 §5, 0046 §2.1 |
| `offlinepack` | Domain-agnostic Offline Pack rows, pack grants, manifests, and serve handler | 0098 |
| `pairing` | Pairing codes, enrollment flows, first-run setup token, and enrollment metadata | 0012 |
| `photoexport` | Bulk photo-export job substrate and portable manifest writer | 0045 §2.9 |
| `playback` | Capability Profiles, `playbackInfo` negotiation, playback/HLS session lifecycle, admission control, and canonical timeline | 0010 |
| `plugin` | Plugin contract + loader: data-only `Bundle`, strict-YAML parse, validation, composition, and activation state | 0003 |
| `proxytee` | Stream-token-gated proxy-tee archive promotion for provisional media | 0052 §2 |
| `push` | Push-registration seam and no-op stub | 0039 |
| `qbittorrent` | Integration plugin state and job support for the qBittorrent bridge | 0020 |
| `racefixture` | Test-only deliberately racy fixture for proving the race tier | 0121 §3 |
| `release` | Signed release manifest, trust anchor, SBOM/provenance helpers | 0013 §A |
| `remoteaccess` | Direct HTTPS/ACME and self-hosted relay carrier client status for Console | 0012 §1, 0018, 0025 |
| `reveal` | The Reveal-contract surface registry only (instance-scoped, stdlib-only); see §6 | 0004 §4, 0010 §3 |
| `scan` | Walk → fingerprint → probe → sidecar → assemble → Identification handoff; runs as jobs | 0009 |
| `schedule` | Scheduled job definitions and due-run selection | 0013 |
| `scrape` | Declarative Scraper executor and enforced outbound allowlist (SSRF) | 0003 §3, 0012 §9 |
| `server` | HTTP wiring: recording `Handle` helper, middleware, web embed mount, `/health`, `/metrics`, TLS/ACME, first-run claim, `Routes()` | 0007, 0012, 0013 §6 |
| `serverrole` | Attached/main authority role vocabulary and guard sentinel | 0123 §2 |
| `skins` | Parked Skin contrast math island: WCAG-2.2 hard-role validation only; persistence, graph, store, and conformance wiring land in SKIN-2/3 | 0125 |
| `skipdetect` | Generic skip-segment detector over markers, ordered siblings, jobs, and FFmpeg | 0048 |
| `store` | The single-writer storage gate: both DB handles, all SQL text, schema row types, typed query methods, typed IDs, `LibraryFilter`, FTS, migrations, backups, placement guard, refuse-on-newer | 0011 |
| `streamtoken` | Stream Token issue + verify (HMAC, expiry, live revocation/reveal recheck); `Grant`, `Target` | 0010 §3 |
| `updatecheck` | Opt-in, default-off version check against a signed public release manifest. No telemetry | 0013 §7 |
| `watcher` | Filesystem watch state and change observation | 0009 |
| `web` | Embedded Core Web UI static file shim | 0007 |
| `webhook` | Webhook emitter state and event-outbox adapter | 0040 §2.5 |

The cross-repo wire-contract consumer-guard work named by ARCH-3/GQL-02/CONF-4 is
tracked by XCONTRACT-1a/1b and later XCONTRACT tickets; this document records that alias
and does not duplicate those build items here.

## 4. Dependency direction — the arch-test table

`internal/conformance` (`TestArch`) consumes this table as a Go map literal (`archWhitelist`
+ `archAnythingExcept`); whitelist semantics — a package or import edge not listed is a build
failure, and `TestArchWhitelistReferencesRealPackages` fails if any row or target is not a
real `internal/<pkg>` dir.

The rows below are reconciled to the **real** packages and their **actual** import edges.
Predecessor-implied catalog/profile/activity/source/fanout package splits did not land;
those concerns are folded into `store`, `graph`, and `server`. Where this table and the
arch test ever disagree, **the test wins** — the test is the law, this table is its mirror.

| package | allowed `internal/` imports |
|---|---|
| `config` | serverrole (bootstrap config carries the ADR-0123 §2 attached/main `Role`) |
| `serverrole` | — (leaf; the ADR-0123 §2 two-face authority `Role` — `Main`/`Attached` — plus the `GuardAuthority` sentinel the catalog-row / Stream-Token / engagement seams consult; stdlib only, no internal deps) |
| `nodehost` | — (leaf; the ADR-0123 §5 Origin-Node enrollment lifecycle + the ADR-0063 origin-node Compatibility Handshake gate + per-node revoke/rotate — NODE-3; operates on real x509 mTLS leaves, stdlib only, no internal deps) |
| `nodehostd` | `nodehost` (node-facing mTLS SESSION transport, ADR-0129 §5: the node dials the main; main-side listener + node-side dialer pin SPKI in both directions. The `app` composition root prepares the cloned self-signed mTLS config + compatibility policy and passes them in, so no `remoteaccess`/`config` edge.) |
| `capabilities` | — (leaf; compile/runtime capability self-report for clients and CI manifest checks) |
| `causation` | — (leaf; ADR-0125 Integration Worker global-chain budget, per-worker action rate, authenticated causation tokens, and bounded/coalesced abort feed; stdlib only) |
| `apptest` | app, config, plugin, store (in-process E2E test harness, mirrors `store/storetest` but top-level next to `app`: boots the composition root over `httptest` for the I/O E2E track. Test-support only, never imported by production code) |
| `ioe2e` | — (I/O E2E journey package: `io_e2e`-tagged `*_test.go` journeys only — real ffmpeg ops + harness lifecycle happy-paths — plus an empty untagged `doc.go`. Test imports are arch-walk-invisible; no production imports) |
| `store` (+ subpackages) | serverrole (the only importer of the SQLite driver; imports `serverrole` for the ADR-0123 §2 attached-authority guard on catalog/engagement authors) |
| `governor` | — |
| `ffmpeg` | — |
| `reveal` | — |
| `compat` | store, auth (ADR-0171 Jellyfin compat route group; integration-session auth + store lookups only) |
| `skins` | — (leaf; user-authored color Skins, ADR-0125 — server-side WCAG-2.2 contrast math for hard-role validation; stdlib only, no internal deps. Validation/store/wire seams land in later SKIN-* tickets) |
| `push` | — (leaf; the ADR-0039 push-registration seam + no-op stub) |
| `racefixture` | — (leaf; test-only deliberately-racy RED-proving fixture for the `-race` tier, ADR-0121 §3 / TEST-CORE-1 — a `doc.go` package clause + one `*_test.go`; no production imports) |
| `discovery` | — |
| `jsonstate` | — (leaf; tiny atomic JSON file load/save helper for private file-backed state stores; stdlib only, no schema/catalog coupling) |
| `libencrypt` | — (leaf; per-Encrypted-Library key model, ADR-0045 §2.4 / ENC-2 — DEK/KEK/passphrase-KDF/Recovery-Key wraps + Sealed/Unsealed; domain-agnostic crypto over stdlib AES-GCM + x/crypto argon2id, persists wrapped blobs through a consumer-side `SecretStore` seam, no key material in any DB column) |
| `backup` | libencrypt (leaf; ZK off-site backup transport, ADR-0115 §1/§2/§4/§5 / BACKUP-1 — BBK1 envelope: per-backup ephemeral DEK wrapped under a user-held `BackupKey` via AES-256-GCM; `BucketClient` seam for any S3-compatible target; `EncryptStream`/`DecryptStream` for ciphertext; restore requires only the `BackupKey` + ciphertext, no server/account/subscription alive; AGPL core: no entitlement/billing code) |
| `conductor` | store (Ambient Group Conductor, ADR-0035 §8 / AV-4: in-memory ephemeral group registry + four-timestamp NTP and drift math; imports `store` only for its id/time types — group state is never persisted, sync signals ride the existing `/api/events` outbox) |
| `decodeverify` | store, jobs (decode-verify sampling job; FFmpeg remains a consumer-side seam, so there is no direct `ffmpeg` edge) |
| `plugin` | `capabilities` (package manifests validate capability IDs against the flat namespace) |
| `web` | — (the `go:embed` shim only) |
| `streamtoken` | store, auth (auth supplies the verifying `Scope`), serverrole (gates `Issue` on the ADR-0123 §2 / ADR-0129 §1 attached authority face — an attached Origin Node never issues) |
| `release` | — (leaf; supply-chain integrity core — signed release manifest, ed25519 sign/verify, pinned trust-anchor public key, SBOM/provenance builders; stdlib only, PANEL-11 / ADR-0013 §A) |
| `updatecheck` | release (opt-in version check, ADR-0013 §7; imports `release` to verify the signed channel manifest before blessing an update — PANEL-11) |
| `jobs` | store |
| `auth` | store |
| `beam` | store, auth |
| `pairing` | store, auth |
| `moderation` | store, jsonstate |
| `artwork` | store, jobs |
| `genassets` | store, jobs |
| `skipdetect` | store, jobs, ffmpeg (skip-segment detector, ENR-7 / ADR-0048: byte-level intro/recap/credits detection over the generic relation graph — markers + ordered-sibling walk in store, the sweep handler in jobs, chapter/fingerprint via the hardened ffmpeg seam) |
| `scan` | store, jobs, ffmpeg |
| `migrate` | store, jobs |
| `scrape` | plugin |
| `identify` | store, artwork, scrape, plugin |
| `ingest` | store, jobs, scan, scrape |
| `playback` | store, auth, ffmpeg, governor, streamtoken |
| `mediahttp` | store, artwork, genassets, playback, streamtoken, auth |
| `proxytee` | store, auth, scrape, streamtoken (ADR-0052 §2.1: Stream-Token'd proxy-tee stream + provisional→archived promotion; origin fetch through `scrape`'s SSRF gate) |
| `offlinepack` | store, auth, streamtoken — ADR-0098 area 6: the **generic, domain-agnostic** Offline Pack substrate. Owns pack rows/items, Pack-subject Stream Token grants (a `Grant.PackID`, not a fake Media subject), per-target validation through a `TargetResolver` plugin registry, manifest building (visible-only), and the `http.HandlerFunc` manifest/key serving with the dual revoke/relock/seal byte-denial check. Carries **no** music vocabulary — the Music target resolvers live in the Music plugin, registered via a core adapter. Mounted into `server` via the `SetProxyTee`-style `http.HandlerFunc` seam, so no `server → offlinepack` edge. |
| `photoexport` | store — ADR-0045 §2.9 mode 2 (extends the My Data export of ADR-0036): the **bulk photo-export** job substrate. A job-substrate job walks a Custodial Library plane-aware (the global catalog, or a per-Library encrypted DB when Unsealed) and emits every **byte-exact** original plus a portable, Takeout-style sidecar manifest (album membership, EXIF, Location, captions). Read-only, free (§2.7). Encrypted-Library originals are recovered from at-rest ciphertext through a composition-wired `ByteDecryptor` seam (the same `libencrypt` path the media byte route uses); a Sealed Library is absent until Unsealed. Domain-agnostic (invariant 1) — "album membership" is a generic `(relation, kind, title)` tuple. Wired into the job registry by composition (like `offlinepack.HandleBuildJob`), so no `server → photoexport` edge. The export-time step-up authorization gate is PANEL-18, not this package. |
| `qbittorrent` | store, jobs, jsonstate |
| `integrationhost` | causation, store, plugin — ADR-0058/0125 third-party Integration Worker runtime: verifies native worker artifacts by hash/signing key, launches them out-of-process behind a narrow local Integration Host API (UDS, fd3 capability-token handshake, rlimit/Pdeathsig hardening), enforces the closed permission vocabulary at method granularity, and admits mutating calls through the shared `causation` leaf before delegating. Domain-agnostic; imports `store` (catalog/event/secret/audit row types) and `plugin` (the closed permission vocabulary it gates on). Reveal-filtered catalog reads, event tails, jobs, ingest, and file-mutation requests reach Core through consumer-side seams the composition root adapts, so no direct edge to those packages. |
| `webhook` | store, jsonstate |
| `watcher` | store |
| `schedule` | store |
| `manifest` | store, plugin, auth |
| `graphquery` | — |
| `graphshape` | — |
| `graph` | any capability above (incl. store, graphquery); never server, app |
| `server` | auth, beam, manifest, moderation, pairing, mediahttp, playback, reveal, store, web |
| `app` | anything except conformance |
| `conformance` | anything (it is the enforcement layer) |
| `cmd/beamfall` | anything (sole importer of `plugin/firstparty/**`) |

**Integration Worker mutation-seam promotion rules (ADR-0125).** These are four independent
dependency rules, so promoting one field cannot rely on another field's review or wiring:

| `integrationhost.Deps` field | Promotion rule before the field may be non-nil |
|---|---|
| `Jobs` | The same `Deps` carries a non-nil `*causation.CausationCap`; `Host.EnqueueJob` admits a Core-issued token before invoking the seam; every descendant outbox event retains that token. |
| `Ingest` | The same `Deps` carries a non-nil `*causation.CausationCap`; `Host.RequestIngest` admits a Core-issued token before invoking the seam; every descendant outbox event retains that token. |
| `FileMut` | The same `Deps` carries a non-nil `*causation.CausationCap`; `Host.RequestFileMutation` admits a Core-issued token before invoking the seam; every descendant outbox event retains that token. |
| `Outbound` | The same `Deps` carries a non-nil `*causation.CausationCap`; `Host.RequestOutbound` admits a Core-issued token before invoking the seam; any resulting callback/outbox event retains that token. |

An empty, forged, expired, or omitted token denies before the mutation adapter. The cap instance is
Core-owned and shared across worker Hosts and worker-process restarts; constructing one cap per worker
is a dependency-rule violation even though each individual field check would compile.

### 4.1 Core Web UI boundary

Beamfall Core ships the backend-served **Core Web UI** from `internal/web/app` (built into
`internal/web/app-dist`, embedded by `internal/web/web.go`, and mounted by `internal/server`
at `/web/`). The product surface is **Beamfall Console**: `/setup` for first-run setup,
`/console` for server-shaped administration, `/manage` for browser Manager/content
operations, and Viewer routes for browser playback.

This repo owns local server management. The separate `beamfall-web` product owns public
marketing, pricing, Beamfall Accounts, checkout entry, hosted flows, and device-link
approval. No hosted account, Stripe, entitlement, email, token-minting, checkout, or hosted
device-code backend logic belongs in Beamfall Core.

**Compilation scope (V-9):** `TestArch` loads **`./...`** via `go/packages` under the
**default build-tag set** and evaluates **non-test compilation units only** — `_test.go`
files and tag-gated I/O packages fall outside the whitelist by construction, so the table
above covers every package the test actually loads. The cross-cutting greppable scans
(rules 2 and 6 below) run over the same default-tag, non-test scope.

Cross-cutting assertions in the same test:

1. **Driver/dep confinement.** Only `store` imports the SQLite driver. Only `server` imports prometheus; only `remoteaccess` imports autocert
   (the ACME/cert machinery, ADR-0012 §1; `app` wires its TLS config + ACME challenge onto the
   listener). Only `config` imports the
   TOML parser. Only `plugin` imports the YAML parser. (When the FFmpeg landlock shim lands,
   go-landlock will be confined to `ffmpeg` the same way — not yet implemented; see §4.2.)
   Capabilities expose plain `Stats()` snapshot structs; `server` adapts them to Prometheus.
2. **One exec site.** Only `internal/ffmpeg` and `tools/` import `os/exec` within the
   scanned (default-tag, non-test) scope; tagged I/O tests sit outside it by tag.
3. **No transport leaks.** Nothing imports `graph` or `server` except `server`/`app`/`cmd`
   (+ `conformance`/tagged I/O tests).
4. **Plugin firewall.** Domain plugins are external `github.com/beamfall/plugin-*` modules;
   Core has no domain-plugin subtree and `conformance.VocabExemptPrefixes` is empty. The
   composition root imports the bundles explicitly, while the vocabulary gate scans all
   Core production Go without a domain-path exemption.
5. **Generated-code fence.** `internal/graph/generated/**` imported only by `graph`.
6. **Mux confinement.** `http.ServeMux.Handle`/`HandleFunc` calls appear **only** inside
   `internal/server`'s recording helper (greppable scan, like the `os/exec` rule). This is
   what makes the route table derived, not curated, and the reveal route meta-test sound.
7. **App fence.** `internal/app` imported only by `cmd`, `conformance`, and tagged I/O tests.
8. **Test-layer fence.** Nothing imports `internal/conformance` from production packages.
9. **Doc comments.** Every `internal/` package has a package doc comment (`make orient`
   derives the live package map from them).

SQLite driver decision: start with `modernc.org/sqlite` (pure Go → trivial amd64+arm64
images, ADR-0013 §3). The ADR-0011 §2 benchmark fixture (100k Media / 10k Entities / 2M
relations) is the arbiter; a miss switches to `mattn/go-sqlite3`, touching exactly one
package.

## 5. The real seams (the only interfaces) and the storage contract

### 5.1 Single-writer storage gate (ADR-0011 §1) — `internal/store`

**Resolution (storage gate, HIGH — the panel's most load-bearing call):** the storage
draft's model wins over the `Write(fn func(*sql.Tx))` closure gate. **All SQL text lives in
`internal/store`**, organized one file per area (`media.go`, `entity.go`, `engagement.go`,
`auth.go`, …); consumers never see `database/sql`. This is what makes the SQL-level gates
mechanically enforceable: the OFFSET-ban SQL-string test, the vocabulary scan of SQL, and
the compile-time `LibraryFilter` convention all assume SQL is confined to typed methods in
one leaf package.

```go
package store

func Open(ctx context.Context, dataDir string, opts Options) (*Store, error)
// Open: placement guard → pending migrations? pre-migration backup → migrate →
// refuse-if-newer / refuse-on-checksum-drift (ADR-0011 §5–6).

type Store struct {
    Catalog *Catalog // distinct types: the compiler stops engagement writes landing in catalog.db
    Events  *Events
}
func (c *Catalog) Read(ctx context.Context, fn func(tx *CatalogTx) error) error
func (c *Catalog) Write(ctx context.Context, fn func(tx *CatalogTx) error) error
func (e *Events) Read(ctx context.Context, fn func(tx *EventsTx) error) error
func (e *Events) Write(ctx context.Context, fn func(tx *EventsTx) error) error
func (s *Store) Backup(ctx context.Context, destDir string) (BackupSnapshot, error)
// VACUUM INTO consistent snapshots of catalog.db + events.db; returns snapshot
// metadata (paths, sizes, schema versions, checksums) ONLY. The ADR-0011 §7 manifest —
// which must also reference the artwork store and the Trash locations — is assembled by
// the maintenance backup JOB HANDLER, registered in app.New: it sits above the gate and
// can see artwork + library/fileops config, which leaf `store` cannot (V-11).
```

`CatalogTx`/`EventsTx` wrap `*sql.Tx` unexported and carry typed query methods
(`UpsertMediaFileBatch`, `MediaPage`, `RegisterEntityKind`, `AppendEngagement`,
`ContinueWatching`, `Search`, `ApplyScanBatch`, …). Not the repository pattern: no
interfaces, no per-entity abstraction — one concrete package owning the schema. Default
test strategy is real SQLite via `storetest.Open(t)` (migrated temp file, single-digit ms).

**Single-writer mechanism:** per file, a read pool (`max(4, GOMAXPROCS)` conns,
`query_only=ON`, sibling ATTACHed `mode=ro`) and a write handle with
**`SetMaxOpenConns(1)`**, `BEGIN IMMEDIATE`. The pool-capacity-1 connection *is* the
serialized writer — enforced structurally, preserving per-caller context cancellation.
ADR-0011 §1 now blesses this pool-capacity-1 reading over a literal writer goroutine.
Write connections never ATTACH the sibling file (no cross-file commit is structurally
possible); batching is the caller's job (scanner: ~500 files per `Write`); a >1s write
closure logs a warning.

**Reveal filtering at the store boundary:** every read method returning Media / Entity /
engagement / search rows takes a **`store.LibraryFilter` as a non-optional parameter** —
there is no unfiltered variant to accidentally call. Internal jobs pass
`store.AllLibraries` explicitly; every `AllLibraries` call site is enumerated in a
committed conformance allowlist (mirroring the route allowlist pattern).

**Typed IDs** (`store.MediaID`, `store.EntityID`, `store.ProfileID`, `store.LibraryID`,
`store.DeviceID`) are defined in `store` next to the row types (store is a leaf and cannot
import owners); capability packages may alias (`type ID = store.MediaID`). Internal rowids
never hit the wire; `graph` maps to opaque external IDs and owns cursor encoding *format*
(store defines cursor *contents*: sort value + id; keyset only — `OFFSET` is banned by a
store-package SQL-string test).

**Stream inventory is schema-shaped rows** (resolution, HIGH): ADR-0010 §4 is frozen and
explicit — per-stream codec/profile/language/flags/channel layout/bit depth/HDR/DV and
attachments are columns from migration 0001 (`media_stream`, `media_attachment` keyed to
`media_file`). `ffmpeg.ParseProbe`'s struct maps to them; raw ffprobe JSON may be kept in
an optional debug column, never as the queryable truth. The opaque-JSON variant is dead.

**FTS** (ADR-0011 §4) lives wholly in `store`: `search_doc` + external-content FTS5,
maintained inside the same write transaction as the rows they mirror (no triggers); the
single query shape applies the `LibraryFilter` predicate **before** `ORDER BY`/`LIMIT`.
`tx.Search` registers as a reveal surface (via graph's `Query.search` registration).

**Entity-Kind index DDL — one owner** (resolution, HIGH; spelling corrected by V-3): the
single stage-4 entry point is **`func (s *entity.Service) RegisterKinds(ctx context.Context,
kinds []entity.KindDecl) error`** — a *method* on the entity service, which is constructed
in `app.New` with the catalog handle (a package-level function could not reach the database
without the package-level state §2 bans). It converts plugin declarations to
`store.EntityKindSchema` and executes via `tx.RegisterEntityKind`
(deterministic partial-index names; idempotent; removal drops indexes, deactivation keeps
them). `catalog.EnsureEntityIndexes(*plugin.Registry)` and "entity emits DDL directly" are
both dead — store never interprets plugin manifests; import direction stays
plugin → entity → store.

**Two files** (ADR-0011): `catalog.db` = curation that must never be lost (library/file
plane, curation, entity system, FTS, plugins/config, profiles/devices/sessions).
`events.db` = high-write (engagement log **canonical**, projections, playback sessions,
jobs + checkpoints, activity log, auth throttle counters). The engagement event log is
kept forever by default. Backups cover **both** files under one manifest (events.db is
*not* rebuildable — see infeasibility report, review doc). Cross-file merge uses the
`merge_intent` outbox protocol (catalog commit carries the intent; events writer re-points
and rebuilds projections idempotently, replayed at startup).

**Migrations:** thin in-repo runner (`internal/store/migrate`), `//go:embed`, two
independent version lines, forward-only by construction, checksum-pinned history,
backup-before-apply, refuse-on-newer.

**Placement guard:** in `store.Open` — Linux `Statfs` magic / macOS `f_fstypename`
denylist (NFS/SMB/CIFS/WebDAV/AFP refuse); FUSE/unknown warn-and-start; override
`--allow-network-data-dir`. Bare-metal Windows data dirs are not a v1 target. Data dir
only; media dirs exempt.

### 5.2 Plugin loader (ADR-0003) — `internal/plugin`

**Plugin-tier map (refined-by: ADR-0124):**

| Tier | Package home | Composer | New privilege/surface |
|---|---|---|---|
| Domain Plugin | external `github.com/beamfall/plugin-<id>` modules in sibling per-plugin repositories | `cmd/beamfall` loads declarations through `internal/plugin` | none; data and hooks against fixed vocabularies |
| Integration Plugin | top-level `internal/<id>` or `internal/integrationhost` for worker runtime | `cmd/beamfall` / `internal/app` wires explicit seams | yes; external service, worker, network, IPC, secret, or acquisition-adjacent surface |
| Workflow Plugin | `internal/workflow/<id>` | `cmd/beamfall` sole composer; compiled-in and runtime-gated | none beyond existing mediated seams |

**Workflow Safety Corollary (ADR-0124):** a Workflow Plugin is admitted only when it composes
existing Core primitives through the governor, managed-file-ops/admission, and already-mediated
engine seams. It may not acquire a new outbound network path, IPC/daemon socket, OS-service
dependency, `exec` site, or raw filesystem writer. A capability that can route
acquisition-adjacent activity is always an Integration Plugin, never Workflow, so invariant 8 remains
compile-excludable on App Store binaries.

**Resolution (pipeline + hooks, HIGH):** Domain Plugin declarations and hooks live in their
external modules and cross the fixed `plugin.Bundle`/SDK boundary. `cmd/beamfall` is the
explicit production composer; Core validates declarations against its fixed vocabularies.

```go
package plugin

func ParseBundle(fsys fs.FS) (*Bundle, error)                   // stage 1: strict decode
func (b *Bundle) Validate() error                               // stage 2: fixed vocabularies (self-contained vocab.go)
                                                                //          incl. plugin.ValidateLayout + plugin.ValidateSource (V-12)
func Compose(active []*Bundle, hooks map[ID]Hooks) (*Registry, error) // stage 3: qualified kinds "<plugin-id>/<kind>"
type Store struct {
    // Injected in app.New from (*entity.Service).RegisterKinds — a func value, not a
    // new interface (V-3); plugin's documented constructor surface stays ParseBundle/
    // Compose/Store, and §2's no-globals rule holds.
    RegisterKinds func(ctx context.Context, kinds []entity.KindDecl) error
    /* atomic.Pointer[Registry] */
}
func (s *Store) Current() *Registry
func (s *Store) Activate(ctx context.Context, ids []ID) error   // stages 1–5, all-or-nothing
// stage 4 calls s.RegisterKinds (the one DDL entry point, §5.1); stage 5 swaps the pointer.
```

**Hooks (resolution of the producer-vs-consumer conflict):** the hook **param/result
types** (`plugin.Guess`, `plugin.Candidate`, `plugin.Secrets`) live in `internal/plugin` as
part of the loader-seam contract; the **interfaces consumers depend on are re-declared at
each consumer** (1–2 methods over plugin types — `identify` declares its `FilenameParser`
and `CandidateRanker`, `scrape` its `SourceAuthenticator`, `app` the `Runner`). `plugin`
also declares matching carrier interface types so the `Hooks` struct can hold values;
Go's structural interfaces make `plugin.Hooks` fields assignable to the consumer-declared
interfaces in `app`'s wiring. This satisfies both frozen constraint 2 (interfaces at the
consumer) and the firstparty arch row (firstparty imports `plugin` **only**).

```go
type Hooks struct { // every field optional; zero for data-only bundles
    Parse FilenameParser      // ParseFilename(path string) (Guess, bool)
    Rank  CandidateRanker     // Rank(g Guess, c []Candidate) []Candidate
    Auth  SourceAuthenticator // Authenticate(ctx, source string, secrets Secrets) (http.Header, error)
    Run   Runner              // Run(ctx) error — Integration Plugins
}
type Secrets interface{ Get(name string) (string, error) } // implemented by config's secret store, wired in app
```

`SourceAuthenticator` takes `plugin.Secrets` (a plugin-local accessor), **not**
`config.Secrets` — firstparty packages never import `config`.

Consumers hold `func() *plugin.Registry` (immutable data snapshot — no interface for
data). An invalid declaration deactivates the whole bundle with a Console-visible error —
never a partial load. `plugin.ValidateLayout` is the one Layout validator, used by both the
bundle loader and the Console layout-editor mutation. Deactivating a plugin with a bound
Library is blocked until the Library is explicitly unbound or removed.

**Source specs (resolution, V-12) — mirroring the ValidateLayout arrangement:** the
declarative Source spec types (**`plugin.SourceSpec`**) live in `internal/plugin` as part
of the loader-seam contract (Bundles carry Section Sources as data), and
**`plugin.ValidateSource`** is the single Source validator — run fail-closed in stage 2
for structural/vocabulary validity and again at `Compose` for cross-kind references; the
Console's Saved-Filter / Smart-Collection mutations call the same validator, satisfying
ADR-0003 §7's "the server validates every Source at plugin load". `store` compiles
`plugin.SourceSpec` values into typed `SourceQuery` inputs (the `scrape → plugin` and
`manifest → plugin` edges in §4 stay acyclic: plugin never imports consumers). The v1 Source grammar includes grouped boolean
logic (`AND`/`OR` plus per-predicate negation) so Saved Filters and Smart Collections do
not harden around an AND-only schema. `graph`'s `SourceSpec`/`SourceInput` mirror it
field-for-field (§7).

### 5.3 FFmpeg boundary (ADR-0016, 0010, 0012 §10, 0013) — `internal/ffmpeg`

One concrete `Runner`; consumers define the slices they need, satisfied by `*ffmpeg.Runner`
and wired in `app`:

```go
// internal/ffmpeg — the concrete boundary; exec + parse split so the test tiers split
func New(cfg Config) (*Runner, error)             // bin paths (pinned bundle or BYO override), hw caps, limits;
                                                  // binary resolution/validation is LAZY (first use) so app.New
                                                  // boots without FFmpeg present (V-7c)
func (r *Runner) Version(ctx) (string, error)     // will be asserted == ffmpeg.PinnedVersion() by the
                                                  // integration tier (V-14)
func (r *Runner) Probe(ctx, path string) (*Probe, error)  // exec wraps the pure parser
func ParseProbe(raw []byte) (*Probe, error)                // pure; the unit-tier surface
func (r *Runner) FrameGrab(ctx, s FrameGrabSpec) error
func (r *Runner) Snapshot(ctx context.Context, path string, at time.Duration, width int) error
                                                  // stdlib-typed convenience over FrameGrab; satisfies
                                                  // artwork's FrameGrabber DIRECTLY (V-17 — no adapter)
func (r *Runner) SpriteSheet(ctx, s SpriteSpec, progress func(float64)) error
func (r *Runner) ExtractSubtitle(ctx, path string, streamIndex int) ([]byte, error) // → WebVTT
func (r *Runner) StartHLS(ctx, spec HLSSpec) (*Proc, error) // one rendition per live session
func (r *Runner) DetectHW(ctx) []HWReport

// consumer slices (each defined at its consumer):
// scan:     Prober        { Probe(ctx, path) (*ffmpeg.Probe, error) }
// artwork:  FrameGrabber  { Snapshot(ctx, path, at, w) error }   (stdlib types only;
//                           matched structurally by Runner.Snapshot — V-17)
// generate: Grabber       (sprites/grabs)
// playback: Transcoder    { StartHLS(ctx, ffmpeg.HLSSpec) (*ffmpeg.Proc, error) }
```

Hardening lives **inside** `Runner` (ADR-0012 §10). **Implemented today:** direct
`exec.Command` (never a shell) and atomic temp-file publish for every output; arch rule §4.2
confines `os/exec` to this package so no caller can bypass the boundary. **Designed but NOT
YET implemented**: positional/after-`--`
input handling with explicit option-injection rejection, per-process rlimits, and Linux
landlock via a re-exec shim (`beamfall ffmpeg-shim` hidden subcommand that self-confines then
execs the pinned binary). The rlimit/landlock confinement needs the re-exec shim because Go's
`SysProcAttr` cannot set per-child rlimits directly — until that shim exists, FFmpeg runs
with full server privileges, which is the largest native parse surface in the process.

**Pin implementation (V-14):** one committed text file
`internal/ffmpeg/ffmpeg.pin` (resolution, HIGH; relocated by V-14 — `go:embed` cannot reach
`../../build/`, so the pin is package-local), read by the Dockerfile (ADR-0013 §3),
`go:embed`-ed by `internal/ffmpeg` (exposed as `ffmpeg.PinnedVersion()`), asserted by
`TestPinnedVersionAndFixtureManifest` and by the integration tier's `Version()` check — a Go const can't feed a
Dockerfile, so the file feeds both. Two test tiers per frozen
constraint 5 — mechanics in `TESTING.md` §6.

**Pin content + match rule (V-18):** the file holds exactly the **normalized FFmpeg
version token** — the string `Runner.Version()` extracts from line 1 of `ffmpeg -version`
output (e.g. `7.1.4-Jellyfin`) — one line, no comments. Comparison is **exact string
equality** everywhere (regen tool, `TestPinnedVersionAndFixtureManifest`, the integration tier's version
assert). The Dockerfile is the single place that maps the token to a package/download
coordinate (jellyfin-ffmpeg-style release id), with a comment marking that translation as
the only one. Runtime equality is currently enforced by the Docker build, fixture refresh,
and tests; server startup does not yet compare a BYO FFmpeg runtime to the pin or provide
mismatch-specific warning or degradation behavior.

### 5.4 Stream-token seam (ADR-0010 §3) — `internal/streamtoken`

**Resolution (three owners → one, HIGH):** both halves (issue + verify) live in
`internal/streamtoken`; `auth.MintStreamToken` and the api-side `token.Target` orphan are
dead. The type formerly called `streamtoken.Scope` is renamed **`Grant`** (killing the
collision with the reveal scope).

```go
package streamtoken

type Target struct { // exactly ONE of Media/Entity is set; Issue validates (V-15)
    Kind   Kind            // playback | artwork | trickplay | sidecar
    Media  store.MediaID   // Media-addressed bytes (playback, trickplay, sidecars, Media artwork)
    Entity store.EntityID  // Entity artwork — Artwork Slots live on Media and Entities alike
}
type Grant struct {
    Kind       Kind
    Media      store.MediaID  // mirrors Target: exactly one of Media/Entity set
    Entity     store.EntityID
    Restricted bool           // computed at issue: target reachable ONLY through restricted Libraries
    Device     store.DeviceID
    Profile    store.ProfileID
    Expires    time.Time
}
func (s *Service) Issue(ctx context.Context, g Grant, ttl time.Duration) (string, error)
func (s *Service) Verify(ctx context.Context, token string, want Kind) (Grant, error)

// consumer-defined seam, satisfied by internal/auth's session store (wired in app):
type Revocations interface {
    DeviceRevoked(store.DeviceID) bool
    RevealHeld(store.ProfileID, store.DeviceID) bool // sub-ms; in-memory cache, writer-invalidated
}
```

**Contract:** `Verify` = HMAC signature + expiry (stateless) **plus live checks on every
call** — `DeviceRevoked` always; when `Grant.Restricted`, `RevealHeld(Profile, Device)`
must *currently* hold. Expiry bounds replay; live checks give relock/revoke immediacy
(ADR-0010 §3; see the TTL infeasibility report). **Reveal semantics for Entity-addressed
bytes (V-15):** the issuer computes `Restricted` at issue time — a Media target inherits
its owning Library's restriction flag; an Entity target is `Restricted` iff the Entity is
not reachable through any unrestricted Library in the issuing `auth.Scope`
(denial-of-existence preserved for Entities that exist only in restricted Libraries). The
former `Revealed` field is dead — `Restricted` gates the live recheck and is a property of
the *target*, not the session. Direct-play copy loops re-verify every 64 MiB / 30 s.
Issuance is called only by `playback` (playbackInfo URLs), `mediahttp`, and `graph`'s URL
builders — `graph` consumes it through its own consumer interface:

```go
// internal/graph
type StreamTokens interface {
    Issue(ctx context.Context, s auth.Session, t streamtoken.Target) (url string, err error)
}
```

**Implementer (V-13):** `mediahttp` exports **pure URL-builder functions**
(`mediahttp.StreamURL(kind, token)` and siblings for the HLS/trickplay/artwork/sidecar
shapes — `mediahttp` stays the single owner of URL shapes and never imports `auth`);
`app.New` constructs the adapter — a small app-local value composing
`streamtoken.Service.Issue`, `auth.ScopeFor` (Device/Profile/`Restricted` derivation from
the Session), and the `mediahttp` builders — and passes it to `graph`'s constructor.
graph's seam stays test-fakeable; no new arch edges (`app` may import anything).
**TTL policy:** `streamtoken` owns per-Kind defaults (artwork/trickplay/sidecar: short);
`playback` supplies the runtime-scale TTL for playback grants (infeasibility report I-7).

## 6. The Reveal contract: one Scope, one Registry

**Scope (resolution, HIGH):** one canonical authorization value, owned by `auth` (it is
derived from the Session, which `auth` owns), converted to the store-owned filter at the
storage boundary — the only arrangement compatible with store-is-a-leaf:

```go
package auth
type Scope struct {
    Libraries []store.LibraryID // Profile access list ∩ reveal state, per ADR-0012 §4
    Revealed  bool
}
func ScopeFor(ctx context.Context, s Session) (Scope, error) // cheap; re-derived per event on long transports
func (sc Scope) Filter() store.LibraryFilter
```

`session.Scope` and `reveal.Scope` are dead names. Resolver flow everywhere:
`auth.SessionFrom(ctx)` → `auth.ScopeFor` → capability/store reads take the scope/filter as
a **required positional parameter** (forgetting reveal filtering is a compile error).
Content outside scope is the owning package's `ErrNotFound` — restricted and nonexistent
are indistinguishable on every surface; unknown/inactive/out-of-scope Entity Kinds return
empty connections, never errors.

**Registry (resolution, HIGH — four designs → one):** one **instance-scoped** registry
type in `internal/reveal` (stdlib-only), created in `app.New` and threaded to every wiring
site; **no package-level mutable state** (conformance can boot multiple in-process
servers per test — globals would panic or cross-contaminate). `mediahttp`'s private byte
registry, the package-global `reveal.RegisterSurface`, and `internal/serving` are all dead.
Testing's four-Class taxonomy wins (Aggregate and EventFanout are required by ADR-0004's
enumeration).

```go
package reveal

type SurfaceID string
type Class int
const (
    ContentJSON Class = iota // GraphQL fields returning content-derived data — Media/Entity/
                             // LIBRARY-reaching, incl. Navigation-Manifest and Layout/Section
                             // types that carry restricted-Library names (V-2: ADR-0003 §6
                             // denial-of-existence is a Library-boundary contract)
    ContentBytes             // media-byte HTTP: ranges, HLS, trickplay, artwork, sidecars
    Aggregate                // counts/stats — leak via numbers, not names
    EventFanout              // subscriptions, tombstone feeds, presence (webhooks when built)
)
type Surface struct {
    ID           SurfaceID // "media.hls.segment", "graphql.search", "fanout.tombstones"
    Class        Class
    Route        string    // "GET /stream/hls/{token}/{seg}"; "" for GraphQL/fanout
    GraphQLField string    // "Query.search"; "" for HTTP/fanout
}
func NewRegistry() *Registry
func (r *Registry) Register(s Surface) // panics on duplicate ID
func (r *Registry) All() []Surface
```

Registration happens where the surface is wired, into the threaded instance: `graph`'s
`surfaces.go` registers every content-bearing root field and subscription; `mediahttp`'s
`Register(sf)` wraps the handler in `Verify` middleware, mounts it through an **injected
function value** — its constructor takes `mount func(pattern string, h http.Handler)`,
wired in `app.New` from `server`'s recording `Handle` (V-4: identical decoupling, zero
interfaces — mediahttp never imports server, and the four-seam interface inventory stays
exact) — **and** registers the `ContentBytes` surface in the same call;
`server` registers the SSE fanout routes as `EventFanout` surfaces. `app.New` returns the populated registry, so any
test constructing the app sees exactly the production surface set. Suite mechanics
(probes, meta-tests, allowlist): `TESTING.md` §2.

**Fanout choke point = `server` SSE over store outbox; ingestion is store-mediated**
(resolution; producer side specified by V-1): the delivery side is the `/api/events` and
`/v1/events` SSE handlers in `server`, wired by `app.New` with the store and registered as
`EventFanout` surfaces. The producer side has no delivery API: producers append
`event_outbox` rows inside their existing write transactions through the single-writer
gate, and the SSE loop tails store from a per-subscriber cursor while re-evaluating
Session/Reveal state per event. Webhooks remain a separate integration path through
`internal/webhook`.

## 7. HTTP and GraphQL wiring

**Routes:** stdlib `ServeMux`, owned by `server`. **All** routes mount through one
recording helper — `server.Handle(pattern string, h http.Handler)` — which both registers
on the mux and appends to the route table behind `Routes()`. Arch rule §4.6 bans direct
mux calls anywhere else, so route-table completeness is enforced, not assumed (this is
what makes `TestEveryRouteRegistered` sound without chi).

**GraphQL** (`internal/graph`):

```
internal/graphquery/
  query.go                   pure operation selection + top-level field parser
internal/graph/
  graph.go                   hand-written GraphQL handler + dispatch
  query.go                   graph request variable helpers + graphquery wrappers
  mydata.go                  My Data graph surface
  *_test.go                  auth, registration, and contract tests
```

One endpoint `POST /api/graphql`; graph code calls store directly and applies auth/reveal
checks in Go. Entities are served through fixed generic queries only (`entities(kind:…)`,
`entity(id:…)`). Plugins contribute generic data and manifest declarations; they do not add
GraphQL schema types. Errors:
`extensions.code ∈ {NOT_FOUND, FORBIDDEN, UNAUTHENTICATED, THROTTLED, INVALID, CONFLICT, INTERNAL}`
with the ErrNotFound denial-of-existence rule of §6.

Graph contract governance lives in focused graph tests plus `internal/conformance`; there is
no generated snapshot/diff toolchain.

## 8. Jobs, governor, playback (pipeline shape)

`internal/governor` is a **separate leaf** (resolution, MEDIUM): playback and jobs both
acquire from the same `Pool` — that shared pool *is* the preemption mechanism
(`AcquireInteractive` may preempt the youngest background `Lease`; `ErrSaturated{Suggest}`
is the "drop to this quality or queue" admission answer, ADR-0010 §6 / 0013 §1).
Interactive transcode sessions are **not** jobs — they are `playback` sessions holding
governor leases. Derivative/background media jobs are split across concrete capability
packages (`genassets`, `decodeverify`, `skipdetect`) and registered through `jobs`.

Job substrate: function registry (`Register(kind, fn)`), `Spec{Kind, Class, Payload,
DedupeKey, Priority}`, checkpoint/yield contract (on `Lease.Preempted` or ctx cancel:
persist checkpoint, return `jobs.ErrYielded`), `Resume(ctx)` at boot. Concurrency classes:
`scan` (1/Library), media generation/verification (CPU-budgeted, niced — the preemption
victims), `network` (scrapers, SSRF-allowlisted upstream), `maintenance` (1). Rows +
checkpoints in events.db.

Playback: `Decide(versions, capabilityProfile, policy) Decision` is pure (golden-fixture
testable); `playbackInfo` = reveal-filtered lookup (graph) → `Decide` →
`streamtoken.Issue` → URLs + `timelineOffset` (see infeasibility report on ADR-0010 §7).
Sessions keyed `(Device, Media, Profile)`; seek = debounced replace; ABR = restart at new
bitrate; multi-part Versions may present a visible part-boundary transition in v1 rather
than a gapless stitched HLS timeline; idle reaped; relock/revoke stops live procs via the
session-store notification and kills token verification. Background generation may borrow
idle hardware-encoder slots, but interactive playback acquisition can preempt those leases.

## 9. Conventions (stdlib-first)

- **Errors:** `fmt.Errorf("…: %w", err)`; sentinel `ErrNotFound` per owning package only
  where callers branch; the §6 denial-of-existence rule; `auth.ErrForbidden` only for
  role/tier failures (never an existence oracle).
- **Logging:** `log/slog` only; cmd installs the handler (JSON prod / text dev) wrapped by
  `server.NewCtxHandler` (request-ID enrichment); capabilities use
  `slog.InfoContext(ctx, …)` or take `*slog.Logger` in constructors.
- **Config:** `internal/config` = bootstrap only; runtime/operational state lives in
  catalog.db owned by its capability (ADR-0013 §5). Secrets via the `0600` file store —
  never in TOML, never logged.
- **Context & session:** `ctx` first arg everywhere; server middleware authenticates the
  transport and stores the Session; `auth.SessionFrom(ctx)`; long-lived transports
  re-evaluate per event by construction of the server SSE loop.
- **IDs:** typed int64 newtypes in `store` (§5.1); aliases in owning packages.
- **Goroutines:** long-running components implement `Run(ctx) error`, owned by app's
  errgroup; no unsupervised background goroutines (background work = the job substrate).
- **Timestamps:** UTC unix milliseconds, `INTEGER`, everywhere in the schema.
- **Seam markers:** the four seam interfaces carry a `// beamfall:seam <name>` comment —
  greppable; `make orient` lists them.

## 10. Skeleton build order

1. `go.mod`, this doc's §0 table.
2. `internal/store` (+ migrate runner + migration 0001 incl. stream-inventory tables +
   storetest) — nothing else compiles meaningfully without it.
3. `internal/conformance`: `TestArch` consuming §4 verbatim, `TestVocab` + data files,
   shared firstparty-boundary constant — red/green from the first package.
4. `config`, `governor`, `ffmpeg` (+ `ffmpeg.pin` + probe fixture manifest), `reveal`.
5. `store` catalog/auth/query surfaces → remaining capabilities along §4's topological
   order.
6. `plugin` (+ one invented-vocabulary test bundle) → `graph` (hand-written contract) →
   `server` → `app` → `cmd`.
7. `conformance` golden/reveal/route suites; I/O E2E; gate + skills
   (AGENT-TOOLING.md).
