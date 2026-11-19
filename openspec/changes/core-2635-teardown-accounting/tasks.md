## 1. Playback teardown

- [x] Release the transcode slot after encoder and reader teardown and keep the
  detached session's lease through directory cleanup.
- [x] Verify that a blocked removal keeps the lease while a new encoder takes
  the slot, and that a failed removal retains the lease through retry.
