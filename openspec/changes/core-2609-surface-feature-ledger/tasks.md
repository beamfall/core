## 1. Registry

- [x] 1.1 Parse `unsupported:`, `tickets:` and `release:` blocks and top-level `fullParity`
- [x] 1.2 Validate surfaces, applies membership, ticket IDs, versions and full-parity owner decisions
- [x] 1.3 Add `fullParity: [web, macos, windows]` and document the fields in `testing/features.yaml`

## 2. featurecheck

- [x] 2.1 Report supported, planned, unsupported, undecided and unticketed counts per surface
- [x] 2.2 Add `-release X.Y` and `-gaps`
- [x] 2.3 Add `maxUndecided` and `maxUnticketed` to the ratchet at today's values

## 3. Verify and close

- [x] 3.1 Run `go test ./cmd/featurecheck`, gofmt, lint, the gate's featurecheck step and `openspec validate --all --strict`
