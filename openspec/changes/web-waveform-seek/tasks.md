## 1. Server

- [ ] 1.1 Advertise `assets.waveform` for every audio-class playback decision
- [ ] 1.2 Generate a missing waveform on demand in the asset route (single-flight, bounded)
- [ ] 1.3 Go tests for both

## 2. Web

- [ ] 2.1 `waveform` on `PlaybackAssets`; peak loader with per-Media cache and next-track prefetch
- [ ] 2.2 Waveform seek bar in the player transport: played/unplayed, plain-bar fallback, 375px
- [ ] 2.3 vitest: peaks drawn, fallback, slider role kept, no reset between tracks

## 3. Follow-ups (not in this change)

- [ ] 3.1 Waveform in the docked mini player's progress strip
- [ ] 3.2 Native clients

## 4. Gate and archive

- [ ] 4.1 `make gate`
- [ ] 4.2 Archive the change
