## 1. Playback teardown

- [x] Release the transcode slot and governor lease after encoder stop and
  reader revocation. Drain fragment joins before removing the directory.
- [x] Verify foreground admission during a blocked join and background
  generation during directory cleanup. Preserve retry-owned directories
  during an orphan purge.
