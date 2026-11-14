## 1. Web

- [ ] 1.1 Play-queue entries carry a known `mediaClass`; nav state round-trips it
- [ ] 1.2 Narrow the player's queue to the playing item's class; learn classes from playback-info
- [ ] 1.3 Show the audio stage while loading when the queue entry's class is audio
- [ ] 1.4 Empty Oscillux visualizer slot in the audio stage
- [ ] 1.5 vitest: queue narrowing rule, audio-vs-video routing, learned video next item skipped, slot

## 2. Follow-ups (not in this change)

- [ ] 2.1 `mediaClass` on graph `SourceItem` rows (wire contract change) so Home shelves queue audio only
- [ ] 2.2 Mount the pinned Oscillux visualizer in the slot (CORE-1309 visualizer criterion)

## 3. Gate and archive

- [ ] 3.1 `make gate`
- [ ] 3.2 Archive the change
