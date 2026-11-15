## 1. Mint and normalize

- [x] 1.1 `randomCode` in `internal/pairing/pairing.go` encodes with Crockford base32
- [x] 1.2 `normalizeCode` drops spaces and hyphens, upper-cases and folds `O`→`0`, `I`/`L`→`1`

## 2. Tests

- [x] 2.1 Minted codes are 8 characters with no `I`, `L`, `O` or `U`
- [x] 2.2 Folding cases, and enrollment with a hyphenated look-alike reading

## 3. Verify and close

- [ ] 3.1 `go test ./internal/pairing/ ./internal/server/ ./cmd/beamfall/`
- [ ] 3.2 wsl preflight, then `corvint-tasks gate run` on the landing head
- [ ] 3.3 `openspec archive core-2132-pairing-code-alphabet` after landing, before completing CORE-2132
