## 1. Store

- [x] 1.1 `EntitiesRelatingTo` and `MediaForEntity` read the Entity's own plane, with the plane-scoped certification filter
- [x] 1.2 Migration 0115 drops the catalog-only media foreign key from `offline_pack_item`; trigger keeps the catalog delete cascade
- [x] 1.3 `CreateOfflinePack` refuses items whose Media is on no readable plane
- [x] 1.4 Store tests: unsealed encrypted targets resolve and freeze, sealed ones refuse, catalog cascade holds

## 2. Offline packs

- [x] 2.1 Size encrypted originals from the decrypt reader when the catalog has no size
- [x] 2.2 Tests: encrypted original pack builds without a catalog size; a short decrypt stream is refused

## 3. Film & TV pack reader

- [x] 3.1 Season and series resolvers read an unsealed encrypted Library and refuse once it is sealed

## 4. Reduced-quality packs in an encrypted Library (ADR-0235)

- [x] 4.1 High/Medium/Low packs in an encrypted Library without plaintext leaving the protected plane
- [x] 4.2 `internal/encderiv`: loopback decrypt endpoint (token, range reads, peer checks, revocation), fMP4 scan and sidx, encrypted staging and sweep
- [x] 4.3 `internal/ffmpeg`: `TranscodeToPipe` reads the endpoint over `http` only and writes fragmented MP4 to stdout, confined with no writable path; Linux requires landlock
- [x] 4.4 Offline packs stage encrypted transcodes, re-authorize the build throughout, and seal from the staged derivative; the app wires the pipe transcoder and sweeps staging at startup
- [x] 4.5 Tests: plaintext never on disk, token and peer refusals, real-FFmpeg moov-at-end transcode, staging cleanup on cancel, error and revocation, startup sweep, Seal mid-build
