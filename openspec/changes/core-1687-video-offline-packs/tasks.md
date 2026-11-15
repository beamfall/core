## 1. Pack service

- [x] 1.1 Accept video Media in `preflightBuildSources`; keep refusing other classes
- [x] 1.2 Add the quality values, a transcoder seam and transcode-before-hash in `BuildPack`
- [x] 1.3 Carry quality and a pre-assigned pack ID in `BuildPackRequest` and the job payload
- [x] 1.4 Add the optional `ProfileTargetResolver` interface
- [x] 1.5 Raise `DefaultBuildByteBudget` to 64 GiB

## 2. FFmpeg

- [x] 2.1 Add bounded-height, bitrate-capped MP4 presets for the three pack qualities

## 3. Graph and composition

- [x] 3.1 Accept `quality` on `createOfflinePack` and build those requests in a background job
- [x] 3.2 Wire the transcoder in the app composition root
- [x] 3.3 Adapt the Film & TV pack resolvers in `cmd/beamfall` and pin the plugin version

## 4. Tests

- [x] 4.1 Service: video accepted, image refused, transcode replaces bytes, transcode bounds, encrypted refusal
- [x] 4.2 FFmpeg preset arguments
- [x] 4.3 Graph: background build returns a stable ID, unknown quality refused
- [x] 4.4 Adapter: visibility, ordering and watched state through the plugin resolvers

## 5. Verify and close

- [x] 5.1 `openspec validate --all --strict`
- [ ] 5.2 Preflight on wsl (`script/wsl-gate.sh`)
