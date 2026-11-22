## ADDED Requirements

### Requirement: The player remembers mute and volume per device

The web player SHALL start from the muted state and volume the listener last set on this device,
for video and music alike. Changing mute or volume in the transport SHALL update that memory, and
unmuting SHALL clear the remembered mute. A store that cannot be read or written SHALL leave the
player at full volume, unmuted.

#### Scenario: A mute outlasts the visit

- **WHEN** the listener mutes the player and opens the player again on the same device
- **THEN** the new player starts muted at the volume it was left at

#### Scenario: Unmuting clears the memory

- **WHEN** the listener unmutes a remembered mute and opens the player again
- **THEN** the new player starts unmuted
