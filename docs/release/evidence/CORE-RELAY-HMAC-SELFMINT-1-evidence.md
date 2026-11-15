# CORE-RELAY-HMAC-SELFMINT-1 (CORE-0739) evidence

The relay carrier mints a fresh single-use v2 token per dial from the shared HMAC
secret in `<DataDir>/secrets/relay-token` instead of sending the secret. The
implementation landed on `main` in 3b1697cc. This change verifies it against the
ticket's acceptance list and moves the `runRelayCarrier` doc comment back onto
its function (it had drifted above `relayBackendListener`).

- Base head: `82ca8305d70001e97812e968d77dddac32e83bb9` (origin/main), plus the
  doc-comment move in `internal/app/app.go` in the working tree
- Run: 2026-11-15T04:48Z UTC, go1.27.4 darwin/arm64, `GOWORK=off GOTOOLCHAIN=local`
- Vendored vectors: `internal/remoteaccess/testdata/relay_hmac_token_v2_vectors.json`
  is byte-identical to beamfall-relay `origin/main:contracts/protocol/v1/hmac-token-v2-vectors.json`
  (`diff` exit 0)

| Command | rc | Result |
|---|---|---|
| `go test -race -count=1 ./internal/remoteaccess/ -run 'Relay\|HMAC\|Conformance'` | 0 | 69 PASS (tests and subtests), 0 FAIL |
| `go test -race -count=1 ./internal/app/ -run 'RelayCarrier'` | 0 | 6 PASS, 0 FAIL |
| `go vet ./internal/remoteaccess/ ./internal/app/` | 0 | clean |
| `gofmt -l internal/app/app.go` | 0 | no output |
| `golangci-lint run ./internal/remoteaccess/... ./internal/app/... ./internal/config/...` | 0 | 0 issues |

## Acceptance

| Item | Test |
|---|---|
| Every vendored vector reproduces | `TestRelayHMACTokenV2ConformanceVectors` (MAC input, MAC and token per vector; `bad_mac` must differ) |
| iat = whole second minus 30s, exp = iat + 5m, injected clock and nonce, hostnames normalized | `TestRelayHMACTokenV2MintUsesClockAndNonce` |
| Two consecutive `Connect` calls carry different `Hello.Token` values, neither containing the secret | `TestRelayConnectMintsFreshTokenPerDial` |
| Rotation registers a replacement at 2m before the first session ends; the first session's end causes no redial | `TestRelayCarrierRotatesAtTwoMinutesWithoutRedial` |
| A superseded session's end leaves the relay (Console) state unchanged; a failed rotation dial keeps it connected | `TestRelaySupersededCarryKeepsState`, `TestRelayFailedRotationKeepsConnectedState` |
| After an unauthorized rejection the next attempt carries a new nonce; errors and relay status never contain the secret or token | `TestRelayUnauthorizedRetryMintsNewNonceAndRedacts`, `TestRelayConnectRedactsTokenFromRelayError`, `TestRelayCarrierRetriesUnauthorizedWithBackoff` |
| A 0644 secret file is refused | `TestRelayRefusesInsecureTokenFile` |

Not covered here: the post-merge relay-core end-to-end run (restart and
hold-past-TTL legs) is an orchestrator step, not a lane criterion.
