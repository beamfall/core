## ADDED Requirements

### Requirement: Player Profile kind policy

The server SHALL support a third Profile kind `player` created only by the owner capability, with
immutable kind, no Reveal path, no PIN, no restricted Library grants, exclusion from Profile
pickers and `switchProfile` targets, and revocation of all bound Devices when deleted.

#### Scenario: Kind fixity and picker exclusion

- **WHEN** an owner creates a Profile with kind `player`
- **THEN** updating it to `adult` or `managed` is refused, updating an `adult` or `managed` Profile
  to `player` is refused, and `profiles` omits the `player` Profile

#### Scenario: Deleting a Player Profile revokes bound Devices

- **WHEN** a `player` Profile has bound Devices and is deleted
- **THEN** every Device bound to that Profile is marked revoked and its Sessions are ended

### Requirement: Durable Device player binding

A `Device` MAY hold an owner-set player binding targeting a `player` Profile. Setting, changing, or
clearing the binding SHALL increment `playerBindingGeneration`, end every Session of that Device,
and cancel its outstanding playback grants and pending session-control commands; every Session
issuance or renewal path for a bound Device SHALL issue solely for the bound `player` Profile, and
`switchProfile` from a bound Session SHALL be refused.

#### Scenario: Rebinding ends active Sessions and increments generation

- **WHEN** the owner binds an already-enrolled Device to a `player` Profile or changes or clears its
  binding
- **THEN** `playerBindingGeneration` increments, every Session of that Device is ended, and any
  subsequent renewal for a bound Device issues a Session on the bound `player` Profile

### Requirement: Client capability gating for bound displays

The server SHALL recognize the `player-display` client capability. On each request from a bound
Device's Session whose connection did not declare `player-display`, only `playerBinding` SHALL be
permitted; library, browse, search, layout, playback, and control operations SHALL be refused with a
typed `"update required"` error (`FORBIDDEN` with reason `UPDATE_REQUIRED`).

#### Scenario: Bound Session without player-display capability

- **WHEN** a bound Device's Session sends a request without having declared `player-display`
- **THEN** `playerBinding` returns `{bound: true, updateRequired: true}` and browse, search,
  layout, playback, and control queries fail with `"update required"`

### Requirement: Server-enforced walk-up policy

A `player` Profile SHALL store `walkUpEnabled` (default `false`). When `walkUpEnabled` is `false`,
a bound display Session SHALL be refused Library browse, search, and Layout reads, and Media reads
SHALL succeed only for Media IDs named in a `play-media` session-control command addressed to that
Session within the preceding 12 hours. When `walkUpEnabled` is `true`, normal reads SHALL succeed
under the `player` Profile's Library access list while Reveal, picker, PIN, and settings remain
refused.

#### Scenario: Walk-up off gates browse and limits Media reads to commanded items

- **WHEN** a bound display Session with `walkUpEnabled: false` queries browse, search, or layout
- **THEN** those reads are refused, while reading a Media item named by a recent `play-media`
  command to that Session succeeds
