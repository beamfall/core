## 1. Policy

- [x] 1.1 `transcode.hardware` key, default on; old key ignored
- [x] 1.2 Codec `hevc` stored as Automatic; Console offers Automatic and H.264 only
- [x] 1.3 Runner encode policy: hardware off, H.264 only, quality; applied live and at startup
- [x] 1.4 Software automatic cap while hardware is off

## 2. Encode

- [x] 2.1 Quality arguments for libx264 and each hardware encoder
- [x] 2.2 Tests: FFmpeg argv for hardware off, H.264 only, each quality; live apply; cap

## 3. Console

- [x] 3.1 Help text for hardware, codec and quality

## 4. Landing

- [x] 4.1 Codex review, gate, land
