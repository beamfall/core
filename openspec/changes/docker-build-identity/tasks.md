## 1. Image

- [x] 1.1 `capabilities.ReleaseCommit` link-time variable, preferred by the reported commit
- [x] 1.2 Dockerfile: `BEAMFALL_VERSION` / `BEAMFALL_COMMIT` args into `-ldflags`, OCI labels, `BEAMFALL_BUILD_SHA`
- [x] 1.3 Unstamped default `development+src.<source digest>`
- [x] 1.4 Tests: link-time identity reaches `Current()`, Dockerfile stamping and the unstamped label
