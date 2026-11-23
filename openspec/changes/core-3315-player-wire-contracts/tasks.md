## 1. Core

- [x] 1.1 Add `CapabilityPlayerControl` to `internal/capabilities/clientcompat.go`
- [x] 1.2 Gate `playerDisplays`, `availableDisplays`, `controlRendererSession` and `partyScreenAddDisplay` on `player-control`
- [x] 1.3 Graph test: an older controller (no `player-control`) gets unchanged listings and refusals

## 2. Contract

- [x] 2.1 Add `contracts/player-profile/v1/fixtures.json` and `README.md`
- [x] 2.2 Conformance test decoding each fixture strictly and re-encoding it identically
- [x] 2.3 Register the `player-profile-wire` boundary after the consumers vendor the fixtures

## 3. SDKs

- [ ] 3.1 BeamfallKit decoders and `PlayerProfileWireConformanceTests` in `beamfall-apple-ui`
- [ ] 3.2 `:kit` decoders and a fixture conformance test in `beamfall-android-ui`
