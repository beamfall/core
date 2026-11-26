## 1. Implementation

- [x] Draft OpenSpec change `core-0366-music-local-artwork` and validate with `openspec validate --all --strict`.
- [x] Resolve direct unordered container folder posters (`cover`/`folder`/`poster`) in `internal/scan` (including disc subfolders inside library root), apply them to member tracks as well as the container Entity, and run Media-level local artwork before auto-identification.
- [x] Protect audio `thumb` slot in `internal/identify` when `poster` is locally locked or embedded, clear stale unlocked non-manual provider-imported audio `thumb` under the directory lock (`local`, `embedded`, `identify`, and `sweep`), record dominant color for embedded covers, and skip video `FrameGrab` for audio without embedded artwork in `internal/artwork/artwork.go`.
- [x] Add focused unit and integration tests covering MP3 `APIC` / M4A `covr` with no MBID and album `cover.jpg`/`folder.jpg` precedence over embedded art and CAA.
- [x] Run `gofmt`, `go vet`, `golangci-lint`, focused Go tests, `openspec validate --all --strict`, and `codex exec --sandbox read-only` review on `wave/core-0366`.
