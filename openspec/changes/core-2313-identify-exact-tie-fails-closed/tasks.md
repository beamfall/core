## 1. Server

- [x] 1.1 `bestCandidate` treats an exact top-score tie between different provider IDs as not auto-applicable and drops the popularity tie-break
- [x] 1.2 Same-ID duplicates at the top score still apply

## 2. Tests

- [x] 2.1 Identify test: two "The Office" candidates at the same score are queued, not applied, even with different popularity
- [x] 2.2 Identify test: distinct scores apply the highest; same-ID duplicates apply
