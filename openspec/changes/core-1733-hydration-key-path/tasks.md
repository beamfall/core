## 1. Hydration key

- [x] 1.1 `hydrationKey` in `internal/app/musichydration.go`: fingerprint plus plan digest, empty fingerprint stays empty
- [x] 1.2 The seam maps the plan before deciding, compares and records the key
- [x] 1.3 Scan test: moving an untagged file into a `CD2` folder updates its disc number; unchanged and fact-preserving moves write nothing
