## 1. Server

- [x] 1.1 `CapabilityProfile.MaxStreamingBitrate`; the quirk overlay carries the viewer ceiling and applies the tighter of it and the quirk cap
- [x] 1.2 Reason `bitrate-ceiling` when the ceiling alone forced the transcode; dashboard cause `bitrate`
- [x] 1.3 Tests: decision, reason, rendition bound, ladder floor, audio-only exemption, fingerprint

## 2. Contract

- [x] 2.1 Regenerate wire goldens; publish wire contract 2.5.0 and repoint the registry
