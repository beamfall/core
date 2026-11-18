## 1. Policy

- [ ] 1.1 `transcode.hardware` key, default on; old key ignored
- [ ] 1.2 Codec `hevc` stored as Automatic; Console offers Automatic and H.264 only
- [ ] 1.3 Runner encode policy: hardware off, H.264 only, quality; applied live and at startup
- [ ] 1.4 Software automatic cap while hardware is off

## 2. Encode

- [ ] 2.1 Quality arguments for libx264 and each hardware encoder
- [ ] 2.2 Tests: FFmpeg argv for hardware off, H.264 only, each quality; live apply; cap

## 3. Console

- [ ] 3.1 Help text for hardware, codec and quality

## 4. Landing

- [ ] 4.1 Codex review, gate, land
