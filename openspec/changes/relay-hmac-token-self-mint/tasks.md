## 1. Token minting

- [x] 1.1 Add the v2 minter in `internal/remoteaccess/relaytoken.go` with injectable clock and nonce source
- [x] 1.2 Vendor `hmac-token-v2-vectors.json` byte-identical and replay every vector

## 2. Carrier

- [x] 2.1 Read the shared secret from the relay-token file and mint a fresh token in every `Connect`
- [x] 2.2 Keep a superseded generation's `Carry` exit from changing the relay state
- [x] 2.3 Redact the secret and minted tokens from errors and relay state
- [x] 2.4 Rotate to a same-identity replacement carrier every 2 minutes in `runRelayCarrier`

## 3. Verify and close

- [x] 3.1 Focused `internal/remoteaccess` and `internal/app` relay tests, `go vet`
- [ ] 3.2 Post-merge: `beamfall-relay/script/relay-core-e2e.sh` including restart and hold-past-TTL legs
- [ ] 3.3 Run `openspec archive relay-hmac-token-self-mint` after landing, before completing CORE-0739
