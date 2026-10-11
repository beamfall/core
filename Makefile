.PHONY: gate gate-preflight gate-race consumer-pin-freshness consumer-pin-freshness-prove-red crossrepo-conformance crossrepo-conformance-fixture crossrepo-conformance-prove-red crossrepo-conformance-interrupt-test image-buildability-check ffmpeg-tonemap-conforming vidparity-hdr-tonemap-gate store-release-waiver-check lint dev-run orient build test bench performance-scale mutation race-short race-full race-prove coverage coverage-ratchet scenario-ledger capability-manifest-check client-drm-eme-check fixtures io-doctor io-ledger io-egress-lint io-e2e io-journeys io-fast io-gate io-smoke io-live web-build web-e2e web-e2e-full web-ui-e2e-smoke web-ui-e2e-runner-test ui-catalog ui-catalog-record ui-catalog-apple-a11y-check ui-catalog-native-evidence-check release source-artifact-check release-test-gates store-release-test-gates app-store-binary-firewall-check secret-scan-release release-workflow-pins-check release-toolchain-images-check flagship-visuals-photosensitivity-gate backup-fixture-generate backup-fixture-staleness backup-restore-drill backup-fixture-rc-check release-gate release-gate-evidence fuzz fuzz-active publish-wire-contract regen-wire-goldens mgmt-manifest vuln-check relay-vector-freshness relay-vector-freshness-prove

# GATE-WEB-E2E-DEDUP-1: gate.sh keeps web-ui-e2e-smoke first so browser
# failures fail fast while every check remains under the same supervisor.
# XCONTRACT-REGISTRY-CHAIN-1: gate-entry runs relay-vector-freshness(-prove).
# gate-entry runs the seeded-red controls and relay freshness inside the fixture guard before
# anything shells out to `go`; keeping them outside would let prerequisites
# mutate fixtures before the top-level guard had taken its snapshot.
gate: web-ui-e2e-runner-test
	@GATE_WORKER_REAPS_DESCENDANTS=1 tools/gate/gate-entry.sh

# GATE-PREFLIGHT-1: the cheap fail-fast subset of `gate` — every selftest and
# static data lint, none of the Go test/race/coverage/image/web-build lanes. Run
# it before submitting a Corvint Tasks gate run so a seconds-long verdict is not paid for
# with the gate's 25-70min tail (5 of 6 LANDCHAIN rounds on 2026-07-30 died that
# way). It is NOT a merge signal and never substitutes for `gate`.
#
# `vuln-check` is deliberately absent (govulncheck + npm audit need the network
# and dominate the budget); the two remaining outer checks are static data lints
# supervised inside gate.sh. Coordination is identical to `gate` on purpose —
# same latest-wins supervisor, same repo-wide gate-capacity flock — so a
# preflight can neither overlap a real gate on this host nor add the concurrent
# load that produces false reds.
gate-preflight:
	@GATE_PREFLIGHT=1 GATE_WORKER_REAPS_DESCENDANTS=1 tools/gate/gate-entry.sh

# ADR-0122: a Core wire-contract change carries THREE consumer re-vendor
# obligations (web, Apple and Android client repositories).
# The gate step above is guarded on the diff touching a contract source; these
# targets run it unconditionally, and prove it still REDs per artifact.
consumer-pin-freshness:
	@python3 script/consumer-pin-freshness.py --all

consumer-pin-freshness-prove-red:
	@python3 script/consumer_pin_freshness_test.py

crossrepo-conformance:
	@BEAMFALL_CROSSREPO_BLOCKING_GATE=1 script/crossrepo-conformance.sh

crossrepo-conformance-fixture:
	@script/crossrepo-conformance.sh

crossrepo-conformance-prove-red:
	@script/crossrepo-conformance_test.sh

crossrepo-conformance-interrupt-test:
	@python3 script/test-process-supervisor.py \
		BEAMFALL_CROSSREPO_INTERRUPT_TEST_SUPERVISED \
		python3 script/crossrepo-conformance_interrupt_test.py

image-buildability-check:
	@tools/gate/image-buildability.sh

lint:
	@GOLANGCI_LINT_CACHE="$${TMPDIR:-/tmp}/beamfall-golangci-lint-$$(basename "$$PWD")" \
		golangci-lint run --allow-serial-runners ./...

gate-race: race-short race-prove

ffmpeg-tonemap-conforming:
	@tools/gate/ffmpeg-tonemap-conforming.sh

# ADR-0075 Amendment 2026-07-26: the GA hardware HDR tone-mapping launch gate.
# RELEASE-only, deliberately not part of `make gate`: it is a launch floor that
# stays RED until a named approver signs real two-host hardware runs, and a
# permanently red merge gate would just be routed around.
vidparity-hdr-tonemap-gate:
	@tools/gate/vidparity-hdr-tonemap-gate.sh

dev-run:
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	: "$${BEAMFALL_ADDR:=127.0.0.1:8090}"; \
	: "$${BEAMFALL_DATA_DIR:=/tmp/beamfall-core-dev}"; \
	: "$${BEAMFALL_ADMIN_PASSWORD:=beamfall-dev-admin}"; \
	BEAMFALL_ADDR="$$BEAMFALL_ADDR" \
	BEAMFALL_DATA_DIR="$$BEAMFALL_DATA_DIR" \
	BEAMFALL_ADMIN_PASSWORD="$$BEAMFALL_ADMIN_PASSWORD" \
	go run ./cmd/beamfall serve

web-e2e: web-ui-e2e-smoke

# WEB-4 Playwright launch-readiness slice. `make gate` runs this after its
# static/unit gate so real-browser regressions block the canonical gate.
web-ui-e2e-smoke:
	@python3 script/test-process-supervisor.py BEAMFALL_WEB_E2E_SETUP_PROCESS_SUPERVISED \
		/bin/sh -c 'cd "$$1" && { \
			node e2e/lib/require-pinned-chromium.mjs --package-only || npm ci --no-audit --no-fund; \
			node e2e/lib/require-pinned-chromium.mjs || { \
				npm exec playwright install chromium; \
				node e2e/lib/require-pinned-chromium.mjs; \
			}; \
		}' web-ui-e2e-setup "$(CURDIR)/internal/web/app"
	@script/web-ui-e2e-smoke.sh

# Full Playwright suite (all tests, no grep filter). Used by the release gate.
# Requires a browser runtime (Playwright Chromium); on CI install via
# `npx playwright install --with-deps chromium` before running this target.
web-e2e-full:
	@script/web-ui-e2e-full.sh

web-ui-e2e-runner-test:
	@script/web-ui-e2e-runner_test.sh

web-build:
	@npm --prefix internal/web/app run verify:build

# Feature-coverage ledger: cross-reference testing/features.yaml against the
# `feature:<id>` markers in test sources and report the (feature x target) matrix.
# Report-only; `make coverage STRICT=1` exits non-zero on uncovered cells.
coverage:
	@go run ./cmd/featurecheck $(if $(STRICT),-strict,)

# Release ratchet: fail if any assessable target drops below the checked-in
# proven-count floor. Targets whose sibling repo is absent are skipped here and
# must be covered by the native evidence release target/artifacts.
coverage-ratchet:
	@go run ./cmd/featurecheck -ratchet testing/feature-coverage-ratchet.json

scenario-ledger:
	@go run ./cmd/scenariocheck $(if $(STRICT),-strict,)

capability-manifest-check:
	@script/capability-manifest-check.sh

client-drm-eme-check:
	@script/client-drm-eme-check.sh

# Core Server I/O E2E track.
# io-doctor: prove the toolchain (fail-loud). fixtures: (re)generate the gitignored
# lavfi bulk test fixtures. Both delegate to the one agent-facing entry script.
io-doctor:
	@script/io-e2e.sh doctor

fixtures:
	@script/io-e2e.sh fixtures

# io-ledger: io-ops coverage ledger (F1/F3). Report-only by default; the Go tool
# (cmd/ioe2e) code-derives ffmpeg *Runner ops + reads testing/io-ops.yaml egress/fs
# ops and reconciles against `// io:<id>` test markers. Run `make io-ledger ARGS=check`
# to gate (non-zero on any non-ignored io-op with no green marker).
io-ledger:
	@script/io-e2e.sh ledger $(ARGS)

# io-egress-lint: the D2 egress silent-gap linter — AST-scan the production
# internal/ tree for raw http.Client constructions; fail on any whose package
# has no egress op in testing/io-ops.yaml and no //io:ignore. Also in `make gate`.
io-egress-lint:
	@go run ./cmd/ioe2e egress-lint

# io-journeys: the slow I/O tier — real ffmpeg ops + lifecycle happy-paths over
# real media (-tags io_e2e ./internal/ioe2e). Needs ffmpeg + generated fixtures
# (`make fixtures`). Deliberately NOT part of `make gate` (boots transcodes).
# Pass `go test` flags via ARGS, e.g. `make io-journeys ARGS='-run Probe -v'`.
io-journeys:
	@script/io-e2e.sh journeys $(ARGS)

# Back-compat alias used by older plans.
io-e2e: io-journeys

# io-fast: the fast I/O execution tier — deterministic probe + loopback-double
# tests (-tags io ./internal/ioe2e). Needs ffmpeg + generated fixtures for the
# probe leg; excludes the slow io_e2e lifecycle journeys. Pass `go test` flags
# via ARGS, e.g. `make io-fast ARGS='-run Egress -v'`.
io-fast:
	@script/io-e2e.sh fast $(ARGS)

# io-gate: the fast I/O inner-loop tier — the fast `io` execution tier (probe +
# loopback doubles) plus the io-ops ledger completeness check. Every io-op must
# be covered by a marker, //io:ignore'd, or documented-blocked in
# testing/io-ops.yaml. The slow io_e2e lifecycle journeys stay out (see io-journeys).
io-gate:
	@script/io-e2e.sh gate

# io-smoke: the D1 built-binary boot smoke — build cmd/beamfall as a release
# artifact, boot it on a loopback port over the conformance seed (no live
# network), prove /health 200, tear down. Fail-loud. Part of release-gate;
# kept out of the fast `gate` because it builds + boots.
io-smoke:
	@script/io-e2e.sh smoke

# io-live: the D4 live-egress drift tier — REAL outbound requests, opt-in twice
# (io_live build tag + INTEGRATION_LIVE=1). Refuses (zero egress) without the
# opt-in; NEVER part of `gate`/io-gate/io-journeys. A weekly CI drift smoke that
# points each egress client at a real endpoint via BEAMFALL_LIVE_* env. Pass go
# test flags via ARGS, e.g. `make io-live ARGS='-run UpdateCheck -v'`.
io-live:
	@script/io-e2e.sh live $(ARGS)

orient:
	@tools/orient/orient.sh

build:
	go build ./...

test:
	go test ./...

# ADR-0121: run all fuzz target seed corpora deterministically in the gate.
fuzz:
	@script/fuzz-seed.sh

# Short active fuzz pass for local/manual bug finding; nightly uses longer
# target-specific windows in .github/workflows/fuzz-nightly.yml.
fuzz-active:
	@script/fuzz-smoke.sh

# The ADR-0011 §2 schema-freeze latency gate: builds the synthetic
# 100k-Media / 10k-Entity / 2M-relation fixture (~1 min) and asserts P95
# budgets over the Source/entity query suite. Deliberately not part of
# `make gate` — too slow for the default suite.
bench:
	BEAMFALL_BENCH=1 go test ./internal/store -run TestEntityQueryLatencyBudget -v -count=1 -timeout 20m
	$(MAKE) performance-scale

# GCR-038: blocking scan/projection, direct-play, transcode-concurrency, and
# allocation floors. release-gate runs `make bench`, so these are release
# blockers rather than advisory/nightly-only measurements.
performance-scale:
	BEAMFALL_PERF=1 go test ./internal/scan -run TestPerformanceScaleScanProjectionFloor -v -count=1 -timeout 5m
	BEAMFALL_PERF=1 go test ./internal/mediahttp -run TestPerformanceScaleConcurrentDirectPlayFloor -v -count=1 -timeout 2m
	BEAMFALL_PERF=1 go test ./internal/playback -run TestPerformanceScaleConcurrentTranscodeFloor -v -count=1 -timeout 2m

include tools/gate/targets.mk

# LLM-consumable UI state catalog (ADR-0007 embedded web UI, surface `web`):
# boots `beamfall serve --seed=conformance` on an ephemeral port, drives headless
# Chromium through the embedded UI, captures images/<id>.png + compact hierarchy dumps,
# (re)writes ui-catalog/manifest.json, and diffs against ui-catalog/baselines/.
# Deliberately NOT part of default `make gate` (Playwright + a browser download
# is heavy for the default suite); CI runs it as a separate job. First run with
# no baselines: `make ui-catalog-record`. See ui-catalog/README.md for CI wiring.
ui-catalog:
	@script/ui-catalog.sh capture

# Re-bless the committed baselines from the current renders. The ONLY way a
# baseline changes — review the new images before committing.
ui-catalog-record:
	@script/ui-catalog.sh record

APPLE_CATALOG_REPO ?= ../beamfall-apple/ui-catalog
ANDROID_CATALOG_REPO ?= ../beamfall-android/ui-catalog
APPLE_RELEASE_REPO ?= ../beamfall-apple
ANDROID_RELEASE_REPO ?= ../beamfall-android
APPLE_CATALOG_MANIFEST ?= $(APPLE_CATALOG_REPO)/manifest.json
APPLE_VOICEOVER_EVIDENCE ?= $(APPLE_CATALOG_REPO)/voiceover-evidence.json

# Set ALLOW_MISSING_NATIVE_REPOS=1 to downgrade absent sibling catalog repo directories
# from hard errors to documented blockers. Intended for core-only CI and core-worktree
# agent runs where the Apple/Android repos are not checked out. The flag does NOT suppress
# artifact-gap errors from repos that ARE present.
ALLOW_MISSING_NATIVE_REPOS ?= 0

_NATIVE_MISSING_FLAG = $(if $(filter 1,$(ALLOW_MISSING_NATIVE_REPOS)),--allow-missing-repos,)

# Release mode, spelled the same way everywhere (tools/gate/gate.sh
# gate_release_mode): an explicit GATE_MODE, or the release-gate workflow itself.
_RELEASE_MODE = $(if $(filter release,$(GATE_MODE)),1,$(if $(filter release-gate,$(GITHUB_WORKFLOW)),1,))

# The missing-native-repo waiver exists for core-only CI and core-worktree agent
# runs. On a release path it is a missing-evidence waiver: native evidence would
# be reported handled while no native artifact was ever opened.
store-release-waiver-check:
	@test "$(ALLOW_MISSING_NATIVE_REPOS)" != 1 || { \
		echo "store release evidence cannot waive missing native repos (ALLOW_MISSING_NATIVE_REPOS=1)" >&2; \
		exit 1; \
	}

# APPLE-7 iOS accessibility release gate: validates the canonical iOS
# large-text/reduce-motion/high-contrast captures plus full ADR-0014 VoiceOver
# evidence rows. Override APPLE_CATALOG_* to point at an Apple catalog artifact
# set; this target only reads those paths.
ui-catalog-apple-a11y-check:
	@python3 tools/ui-catalog/apple_a11y_check.py \
		--manifest "$(APPLE_CATALOG_MANIFEST)" \
		--repo-root "$(APPLE_CATALOG_REPO)" \
		--voiceover-evidence "$(APPLE_VOICEOVER_EVIDENCE)" \
		$(_NATIVE_MISSING_FLAG)

# Native release evidence packet: validates Apple/Android UI-catalog artifacts
# committed by the client repos, then applies the stricter APPLE-7 iOS a11y gate.
ui-catalog-native-evidence-check:
	@test -z "$(_RELEASE_MODE)" || test "$(ALLOW_MISSING_NATIVE_REPOS)" != 1 || { \
		echo "release mode cannot waive missing native repos (ALLOW_MISSING_NATIVE_REPOS=1)" >&2; \
		exit 1; \
	}
	@python3 tools/ui-catalog/native_evidence_check.py \
		--apple-catalog "$(APPLE_CATALOG_REPO)" \
		--android-catalog "$(ANDROID_CATALOG_REPO)" \
		$(_NATIVE_MISSING_FLAG)
	@$(MAKE) ui-catalog-apple-a11y-check ALLOW_MISSING_NATIVE_REPOS=$(ALLOW_MISSING_NATIVE_REPOS)

# Human release checklist target. It is intentionally broader than default Core
# CI because it requires sibling native catalog artifacts.
release:
	@script/release.sh $(ARGS)

source-artifact-check:
	@script/source-artifact-check.sh $(ARGS)

# Core's release suite does not certify proprietary store binaries. Store releases
# use the strict, no-waiver `store-release-test-gates` target below.
# BKUP-10: the full crash-injection restore drill runs on the existing daily schedule —
# .github/workflows/release-gate.yml (cron 17 6 * * *) invokes this target.
release-test-gates: gate release-workflow-pins-check release-toolchain-images-check flagship-visuals-photosensitivity-gate ui-catalog backup-restore-drill

release-workflow-pins-check:
	@python3 script/check-release-workflow-pins.py
	@python3 -m unittest script/check_release_workflow_pins_test.py
	@python3 -m unittest script/release_deploy_manifests_test.py

# Release-only: resolving digest contents requires Docker and registry access.
release-toolchain-images-check:
	@python3 script/check-release-workflow-pins.py --verify-toolchain-images

app-store-binary-firewall-check:
	@for input in \
		"APPLE_IPHONE_IPA=$(APPLE_IPHONE_IPA)" \
		"APPLE_IPAD_IPA=$(APPLE_IPAD_IPA)" \
		"APPLE_TVOS_IPA=$(APPLE_TVOS_IPA)" \
		"ANDROID_MOBILE_AAB=$(ANDROID_MOBILE_AAB)" \
		"ANDROID_TV_AAB=$(ANDROID_TV_AAB)"; do \
		name=$${input%%=*}; path=$${input#*=}; \
		test -n "$$path" && test -f "$$path" || { echo "$$name must name a release archive" >&2; exit 2; }; \
	done
	@script/app-store-firewall-static-check.sh --require-repos \
		--target "apple:beamfall-iphone:$(APPLE_RELEASE_REPO)/Apps/BeamfallMobile" \
		--target "apple:beamfall-ipad:$(APPLE_RELEASE_REPO)/Apps/BeamfallPad" \
		--target "apple:beamfall-tvos:$(APPLE_RELEASE_REPO)/Apps/BeamfallTV" \
		--target "apple:beamfall-apple-policy:$(APPLE_RELEASE_REPO)/Sources/BeamfallAppStorePolicy" \
		--target "apple:beamfall-mobile:$(APPLE_RELEASE_REPO)/Sources/BeamfallMobile" \
		--target "apple:beamfall-tv:$(APPLE_RELEASE_REPO)/Sources/BeamfallTV" \
		--target "android:beamfall-android-mobile:$(ANDROID_RELEASE_REPO)/app-mobile/src/main" \
		--target "android:beamfall-android-tv:$(ANDROID_RELEASE_REPO)/app-tv/src/main" \
		--target "archive:beamfall-iphone:$(APPLE_IPHONE_IPA)" \
		--target "archive:beamfall-ipad:$(APPLE_IPAD_IPA)" \
		--target "archive:beamfall-tvos:$(APPLE_TVOS_IPA)" \
		--target "archive:beamfall-android-mobile:$(ANDROID_MOBILE_AAB)" \
		--target "archive:beamfall-android-tv:$(ANDROID_TV_AAB)"

secret-scan-release:
	@tools/gate/secret-scan.py --require-repos \
		--target "beamfall:$(CURDIR)" \
		--target "beamfall-apple:$(APPLE_RELEASE_REPO)" \
		--target "beamfall-android:$(ANDROID_RELEASE_REPO)"

# The waiver guard is the only prerequisite so it cannot run alongside the
# multi-hour work under `make -j`; the rest is invoked once the guard has passed.
store-release-test-gates: store-release-waiver-check
	@$(MAKE) release-test-gates ui-catalog-native-evidence-check app-store-binary-firewall-check secret-scan-release

flagship-visuals-photosensitivity-gate:
	@go test ./internal/conformance -run TestPhotosensitivity -count=1
	@go test ./tools/visualsync/contract -run 'Test(VisualDirectorPack|.*Photosensitivity)' -count=1

# Automated release gate: slow I/O journey tier + ioe2e release-check + full
# Playwright web suite. Blocks releasing unless all three pass.
#
# Pre-requisites (CI must install before running this target):
#   - ffmpeg (apt-get install -y ffmpeg) — required for io-journeys (real transcodes)
#   - Playwright browser: npx playwright install --with-deps chromium
#   - Fixture corpus: make fixtures (or ensure testing/io-fixtures/generated/manifest.json exists)
#
# The slow journey tier (io_e2e) runs real ffmpeg ops over generated lavfi fixtures;
# results are emitted as JUnit XML and cross-checked by `cmd/ioe2e release-check`
# to ensure every io:<id>-marked op has a green executed test. The full Playwright
# suite (web-e2e-full) runs all spec files without a grep filter.
RELEASE_ORDINAL ?= 1

backup-fixture-generate:
	@script/backup-fixtures.sh generate "$(VERSION)" "$(RELEASE_ORDINAL)"

backup-fixture-staleness:
	@script/backup-fixtures.sh staleness "$(RELEASE_ORDINAL)"

backup-restore-drill:
	@tools/gate/backup-restore-drill.sh --mode nightly

backup-fixture-rc-check: backup-fixture-staleness backup-restore-drill
	@test -f "testing/backup-fixtures/$(VERSION)/fixture.json" || { \
		echo "release fixture $(VERSION) is missing; run: make backup-fixture-generate VERSION=$(VERSION) RELEASE_ORDINAL=$(RELEASE_ORDINAL)" >&2; \
		exit 1; \
	}
	@echo "backup-fixture-generate: verified release-candidate fixture $(VERSION)"

release-gate: backup-fixture-rc-check
	@script/release-gate-evidence.sh

release-gate-evidence: release-gate

# ADR-0122 Tier 1: publish the versioned wire-contract artifact to the stable
# conformance path that clients pin (internal/conformance/wire-contract-v<N>.json).
# Stamp with the release version via VERSION= (default 1.0.0 for the initial ship).
# The unversioned drift-gate baseline (tools/wire-contract/testdata/wire-contract.json)
# is unchanged — it is the dev snapshot that TestWireContract byte-compares.
# To cut a new release artifact: make publish-wire-contract VERSION=1.2.3
#
# For a MAJOR bump, first prepare and validate every consumer repin. The new
# artifact, registry fields, and registry consumer pins must land together,
# followed by the prepared consumer commits in one coordinated publish window.
# There is no staging escape.
VERSION ?= 1.0.0
publish-wire-contract:
	@set -eu; \
		final="internal/conformance/wire-contract-v$(VERSION).json"; \
		echo "    NOTE: before a MAJOR bump, prepare every consumer repin for one coordinated publish window."; \
		echo "    There is no staging escape."; \
		if test -e "$$final"; then \
			echo "wire-contract $(VERSION) already exists; publish a new version instead of overwriting it" >&2; \
			exit 1; \
		fi; \
		tmp=$$(mktemp "internal/conformance/.wire-contract-v$(VERSION).XXXXXX"); \
		trap 'rm -f "$$tmp"' EXIT INT TERM; \
		echo "==> publishing wire-contract $(VERSION) → $$final"; \
		go run ./tools/wire-contract --version "$(VERSION)" --registry contracts/registry.json > "$$tmp"; \
		test -s "$$tmp"; \
		if ! ln "$$tmp" "$$final" 2>/dev/null; then \
			echo "wire-contract $(VERSION) already exists; publish a new version instead of overwriting it" >&2; \
			exit 1; \
		fi; \
		rm -f "$$tmp"; trap - EXIT INT TERM; \
		echo "==> done: $$final"

# Regenerate every committed wire golden after a wire-schema change.
#
# Adding a wire field or type makes four generated artifacts stale, and each one
# is only discoverable after the previous is fixed — so doing this by hand costs
# several full gate cycles:
#
#   internal/conformance/testdata/nested-schema{,-appstore}.json
#   internal/conformance/testdata/schema-manifest{,-appstore}.json
#   internal/conformance/testdata/wire-contract-appstore.json
#   tools/wire-contract/testdata/{wire-contract,field-inventory}.json
#
# Each golden has TWO build-tag variants, so the sanctioned BEAMFALL_UPDATE_GOLDEN
# in-test regeneration path runs twice. Do NOT hand-run the underlying generators
# (go run ./tools/nested-schema > ...-appstore.json): without -tags beamfall_appstore
# that writes the full non-App-Store schema into the App Store golden, silently
# removing surface suppression and breaking hard invariant 8.
regen-wire-goldens:
	@echo "==> regenerating wire goldens (untagged)"
	@BEAMFALL_UPDATE_GOLDEN=1 go test ./internal/conformance/ -count=1 \
		-run 'TestNestedSchema$$|TestSchemaManifest$$|TestWireContract$$|TestFieldInventory$$'
	@echo "==> regenerating wire goldens (-tags beamfall_appstore)"
	@BEAMFALL_UPDATE_GOLDEN=1 go test -tags beamfall_appstore ./internal/conformance/ -count=1 \
		-run 'TestNestedSchema$$|TestSchemaManifest$$|TestWireContract$$|TestFieldInventory$$'
	@echo "==> done."
	@echo "    NOTE: the pinned published contract is NOT a golden. If the wire"
	@echo "    contract itself changed, the publish-freshness gate still blocks merge"
	@echo "    until you also run: make publish-wire-contract VERSION=x.y.z"
	@echo "    For a MAJOR bump, first prepare every consumer repin for one coordinated"
	@echo "    publish window."

# ADR-0122 Tier 2: regenerate the management-surface manifest (ADR-0060/0061)
# consumed by beamfall-web's script/check-mgmt-parity.sh parity checker.
mgmt-manifest:
	@echo "==> regenerating management-surface manifest → internal/conformance/mgmt-manifest.json"
	@script/emit-mgmt-manifest.sh

# Vulnerability scans (CAUDIT-18). Measured wall-clock is ~5s locally — not
# slow. In `make gate` as of 2026-07-24: the 5 pre-existing high/critical npm
# advisories (react-router, all fixed by the react-router-dom -> react-router
# v8 migration) are resolved, so this target is green and now runs on every
# merge via the `gate:` prerequisite list (matching beamfall-relay/Makefile).
# Hosted GitHub Actions (incl. vuln-nightly.yml) is not used as a
# merge-blocking signal here (operator decision, 2026-07-22) — local
# `make gate` is the gate.
#
#   govulncheck — Go CVE scan via the Go vulnerability database.
#   npm audit    — web UI audit; fails only on NEW high/critical advisories
#                  not listed in internal/web/app/.audit-baseline.
#
# To run both: make vuln-check
vuln-check:
	@echo "==> govulncheck ./..."
	@govulncheck ./...
	@echo "==> npm audit (baseline-aware, high/critical)"
	@bash script/audit-npm.sh

# XCONTRACT-16 / ADR-0122 Amendment B §4: relay golden-vector copy freshness.
#
# beamfall-relay produces the tunnel golden vectors; this repo keeps a hand-made
# copy at internal/remoteaccess/testdata/relay_vectors_v1.json so the core gate
# stays self-contained (internal/remoteaccess/relayproto_conformance_test.go
# round-trips it). Nothing observed the producer, so a regeneration on the relay
# side left core green against a stale copy — which is exactly what happened:
# relay's file now carries 9 vectors (the ADR-0188 cache-carrier frames landed
# with MCACHE-4/MCACHE-13), core's copy still carries the 5 pre-cache ones.
#
# Both sides are therefore digest-pinned here, and the two pins differ only
# while a dated {ticket, deadline} waiver covers the gap:
#
#   * core copy edited or re-synced without re-pinning  -> red
#   * relay regenerates its vectors without a re-copy   -> red
#   * the waiver deadline passes                        -> red
#   * the two pins converge but a waiver is still recorded (stale) -> red
#
# Re-sync procedure: copy the producer file over the core copy, update BOTH
# digests below, drop the waiver, and make relayproto.go speak the new frames.
RELAY_VECTORS_CORE_COPY ?= internal/remoteaccess/testdata/relay_vectors_v1.json
RELAY_VECTORS_CORE_SHA256 ?= 9f46cdf948af68f13618da1b1a428f3aa3277d9d2a07b093cf7af496bbe55503
RELAY_VECTORS_PRODUCER_REL ?= beamfall-relay/contracts/protocol/v1/vectors.json
RELAY_VECTORS_PRODUCER ?=
RELAY_VECTORS_PRODUCER_SHA256 ?= 9f46cdf948af68f13618da1b1a428f3aa3277d9d2a07b093cf7af496bbe55503
RELAY_VECTORS_DRIFT_TICKET ?=
RELAY_VECTORS_DRIFT_DEADLINE ?=
RELAY_VECTORS_TODAY ?= $(shell date -u +%Y-%m-%d)

relay-vector-freshness:
	@copy="$(RELAY_VECTORS_CORE_COPY)"; \
	if [ ! -f "$$copy" ]; then \
		echo "relay-vector-freshness: FAIL - core copy $$copy is missing" >&2; exit 1; \
	fi; \
	got="$$(shasum -a 256 "$$copy" | awk '{print $$1}')"; \
	if [ "$$got" != "$(RELAY_VECTORS_CORE_SHA256)" ]; then \
		echo "relay-vector-freshness: FAIL - core copy digest drifted" >&2; \
		echo "  file $$copy" >&2; \
		echo "  want $(RELAY_VECTORS_CORE_SHA256)" >&2; \
		echo "  got  $$got" >&2; \
		echo "  re-syncing from beamfall-relay must also update RELAY_VECTORS_CORE_SHA256 in the Makefile" >&2; \
		exit 1; \
	fi; \
	if [ "$(RELAY_VECTORS_CORE_SHA256)" = "$(RELAY_VECTORS_PRODUCER_SHA256)" ]; then \
		if [ -n "$(RELAY_VECTORS_DRIFT_TICKET)" ]; then \
			echo "relay-vector-freshness: FAIL - stale waiver" >&2; \
			echo "  copy and producer pins are identical, so RELAY_VECTORS_DRIFT_TICKET" >&2; \
			echo "  ($(RELAY_VECTORS_DRIFT_TICKET)) no longer covers anything - clear it" >&2; \
			exit 1; \
		fi; \
	else \
		if [ -z "$(RELAY_VECTORS_DRIFT_TICKET)" ]; then \
			echo "relay-vector-freshness: FAIL - copy and producer pins differ with no recorded waiver" >&2; \
			echo "  set RELAY_VECTORS_DRIFT_TICKET + RELAY_VECTORS_DRIFT_DEADLINE, or re-sync the copy" >&2; \
			exit 1; \
		fi; \
		earliest="$$(printf '%s\n%s\n' "$(RELAY_VECTORS_TODAY)" "$(RELAY_VECTORS_DRIFT_DEADLINE)" | sort | head -1)"; \
		if [ "$$earliest" != "$(RELAY_VECTORS_TODAY)" ]; then \
			echo "relay-vector-freshness: FAIL - waiver $(RELAY_VECTORS_DRIFT_TICKET) expired on $(RELAY_VECTORS_DRIFT_DEADLINE) (today $(RELAY_VECTORS_TODAY))" >&2; \
			exit 1; \
		fi; \
	fi; \
	producer="$(RELAY_VECTORS_PRODUCER)"; \
	if [ -z "$$producer" ]; then \
		common="$$(git rev-parse --git-common-dir 2>/dev/null || echo .git)"; \
		producer="$$(cd "$$common/../.." 2>/dev/null && pwd)/$(RELAY_VECTORS_PRODUCER_REL)"; \
	fi; \
	if [ ! -f "$$producer" ]; then \
		echo "relay-vector-freshness: LOUD SKIP - producer artifact not found at $$producer" >&2; \
		echo "  the beamfall-relay sibling checkout is absent on this host, so a producer-side" >&2; \
		echo "  regeneration CANNOT be detected here. The core-copy pin above is still enforced." >&2; \
		echo "  Run this in the workspace contracts lane, where the sibling is present." >&2; \
		echo "relay-vector-freshness: core copy pinned ok (producer check SKIPPED, see above)"; \
		exit 0; \
	fi; \
	got="$$(shasum -a 256 "$$producer" | awk '{print $$1}')"; \
	if [ "$$got" != "$(RELAY_VECTORS_PRODUCER_SHA256)" ]; then \
		echo "relay-vector-freshness: FAIL - producer artifact digest drifted" >&2; \
		echo "  file $$producer" >&2; \
		echo "  want $(RELAY_VECTORS_PRODUCER_SHA256)" >&2; \
		echo "  got  $$got" >&2; \
		echo "  beamfall-relay regenerated its vectors: re-copy into $(RELAY_VECTORS_CORE_COPY) and re-pin both digests" >&2; \
		exit 1; \
	fi; \
	echo "relay-vector-freshness: ok - copy and producer both match their pins (waiver $(RELAY_VECTORS_DRIFT_TICKET) until $(RELAY_VECTORS_DRIFT_DEADLINE))"

# RED-proof for the target above. A digest check that only ever runs on good
# input proves nothing, so each rejection path is fed a deliberately bad input
# and asserted to red, and two good inputs are asserted to pass so the target
# cannot be trivially-always-red either.
relay-vector-freshness-prove:
	@set -eu; tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; trap 'exit 130' INT; trap 'exit 143' TERM; \
	printf '{}\n' >"$$tmp/core.json"; \
	printf '{"fixture":"producer"}\n' >"$$tmp/producer.json"; \
	core_sha="$$(shasum -a 256 "$$tmp/core.json" | awk '{print $$1}')"; \
	producer_sha="$$(shasum -a 256 "$$tmp/producer.json" | awk '{print $$1}')"; \
	[ -n "$$core_sha" ] && [ -n "$$producer_sha" ] && [ "$$core_sha" != "$$producer_sha" ]; \
	run() { $(MAKE) --no-print-directory relay-vector-freshness \
		RELAY_VECTORS_CORE_COPY="$$tmp/core.json" RELAY_VECTORS_CORE_SHA256="$$core_sha" \
		RELAY_VECTORS_PRODUCER="$$tmp/producer.json" RELAY_VECTORS_PRODUCER_SHA256="$$producer_sha" \
		RELAY_VECTORS_DRIFT_TICKET=FIXTURE-DRIFT RELAY_VECTORS_DRIFT_DEADLINE=2026-01-02 \
		RELAY_VECTORS_TODAY=2026-01-01 "$$@" >"$$tmp/out" 2>&1; }; \
	greens() { if ! run "$$@"; then cat "$$tmp/out" >&2; echo "relay-vector-freshness-prove: FAIL - valid fixture did not pass" >&2; exit 1; fi; }; \
	assert_output() { if ! grep -q "$$1" "$$tmp/out"; then cat "$$tmp/out" >&2; echo "relay-vector-freshness-prove: FAIL - expected diagnostic $$1" >&2; exit 1; fi; }; \
	reds() { expected="$$1"; shift; greens; if run "$$@"; then cat "$$tmp/out" >&2; echo "relay-vector-freshness-prove: FAIL - $$expected did not red" >&2; exit 1; fi; assert_output "$$expected"; }; \
	greens; assert_output 'copy and producer both match their pins'; \
	cp "$$tmp/core.json" "$$tmp/mutated.json"; printf ' ' >>"$$tmp/mutated.json"; \
	reds 'core copy digest drifted' RELAY_VECTORS_CORE_COPY="$$tmp/mutated.json"; \
	reds 'is missing' RELAY_VECTORS_CORE_COPY="$$tmp/absent.json"; \
	reds 'producer artifact digest drifted' RELAY_VECTORS_PRODUCER="$$tmp/mutated.json"; \
	reds 'expired on 2026-01-02 (today 2026-01-03)' RELAY_VECTORS_TODAY=2026-01-03; \
	reds 'expired on 2026-01-02 (today 2999-01-01)' RELAY_VECTORS_TODAY=2999-01-01; \
	reds 'no recorded waiver' RELAY_VECTORS_DRIFT_TICKET=; \
	reds 'stale waiver' RELAY_VECTORS_PRODUCER="$$tmp/core.json" RELAY_VECTORS_PRODUCER_SHA256="$$core_sha"; \
	greens RELAY_VECTORS_TODAY=2026-01-02; \
	greens RELAY_VECTORS_PRODUCER="$$tmp/core.json" RELAY_VECTORS_PRODUCER_SHA256="$$core_sha" \
		RELAY_VECTORS_DRIFT_TICKET= RELAY_VECTORS_DRIFT_DEADLINE=; \
	assert_output 'copy and producer both match their pins'; \
	greens; \
	greens RELAY_VECTORS_PRODUCER="$$tmp/absent.json"; assert_output 'LOUD SKIP'; \
	echo "relay-vector-freshness-prove: ok - isolated fixtures reach every rejection path, valid input passes"
