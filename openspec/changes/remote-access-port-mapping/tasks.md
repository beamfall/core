# Tasks (CORE-1969)

- [ ] 1. `internal/remoteaccess`: NAT-PMP and UPnP IGD clients behind a `Gateway` interface
- [ ] 2. `PortMapper`: map, renew at half lease, backoff, expiry, delete on disable, bounded close
- [ ] 3. Reachability probe (ALPN nonce ClientHello) and the listener-side responder
- [ ] 4. Store setting `remoteaccess.port_mapping_enabled` in `server_config`
- [ ] 5. Graph: `RemoteAccessInfo.portMapping` and `.reachability`; admin mutations, audit
- [ ] 6. App wiring: boot, TLS listener wrap, bounded removal on shutdown
- [ ] 7. Console Remote access card, strings, tests
- [ ] 8. Fake-gateway unit tests for map, renew, expiry and refusal
- [ ] 9. Publish the wire contract with the next batch; archive this change after landing
