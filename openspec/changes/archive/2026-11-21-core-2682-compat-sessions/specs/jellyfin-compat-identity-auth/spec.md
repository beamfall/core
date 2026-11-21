## ADDED Requirements

### Requirement: Compatibility sessions list now-playing for opted-in Profiles only

The compatibility `GET /Sessions` route SHALL require the `compat.read` scope. It SHALL list only
playbacks active within the live-session activity window whose Profile is adult, enabled and opted
in to integration sign-in, and whose Media is a visible item of an eligible, mapped Library under
the catalog filter. Each entry SHALL carry an opaque session ID, the Profile's compatibility user
ID and name, the playing item's title, type and runtime, and the play position.

#### Scenario: An opted-in Profile is playing a visible item

- **WHEN** a dashboard reads `GET /Sessions` while an opted-in Profile plays a visible item
- **THEN** the response lists that playback with the item title, runtime and play position

#### Scenario: A Profile that has not opted in is playing

- **WHEN** a Profile whose integration sign-in is off is playing a visible item
- **THEN** `GET /Sessions` does not list that playback

#### Scenario: The playing item is hidden from the catalog

- **WHEN** an opted-in Profile plays an item of a Reveal-restricted, sealed or unmapped Library
- **THEN** `GET /Sessions` does not list that playback

#### Scenario: A device switched Profiles while playing the same item

- **WHEN** one device session played an item under a Profile that has not opted in, then switched
  to an opted-in Profile that is now reporting progress for the same item
- **THEN** `GET /Sessions` lists one playback, under the opted-in Profile, and drops it once the
  other Profile reports progress again, or while both Profiles' latest reports share a millisecond
  and the last reporter cannot be told apart

#### Scenario: The player stopped reporting

- **WHEN** a playback has ended or reported no activity within the activity window
- **THEN** `GET /Sessions` does not list it

### Requirement: Compatibility sessions expose no native identifiers

A compatibility session entry SHALL NOT carry native session or Device IDs, device names or
play-method fields. Its session ID SHALL be derived from the server, session and Media so that it
cannot be mapped back to a native session.

#### Scenario: A dashboard reads a session entry

- **WHEN** a dashboard reads `GET /Sessions` during an active playback
- **THEN** the entry's ID differs from the native session ID and no Device name or ID appears

### Requirement: Compatibility sessions are read-only

The compatibility route group SHALL serve no session-control route. Requests to pause, unpause,
command, or otherwise control a session, and requests to the session socket, SHALL fall through to
the uniform miss.

#### Scenario: A dashboard tries to pause a session

- **WHEN** a client sends `POST /Sessions/{id}/Playing/Pause`
- **THEN** the compatibility route group serves no handler and the request falls through

### Requirement: Compatibility item counts are filtered before counting

The compatibility `GET /Items/Counts` route SHALL require the `compat.read` scope and SHALL report,
for each item type a plugin declares, the number of visible items of that type in the eligible
Libraries, under the same filter as the catalog routes. Each count's field name SHALL be the item
type followed by `Count`.

#### Scenario: A Library holds a hidden item

- **WHEN** a dashboard reads `GET /Items/Counts` and an item is in a Reveal-restricted Library
- **THEN** the counts do not include that item
