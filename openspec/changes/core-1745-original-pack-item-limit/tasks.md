## 1. Core

- [x] 1.1 `offlinepack.ErrOriginalItemTooLarge` for a video item over the bound at Original; `Service.AdmitBuild` runs the check without queueing or reading bytes
- [x] 1.2 `createOfflinePack` refuses the request before both the background and the interactive job are queued; message pinned as INVALID
- [x] 1.3 Tests: over-limit Original refused up front, exactly 4 GiB admitted, a transcoded quality of an over-limit source still packs and queues
