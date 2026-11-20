## ADDED Requirements

### Requirement: Fenced playback library drain

Playback SHALL close admission for one library and wait for its playback-owned resources to be released. A successful drain fence SHALL be required to reopen that library.

#### Scenario: A decision crosses a drain boundary

- **WHEN** playback-info or live HLS starts before a library closes and resolves that library after it reopens
- **THEN** the old request starts no source probe and publishes no live grants

#### Scenario: A drain must be retried

- **WHEN** cancellation or cleanup failure interrupts a drain
- **THEN** admission stays closed and a retry requires the same fence

#### Scenario: Another library is active

- **WHEN** a library drains while another library or root reconciliation is active
- **THEN** the drain waits only for work owned by its target library
