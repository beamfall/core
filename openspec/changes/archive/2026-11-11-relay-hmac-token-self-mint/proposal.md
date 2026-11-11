# Self-mint a single-use v2 carrier token per relay dial

Ticket: CORE-0739 (`queue:core:main`).

## Why

The relay carrier reads `<DataDir>/secrets/relay-token` and presents its raw bytes as the hello
token on every dial. relayd's default verifier (`--token-mode hmac`) accepts only single-use
"Carrier token v2" tokens with a signed lifetime of at most five minutes, so every dial is
rejected with `unauthorized`. The shared secret also crosses the wire on every dial.

## What Changes

- The relay-token file holds the operator's shared HMAC secret, the same value as relayd
  `--token-file`. It is still refused when group/other-accessible or empty.
- Every `RelayClient.Connect` mints a fresh v2 token in memory from the relay's published
  "Carrier token v2" spec: a 16-byte `crypto/rand` nonce, `issued_at` = whole-second now minus
  30s, `expires_at` = `issued_at` + 5m, HMAC-SHA256 over the normalized, sorted hostnames sent in
  the hello. The secret never goes on the wire and no token is stored or re-sent.
- The carrier loop dials a same-identity replacement carrier every 2 minutes while the current
  session keeps draining. A superseded session ending is neither a failure nor a redial trigger
  and does not change the Console relay state.
- An `unauthorized` rejection stays retryable with the existing backoff; the next attempt mints a
  new token.
- Error text, logs and the Console relay state redact the secret and every minted token.
- The relay's `hmac-token-v2-vectors.json` is vendored byte-identical as
  `internal/remoteaccess/testdata/relay_hmac_token_v2_vectors.json` with a conformance test.

## Non-goals

- Changing the hello frame layout or `testdata/relay_vectors_v1.json`.
- Supporting relayd's development-only `--token-mode static`, which expects the raw secret.
- Enforcing relayd's secret strength rules in core; relayd refuses a weak secret at startup.

## Failure modes

- A relay running development-only static token mode rejects the minted token; the operator runs
  relayd in the default `hmac` mode.
- A home-server clock more than 30s ahead of a relay without issued-at leeway, or more than 90s
  ahead of a relay with the 60s leeway, is rejected as `unauthorized` and retried with backoff.
- A failed rotation dial is logged and retried with backoff while the current session carries;
  if the relay closes that session at token expiry, the carrier redials as for any loss.

## Rollback

Revert the change. No stored state depends on it; the secret file format is unchanged.

## Capabilities

### New Capabilities

- `relay-carrier-token`: how the home server authenticates and rotates its relay carrier.

### Modified Capabilities

None.
