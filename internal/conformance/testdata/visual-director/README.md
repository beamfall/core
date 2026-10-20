# Visual Director behavior-matrix conformance pack (`visual-director-pack.v1`)

Producer-side conformance pack for the reference Visual Director (`RunDirector`) and reference
SmartPreset curve evaluator (`EvaluateCurve`) in `tools/visualsync/contract`. It asserts **semantic
behavior traces, not rendered pixels** (VSYNC-5).

- **Authoritative contract:** [`docs/architecture/visual-runtime.md`](../../../../docs/architecture/visual-runtime.md)
  — "Visual Director v1 contract" (accepted), which realizes ADR-0047 and ADR-0139 (accepted
  2026-09-27).
- **Registered boundary:** `visualsync-behavior-matrix` in `contracts/registry.json` (ADR-0122 Tier
  2; approval-required cross-repo contract artifact).
- **Executor:** `tools/visualsync/contract/visual_director_pack_test.go`
  (`TestVisualDirectorPack`). Native consumer executors (web/apple/android/windows) run the same
  fixtures in their own blocking gates — tracked in the registry `knownDrift` (VSYNC-6) until they
  land.

## Fixture format

Each `NN-<row>.fixture.json` (`visual-director-pack.v1`) carries:

- `input` — a tick-indexed `AudioFrame` sequence (never wall-clock sampled) plus run-level surface
  profile, clamp sources, candidate set, SmartPreset intensity bounds, optional hold selection, and
  the selection PRNG seed.
- `expectedTrace` — the `DirectorTrace` slice the reference Director must reproduce: cue and the
  media-time tick it landed on, selected Visual/SmartPreset, transition + boundary alignment,
  fallback reason (incl. safety-driven), composed intensity, intensity clamp, clamp ceilings, and
  ordered degradation-ladder steps.
- `smartPresetEval` — **mandatory** per-tick reference-evaluator samples covering `param0..2` plus
  the hint channels (`palette`/`motion`/`camera`/`overlay`/`intensity`), each across multiple
  tick-indexed `CurveInput`s, so two platforms cannot pass every Director trace while easing the same
  parameter differently.

## What the frozen oracle reads (encoding rules)

`RunDirector` reads only these `AudioFrame` fields: `RMS`, `Onset.{Broadband,High}`,
`Beat/Bar/Phrase.{Confidence,TimeToNextMS}`, `MediaTimeMS`, and the single `Flags` value
`live-analysis-unavailable`. Beatgrid / sync-lane matrix rows are therefore encoded **only** through
those fields — never through arbitrary unread flags (which the oracle silently ignores):

- **low-confidence / mid-playback beatgrid** → `Beat`/`Bar` confidence relative to the 0.60-enter /
  0.40-leave hysteresis (row 05 raises confidence within one run).
- **absent sync-lanes** → `Phrase == nil`; `phraseTurn` never fires.
- **misaligned sync-lanes** → `Phrase` present but low confidence / `TimeToNextMS` beyond a tick, so
  `phraseTurn` is suppressed and `bestAlignment` falls back to bar/beat/tick.

`RunDirector` composes profile/clamps **once per run** and has no per-tick profile mutation, so
"toggled mid-playback" rows (reduced-motion) are encoded as a **before/after PAIR** of single-run
fixtures (rows 13/14), asserting the difference between the two runs. `seek`/`pause`/`track-change`
are likewise the frame sequence a client feeds after the event (rows 10/11/12).

## Landing-tick tolerance

The reference oracle currently sets `LandingTick == tick` unconditionally, so there is no
independent landing prediction to tolerate yet. This pack compares `LandingTick` **exactly**, so the
assertion cannot mask drift. The ADR-0139 "±1 tick" tolerance is **reserved** for when the oracle
gains an independent landing calculation.

## RED-proving fixture (ADR-0121 §2)

`TestVisualDirectorPackRedProof` re-invokes the real pack test in a child `go test` process with
`BEAMFALL_VISUAL_DIRECTOR_INJECT_DRIFT=1`. In that child the verifier drifts the live trace/evaluator
output (standing in for a divergent platform implementation) and the RED test asserts the child gate
**exits nonzero** — proving the conformance gate itself fails on injected implementation drift, not
merely that a helper could return false. The frozen oracle is never edited.

## PCM → AudioFrame goldens (VSYNC-2 second fixture class)

The layer below the Director — raw PCM (WAV) → expected `AudioFrame` goldens with stated tolerances —
is pinned by the existing `TestAudioFrameGoldens` conformance test against
[`../visualsync/`](../visualsync/) (`sine-440.frames.golden.json`, `gap-stall-latency.frames.golden.json`).
That corpus is intentionally **not** duplicated here; this pack starts at the Director/evaluator layer
and points at those goldens so the two-layer conformance surface is complete.

## Regenerating

```sh
BEAMFALL_UPDATE_VISUAL_DIRECTOR_PACK=1 go test ./tools/visualsync/contract -run VisualDirectorPack
```

Regenerate whenever a reference-implementation change intentionally shifts a trace or evaluator
output; commit the fixture diff alongside the code change.
