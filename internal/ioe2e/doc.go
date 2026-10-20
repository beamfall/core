// Package ioe2e holds the Core Server I/O integration + E2E journeys: real
// ffmpeg operations over real generated media, and lifecycle happy-paths driven
// in-process through the app harness (internal/apptest).
//
// Coverage is split across two build tags so the default `go test ./...` stays
// fast and ffmpeg-free (neither tag set):
//
//   - //go:build io      — the fast tier: deterministic probe + loopback-double
//     execution (ffmpeg probe over fixtures + egress emitters against httptest
//     doubles). Runs with `-tags io` (script/io-e2e.sh fast / make io-gate).
//   - //go:build io_e2e  — the slow tier: real transcode/audio/image ffmpeg ops
//     and lifecycle happy-paths driven through the apptest harness. Runs with
//     `-tags io_e2e` (script/io-e2e.sh journeys).
//
// Shared helpers (newRunner/fixtures/ctx) carry `//go:build io || io_e2e` so
// both tiers compile. Each test carries an `// io:<id>` marker that the io-ops
// ledger (cmd/ioe2e) reconciles against the code-derived ffmpeg ops and the
// testing/io-ops.yaml egress/fs ledger.
//
// Plan: docs/plans/core-server-io-integration-e2e-2026-06-21.md (D5/D6/F4);
// fast-tier split: roadmap E2E-IO-FAST-TIER.
//
// This file is intentionally untagged so the package always has a buildable
// (empty) compilation unit — the journeys themselves are all tag-gated.
package ioe2e
