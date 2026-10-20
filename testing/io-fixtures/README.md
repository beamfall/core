# `testing/io-fixtures/` — Core Server I/O test fixtures

Fixtures for the Core Server I/O integration + E2E track
(plan, decision **D3**).

## Layout

| Path | Committed? | What |
|---|---|---|
| `generated/` | **No** (gitignored) | Tiny real lavfi media (h264/aac/flac/mpegts…), regenerated on demand. The bulk: probe, FrameGrab, loudness, waveform, silence, transcode-source. |
| `generated/manifest.json` | No | logical-name → file + **external sha256 digest** (feeds the D6 fingerprint cross-check) + codec/duration. |
| _quirk samples_ (chapters/PGS/DV/HDR10+) | **Yes**, later | Separate committed build script per D3 — needs `dovi_tool`/`libass` (hardening #6, verify first). Not here yet. |

## Regenerate (for agents)

```sh
script/io-e2e.sh doctor     # prove ffmpeg/lavfi/libx264 work first (fail-loud)
script/io-e2e.sh fixtures   # (re)generate generated/ + manifest.json (idempotent)
make fixtures               # same, via Make
```

Generation is deterministic in bytes for a pinned ffmpeg **major**; ffmpeg version drift can shift
output (D3 caveat) — `doctor` reports the version. All fixtures are stripped with `ffmpeg
-map_metadata -1` (hardening #8) so committed/inspected bytes carry no encoder/version/timestamp
metadata.

## License

Everything here is **synthetic** (ffmpeg `lavfi` sources) — no third-party content, license-clean
(D3). Never add a commercial rip; if a real sample is ever needed, it must be CC0/self-authored.
