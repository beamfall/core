## ADDED Requirements

### Requirement: An admin remote bitrate cap bounds remote playback

The server SHALL hold a server-wide remote bitrate cap and a per-profile remote bitrate cap, each in
bits per second, where 0 means unlimited. For a playback-info request the server classifies as
remote, playback negotiation SHALL apply as the streaming ceiling the lowest non-zero value among
the client's `maxStreamingBitrate`, the server-wide cap and the requesting profile's cap. A request
the server classifies as local SHALL NOT be bound by either cap.

#### Scenario: Remote request under the server-wide cap

- **WHEN** the server-wide cap is 4 Mbit/s and a remote client that can direct play the source
  requests playback info with no ceiling of its own
- **THEN** the decision is `transcode` with reason `policy-bitrate-ceiling` and no rendition
  exceeds 4 Mbit/s

#### Scenario: The lower of the server and profile caps applies

- **WHEN** the server-wide cap is 8 Mbit/s and the requesting profile's cap is 3 Mbit/s
- **THEN** a remote request plays under 3 Mbit/s, and with the values swapped it also plays under
  3 Mbit/s

#### Scenario: A lower client ceiling keeps its own reason

- **WHEN** a remote client's own ceiling is at or below the policy cap
- **THEN** the client's ceiling applies and the reason is `bitrate-ceiling`

#### Scenario: Local playback is unaffected

- **WHEN** the same caps are set and the request comes from a loopback or private-network address
- **THEN** the source is direct played as it would be with no cap

### Requirement: Remote bitrate caps are admin-only and validated

The server-wide and per-profile remote bitrate caps SHALL be writable only through the admin
operations that already gate server configuration and profile settings. A value that is not a whole
number from 0 to 1,000,000,000 SHALL be refused and SHALL leave the stored value unchanged.

#### Scenario: Non-admin write refused

- **WHEN** a viewer, or a manager without an admin session, sets either cap
- **THEN** the write is refused and the stored cap is unchanged

#### Scenario: Out-of-range value refused

- **WHEN** an admin sets either cap to -1, 1,000,000,001 or 1.5
- **THEN** the write is refused and the stored cap is unchanged
