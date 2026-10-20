# Beamfall Core — testing architecture (converged)

Status: **canonical**. Names, paths, and mechanisms match `ARCHITECTURE.md` §0's canonical
name table exactly.

Standard pyramid **plus** four first-class conformance suites (frozen constraint 4). The
suites are the single source of enforcement; hooks, skills, and CI invoke them and never
re-implement a check (frozen constraint 6).

## 1. Test layers

| Layer | Lives | Gate | Runs |
|---|---|---|---|
| **Unit** | `*_test.go` beside code | none | always; included in `-short` |
| **Integration (in-process)** | `*_test.go` beside code, `testing.Short()` skip | none | default `go test ./...`; skipped by `-short` |
| **Conformance** | `internal/conformance/` (test-only package) | none — never tag-gated | always, including `-short` |
| **FFmpeg integration** | shim and FFmpeg-adjacent tests | `//go:build ffmpeg` | CI stage with pinned FFmpeg; locally when present |
| **I/O E2E** | `internal/ioe2e/` | `//go:build io`, `io_e2e`, `io_live` | fast I/O gate, slow release journeys, opt-in live checks |
| **Bench gate** | `internal/store/benchfixture_test.go` | `BEAMFALL_BENCH=1` | `make bench`; required for schema-freeze changes |

**Build-tag registry (closed): `ffmpeg`, `io`, `io_e2e`, `io_live`.** No other tags exist.
A tag means "needs something outside the Go toolchain"; `testing.Short()` means
"in-process but slow" (real SQLite files, migration runs, FTS rebuilds). Budgets:
`go test -short ./...` < 30s; default `go test ./...` < 3min.
SQLite is in-process, so storage tests are *integration by honesty, default-run by cost* —
no tag.

**No mocks at non-seams** (ADR-0016). Integration and conformance tests run the real store
(`storetest.Open(t)`), real migrations, real token verification. The only substituted
boundaries are the four ADR seams. `app.New` constructs the real FFmpeg runner directly;
the runner resolves binaries lazily, so default conformance can boot without a local FFmpeg
binary, and real-FFmpeg behavior belongs to the `ffmpeg` and I/O tiers.

In-process integration anchors: migration backup-then-apply + refuse-on-newer +
refuse-on-checksum-drift (ADR-0011 §5), placement-guard refusal (ADR-0011 §6),
single-writer serialization under concurrent scan + engagement writes (ADR-0011 §1),
merge-intent replay after simulated crash (ARCHITECTURE.md §5.1).

## 2. Conformance suite A — Reveal contract

The enumerated completeness contract (ADR-0004 §4, amended by 0010 §3 to cover bytes and
0012 to cover event streams) is a **registry + probe + meta-test** triangle.

### The registry (production code — `internal/reveal`, see ARCHITECTURE.md §6)

One instance-scoped `reveal.Registry`, created in `app.New`, threaded to every wiring
site, returned by `app.Surfaces()`. **No package-level mutable state** — the harness boots
two in-process servers per test run and must not cross-contaminate. Four Classes:
`ContentJSON`, `ContentBytes`, `Aggregate`, `EventFanout`. Registration sites include
`graph` for content-bearing root fields, `mediahttp` for byte-serving routes, and `server`
for SSE event routes. There is exactly one registry; the old mediahttp-private byte
registry and the package-global `RegisterSurface` do not exist.

### The harness

Conformance boots real in-process handlers through the production composition path where a
test needs the full surface set, and otherwise uses focused handlers/stores for smaller
route and golden checks. `app.New` constructs the real lazy FFmpeg runner; there is no
FFmpeg test-substitution option. Corpus worlds use invented vocabulary (never domain
words). World:
Library
"Plain" + a restricted Library **titled with a second generated sentinel**
(`reveal-canary-lib-<rand>`, V-2 — restricted-Library *names* are content too, per
ADR-0003 §6) containing canary Media titled `reveal-canary-<rand>`; three Sessions —
unrevealed adult Profile, revealed adult Profile, managed Profile. It also builds the
**world-without**: the same server with the restricted Library never created. All fixture
data is written through real services (no SQL bypassing the single writer). Raw-body
sentinel scans check **both** sentinels — Media names and restricted-Library names must
not leak.

### Route And Golden Checks

The current Reveal enforcement combines route coverage (`TestEveryRouteRegistered`),
server route surface derivation, and golden corpus checks for locked, revealed, and
managed contexts. Byte and SSE behavior is covered by focused server/media tests plus the
I/O tiers when real FFmpeg bytes are required.

`TestRevealContract` iterates `registry.All()` × sessions, asserting per Class:

| Class | unrevealed / managed | revealed |
|---|---|---|
| ContentJSON | no canary; raw response bytes contain neither sentinel | canary present |
| ContentBytes | no token mintable; a token minted under a revealed session **stops serving after relock/revocation** (ADR-0010 §3 — driven by the lifecycle driver above) | 200 with valid token — asserted on real bytes for direct-play/artwork/sidecar; **HLS (transcode-dependent) asserts token-gating/admission semantics here, with real-byte 200 owned by the `ffmpeg` and I/O tiers** (V-7b) |
| Aggregate | count == world-without count ("absent, not hidden") | count includes restricted |
| EventFanout | a restricted-content event delivers nothing; payload bytes lack both sentinels; restricted tombstones arrive as generic resync hints | delivered |

### Meta-tests — how "all surfaces enumerated" is itself verified

1. `TestEveryRouteRegistered` — iterate `app.Routes()` (the **derived** route table; all
   routes mount through `server.Handle`, and arch rule ARCHITECTURE.md §4.6 bans direct
   `ServeMux` calls elsewhere, so the table is complete by construction — no chi, no
   hand-curated list). Every route is in the registry **or** in the self-pruning inline
   allowlist in `internal/conformance/route_test.go` (`/health` plus the small set of
   non-content bootstrap/control routes, each with an adjacent justification). Unknown
   route = fail. **Unused allowlist entry = fail.**
2. The hand-written GraphQL dispatcher is enumerated directly from its live registries.
   `TestSchemaManifest`, `TestNestedSchema`, and `TestWireContract` byte-compare those
   deterministic projections with committed JSON artifacts; Suite D below is the full
   contract. There is no SDL parser or GraphQL-schema allowlist.
3. `TestEveryActionHasBehaviorTest` and `TestEveryCommandRouteHasBehaviorTest` scan the
   focused graph/server tests for every mutation and command route. Their exceptional
   cases are inline, justified, and self-pruning; an orphan or newly unnecessary entry
   fails.
4. Fanout closure is **structural** (V-1): `server` is the SSE delivery side, and
   producers reach subscribers only via store event rows written through the single-writer
   gate (ARCHITECTURE.md §6). A second unfiltered fanout path must register as an
   `EventFanout` Reveal surface and pass route coverage.

New-surface workflow: register the route/surface, add its focused behavior test, regenerate
the schema artifacts when the dispatcher shape changes, then run the conformance suite.

## 3. Conformance suite B — domain-agnosticism scan

`internal/conformance/vocab_test.go` (`TestVocab`). Mechanical enforcement of hard
invariants 1–2.

| Scanned | How |
|---|---|
| Go source lines | all non-test `.go` files in `internal/` + `cmd/`; `VocabExemptPrefixes` is empty, so extracted plugin modules create no Core exemption |
| Client-facing strings | covered by the same line scan (errors, logs, manifest defaults) |

Mechanics: case-insensitive word-boundary matching against the inline `bannedVocab` slice
in `internal/conformance/vocab_test.go`. `_test.go` is exempt. Narrow source-line exceptions
live in the inline `vocabAllowedLines` map, and `TestVocabAllowedLinesStayNarrow` fails stale
or broadened entries.

## 4. Conformance suite C — arch test (dependency direction)

`internal/conformance/arch_test.go` (`TestArch`) over `golang.org/x/tools/go/packages`.
The rules table is **ARCHITECTURE.md §4 verbatim**, committed as a Go map literal —
whitelist semantics, plus the nine cross-cutting assertions (driver confinement, one
`os/exec` site, transport leaks, plugin firewall via the shared constant, generated fence,
**mux confinement**, app fence, test-layer fence, package doc comments). **Scope (V-9):**
the test loads `./...` under the default build-tag set and evaluates non-test compilation
units only — `_test.go` files and tag-gated I/O packages fall outside the whitelist by
construction. Failure output names the offending import edge and the
ARCHITECTURE.md section it violates.

`internal/skins` is intentionally parked until SKIN-2/3: default tests cover only the
WCAG-2.2 contrast math already ported from the design-system tokens. The future Skin
persistence/store/graph surface must add conformance coverage when it is wired, not before.

## 5. Conformance suite D — hand-written GraphQL wire snapshots

Beamfall's GraphQL contract is a hand-written, depth-1 dispatcher; there is no SDL, gqlgen,
or schema-codegen pipeline. The gate derives three deterministic JSON artifacts from the
same registries and result structs that serve requests:

- `TestSchemaManifest` compares `graph.SchemaManifest()` with
  `internal/conformance/testdata/schema-manifest.json`. It enumerates every dispatchable
  top-level field and records its effect class and required tier. Regenerate with
  `go run ./tools/schema-manifest > internal/conformance/testdata/schema-manifest.json`.
- `TestNestedSchema` compares `graph.NestedSchema()` with
  `internal/conformance/testdata/nested-schema.json`. Reflection over the actual result
  structs pins every dispatchable field's nested result shape. Regenerate with
  `go run ./tools/nested-schema > internal/conformance/testdata/nested-schema.json`.
- `TestWireContract` composes both GraphQL projections with the stable `/v1` REST and SSE
  surfaces into `tools/wire-contract/testdata/wire-contract.json`. Regenerate with
  `go run ./tools/wire-contract > tools/wire-contract/testdata/wire-contract.json`;
  `TestWireContractDeterministic` proves stable output for a fixed contract version.

All three drift checks run in `make gate`. Wire changes are reviewed as committed JSON
diffs against the hand-written dispatcher; no exception file or breaking-change CLI can
bypass them.

## 6. FFmpeg: two tiers behind one seam

The seam is `internal/ffmpeg` (concrete `Runner`; consumers declare 1–2-method slices).
**Pin implementation (V-14):** `internal/ffmpeg/ffmpeg.pin` is
(package-local — `go:embed` cannot reach a top-level `build/` dir), one committed text file
read by the Dockerfile (ADR-0013 §3), `go:embed`-ed into `internal/ffmpeg`
(`PinnedVersion()`), asserted by the fixture-manifest test, required by the regen tool, and
version-asserted by the integration tier. **Content + match rule (V-18):**
one line holding the normalized FFmpeg version token — the string `Runner.Version()` extracts
from line 1 of `ffmpeg -version` (e.g. `7.1.4-Jellyfin`) — compared by exact string equality
everywhere; only the Dockerfile maps it to a download coordinate (ARCHITECTURE.md §5.3).

### Unit tier — golden ffprobe fixtures

`internal/ffmpeg/testdata/*.json` contains one golden per entry in
`internal/ffmpeg/testdata/probe/manifest.json`. The goldens exercise the ffprobe-JSON →
stream-inventory parser (`ParseProbe`; ADR-0010 §4 fields including DV profile,
HDR10/HDR10+, channel layouts, and font attachments). They do not feed the subtitle/audio
decision matrices or the `playbackInfo` ladder.

**Manifest integrity, enforced:** `manifest.json` records each golden's SHA-256, fixed
`fabricated` mode, empty synthesis arguments, the pinned FFmpeg version, and a timestamp.
The manifest test checks those recorded values, complete fixture coverage, and current
file hashes. The regeneration tool treats the existing fixture files and their recorded
provenance as inputs; it does not prove how a fixture was created or forbid a deliberate
fixture edit followed by regeneration.

### Integration tier — `//go:build ffmpeg`

The delivered build-tagged integration test, `TestPinnedRuntimeVersion`, invokes the real
FFmpeg binary and requires `Runner.Version() == ffmpeg.PinnedVersion()`. It does not
synthesize the probe corpus or exercise transcode/HLS behavior. Run it only where the
installed FFmpeg's normalized version token matches `internal/ffmpeg/ffmpeg.pin`.

### The committed probe fixtures (single source of truth)

`internal/ffmpeg/testdata/probe/manifest.json` inventories the committed probe JSON
fixtures and records each fixture's mode, synthesis provenance, and SHA-256 digest. There
is no generated corpus table: the manifest and its listed files are the delivered source
of truth. `TestPinnedVersionAndFixtureManifest` enforces the committed provenance policy
and hashes; it does not independently attest the fixtures' origin.

### Regen tool spec — `tools/regen-fixtures`

`go run ./tools/regen-fixtures` refreshes only the committed
`internal/ffmpeg/testdata/probe/manifest.json`; it does not synthesize, render, probe, or
write fixture corpus files. It refuses to run unless `ffmpeg -version`'s normalized token
equals `ffmpeg.PinnedVersion()` (`internal/ffmpeg/ffmpeg.pin`), then rehashes each existing
manifest-listed fixture, preserves its `mode` and `synthesis_args` provenance, updates the
pin and timestamp, and atomically replaces the manifest. Fixture paths must be basenames
whose symlink-resolved targets remain under `internal/ffmpeg/testdata/`.

`TestPinnedVersionAndFixtureManifest`, `TestRunnerVersionMatchesPin`,
`TestRegenFixturesRefusesMismatchedFFmpeg`, and `TestFixturePath` enforce the delivered
pin, manifest, refusal, provenance, and path-containment behavior. Corpus generation and
fixture writes remain unimplemented.

## 7. I/O E2E

`internal/ioe2e` owns the fast `io` tier, the slow `io_e2e` journey tier, and the opt-in
`io_live` tier. The agent-facing entrypoint is `script/io-e2e.sh`; `make io-gate` runs
the fast checks and `make release-gate` runs the slow journey evidence.

## 8. Bench Gate

`make bench` runs `BEAMFALL_BENCH=1 go test ./internal/store -run
TestEntityQueryLatencyBudget`: a seeded deterministic 100k Media / 10k Entities /
2M-relation DB from `internal/store/benchfixture_test.go`, plus the store's real
Source/entity query suite and committed P95 budgets. It gates schema-freeze changes
(ADR-0011 §2) and arbitrates the SQLite-driver choice without a separate benchmark
package.

## 9. CI pipeline — what blocks merge

| Stage | Command | Blocks merge |
|---|---|---|
| 1 vet/lint | `go vet ./...` + gofmt check | yes |
| 2 unit + integration + conformance | `go test ./...` | yes |
| 3 FFmpeg integration | pinned-FFmpeg container: `go test -tags ffmpeg ./...` | yes |
| 4 wire snapshots | `go test ./internal/conformance -run 'Test(SchemaManifest|NestedSchema|WireContract)$'` | yes |
| 5 I/O smoke | `make io-gate` plus release `io_e2e` journeys | yes in their respective lanes |
| 6 bench gate | `make bench` | yes for schema-freeze changes and release gate |
| 7 gate parity | `tools/gate/gate.sh` (same script the Stop hook runs) + `tools/gate/gate.sh --selftest` + orient line-budget check | yes |

**Non-vacuity assertion (resolution, HIGH; corrected by V-6):** a mismatched `-tags` or
`-run` value makes `go test` exit 0 having selected zero tests — a gate that silently
enforces nothing. Every tagged or `-run`-filtered CI stage (3, 4, 5, 6) and the gate
runner itself pipe `go test -json` through a check asserting **≥ 1 test ran** — counting
only `pass`/`fail` events **that carry a `"Test"` field**. (A zero-test selection still
emits a package-level `{"Action":"pass","Package":…}` event with no `Test` field, so
matching bare `"Action":"pass"` is itself vacuous — the exact failure mode this assertion
exists to kill.) Zero-test selection fails the stage. **Self-test (V-6):** CI stage 7
additionally runs `tools/gate/gate.sh --selftest`, which feeds the runner a deliberately
non-matching `-run` pattern and asserts the non-vacuity check trips — the tripwire is
itself tripwired.

Agent tooling stays thin (frozen constraint 6): the gate runner invokes the conformance
package (`TestArch`, `TestVocab`, and the three wire-snapshot drift tests above) rather than
re-implementing those checks.
