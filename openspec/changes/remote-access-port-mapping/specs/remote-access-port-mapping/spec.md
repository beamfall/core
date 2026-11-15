## ADDED Requirements

### Requirement: Opt-in automatic port mapping

The server SHALL ask the gateway for a TCP port mapping only when the operator has turned on
`remoteaccess.port_mapping_enabled`. The setting SHALL be stored in `server_config` and SHALL
default to off. The server SHALL try UPnP IGD first and NAT-PMP second, and SHALL map its listening
port.

#### Scenario: Toggle on

- **WHEN** an admin turns port mapping on and the gateway grants the mapping
- **THEN** the Console shows the state `mapped`, the protocol, the external port, the external
  address and the lease

#### Scenario: Default

- **WHEN** the setting has never been written
- **THEN** the server sends no port-mapping request

#### Scenario: Setting survives restart

- **WHEN** the server starts with the setting on
- **THEN** it requests the mapping without operator action

### Requirement: Lease renewal and expiry

The server SHALL renew a granted mapping at half its lease. When a renewal fails, the server SHALL
retry with bounded backoff and SHALL keep reporting the mapping as `mapped` until the lease runs
out. When the lease runs out without a successful renewal, the state SHALL become `failed` with the
reason `lease_expired`.

#### Scenario: Renewal

- **WHEN** half the lease has passed
- **THEN** the server requests the mapping again and the lease restarts

#### Scenario: Expiry

- **WHEN** every renewal fails until the lease ends
- **THEN** the state is `failed` with reason `lease_expired`

### Requirement: Gateway refusal and absence

When no gateway answers, the state SHALL be `failed` with reason `no_gateway`. When the gateway
refuses the request, the state SHALL be `failed` with reason `gateway_refused` and the gateway's
code. Other gateway errors SHALL report `gateway_error`.

#### Scenario: Refused

- **WHEN** the gateway answers the mapping request with a refusal
- **THEN** the state is `failed` with reason `gateway_refused`

### Requirement: Removal on disable and shutdown

Turning the setting off SHALL delete the mapping from the gateway. Server shutdown SHALL delete
the mapping and SHALL give up after a bounded timeout.

#### Scenario: Disable

- **WHEN** an admin turns port mapping off
- **THEN** the server deletes the mapping and the state is `disabled`

#### Scenario: Silent gateway at shutdown

- **WHEN** the gateway does not answer the delete during shutdown
- **THEN** shutdown continues once the timeout passes

### Requirement: Reachability self-test

The server SHALL test each remote path by sending only a TLS ClientHello whose ALPN carries a
one-time nonce, and SHALL count a path as reachable only when its own listener receives that nonce.
The probe SHALL NOT send any request, library data or credential. The relay path SHALL be tested
through the relay's public host, so the relay forwards the probe byte-blind.

#### Scenario: Direct path reachable

- **WHEN** the probe to the mapped external address reaches the server's listener
- **THEN** the direct verdict is reachable

#### Scenario: Carrier-grade NAT

- **WHEN** the gateway reports a private or shared (100.64.0.0/10) external address
- **THEN** the direct verdict is unreachable with reason `double_nat`

#### Scenario: Something else answers

- **WHEN** a listener that is not this server completes or rejects the handshake
- **THEN** the verdict is unreachable with reason `wrong_responder`

### Requirement: Reachability reason codes

Each unreachable path SHALL carry one reason code: `not_mapped`, `double_nat`,
`connection_refused`, `timeout`, `wrong_responder`, `relay_not_configured`, `relay_disconnected`
or `probe_error`.

#### Scenario: No mapping yet

- **WHEN** the reachability check runs while no mapping is held
- **THEN** the direct verdict is unreachable with reason `not_mapped`

### Requirement: Audited admin mutations

`setRemoteAccessPortMapping` and `checkRemoteAccessReachability` SHALL require the admin role. The
toggle SHALL record a `config_update` activity row.

#### Scenario: Non-admin

- **WHEN** a non-admin session calls `setRemoteAccessPortMapping`
- **THEN** the call is rejected and the setting is unchanged
