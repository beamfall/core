# GA hardware HDR tone-mapping evidence

`evidence.json` is the release-gate artifact for the ADR-0075 GA hardware
HDR tone-mapping floor (Amendment 2026-07-26, D5/D6). It is validated by
`tools/gate/vidparity-hdr-tonemap-gate.sh`, which runs inside `make release-gate`
and **never mutates or regenerates what it validates**.

## Current status: RED, by design

Two things keep it red, and neither is a defect:

- **`GA_EVIDENCE_APPROVER` is unassigned.** A row with an empty `approver`
  cannot satisfy the floor, and no agent-produced run substitutes for the
  required hardware run.
- **The floor needs two physical hosts.** `hwAccelCandidates()` returns
  VideoToolbox only on `darwin` and never on `linux`, so the two halves of the
  2026-07-25 floor can only be produced on two different machines.

A third, narrower blocker is recorded in
`internal/ffmpeg/testdata/ffmpeg-pin-filters.txt`: no build available to this
tree ships `tonemap_videotoolbox`, so the VideoToolbox chain has no verified
filter spelling yet. The darwin half cannot pass until a darwin host with a
conforming FFmpeg records that manifest section.

Software tone-mapping is unaffected and keeps working everywhere. A transfer
class with no hardware row falls back to the **software chain**, never to a
refusal.

## Producing a row

Each row is one `(host, backend, transfer class)` measurement, taken on the real
hardware. Rows from different hosts are expected and are combined by the gate;
no single host is ever required to produce both halves.

1. On the host, run the probe for the backend and class through
   `ffmpeg.ProbeToneMap` (backend + class in `ToneMapOptions`). The probe runs
   the **same chain builder the transcoder uses**, measures the candidate
   against a pass-through control with identical device topology, and requires
   that control to fail the floors.
2. If the backend's numbers legitimately sit outside the absolute constants, run
   `ffmpeg.RunToneMapCalibration` first and pass its `Thresholds` to the probe.
   Calibrated floors derive from the **software reference leg**, never from the
   candidate, and are clamped by `max(…)` to the absolute constants (32 / 64).
   A calibration whose software reference does not itself pass those constants
   is invalid and produces no row.
3. Record the measurements into a row (see the schema in ADR-0075 D6), with:
   - `ffmpeg_pin_identity` — the `ffmpeg.lock` artifact SHA-256 on Linux; the
     resolved binary's SHA-256 on darwin (`ffmpeg.ResolveFFmpegIdentity`).
   - `certification_scope_digest` at the file level — print it with
     `tools/gate/vidparity-hdr-tonemap-gate.sh --print-digest`.
   - `passthrough_control` — always `failed-as-required` on a hardware row.
   - `software_ref_highlight` — a number on `calibrated` rows, `null` on
     `inherited` rows. `null` is the recorded value, not a missing key.
4. Add `approver` and `approved_at`, and commit. Approval **is** the named line
   plus git authorship of the commit that adds the row; there is deliberately no
   signature scheme.

## Staleness

A row goes stale when the certification-scope digest or its FFmpeg pin identity
stops matching the release under gate. There is no age dimension and no grace.
The digest covers only the paths that can change what the probe *measures* —
`internal/ffmpeg/**` (excluding `*_test.go`), `internal/playback/playback.go`,
`tools/gate/ffmpeg-tonemap-conforming.sh`, and `Dockerfile`.
