# APPLE-COMPAT-PROFILE-PCM-TOKEN-1 (CORE-0409) evidence

Capability matching now folds ffprobe's concrete linear-PCM codec names
(`pcm_s16le`, `pcm_s24le`, `pcm_s16be`, `pcm_f32le`, `pcm_bluray`, ...) into the
wire token `pcm` through a new `audioCodecFamily`. It applies on both the
audio-envelope path (`audioCodecCap`) and the legacy `DirectCodecs` path for
audio-only items, so WAV and AIFF tracks direct-play on clients that declare `pcm`.
A-law and mu-law keep their own names. `normalizedAudioCodec` is byte-identical:
`losslessAudioStream` and the quirk canonicalizer still read its output.

- Base head: `3d4c381e6833ac21145c855879cf7949ae1ae4a5` (origin/main), plus this change
- Run: 2026-11-17T22:57Z UTC, go1.27.4 darwin/arm64, `GOWORK=off`

| Command | rc | Result |
|---|---|---|
| `go test -race -count=1 ./internal/playback/ -run 'PCM\|AudioCodecFamily\|Lossless\|AudioOnly'` | 0 | 12 PASS, 0 FAIL |
| `go test -count=1 ./internal/playback/` | 0 | ok |
| `gofmt -l internal/playback` | 0 | no output |
| `golangci-lint run ./internal/playback/` | 0 | 0 issues |

With the two call sites reverted to `normalizedAudioCodec`, the new acceptance
test fails 16 assertions, so it pins the fix.

## Acceptance

| Item | Test |
|---|---|
| A `pcm_s24le` track direct-plays against `{codec: "pcm", maxChannels: 8}` and transcodes without it (envelope and legacy, probed and probe-less) | `TestPCMTrackDirectPlaysAgainstDeclaredPCM` |
| Table over `pcm_s16le`, `pcm_s24le`, `pcm_f32le`, `pcm_bluray` asserts the `pcm` family; companded PCM stays distinct | `TestAudioCodecFamilyFoldsLinearPCM` |
| Existing normalization tests unchanged | `TestLosslessAudioStream`, quirks canonicalizer tests pass unedited |
