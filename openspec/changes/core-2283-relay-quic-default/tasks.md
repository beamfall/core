## 1. Client

- [x] 1.1 `config.RelayTransportAuto` is the default; `tcp` and `quic` remain
- [x] 1.2 `RelayClient.Connect`: QUIC first, TLS/TCP fallback with a fresh token, 10 minute hold-off
- [x] 1.3 The carrier loop logs the transport in use

## 2. Tests

- [x] 2.1 A client that cannot reach the relay over UDP still connects, with a different token
- [x] 2.2 Hold-off, relay refusal over QUIC, explicit transports do not fall back
- [x] 2.3 Config default and validation

## 3. Measurement

- [x] 3.1 Harness driver honours `CARRIER_TRANSPORT`; numbers recorded in beamfall-relay `docs/relay-latency-budget.md`
