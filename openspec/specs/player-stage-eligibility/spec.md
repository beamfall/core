# player-stage-eligibility Specification

## Purpose
Decides which screens a Party may put on stage and what each stage screen may show, binding stage reads and events to that screen's own registration and live scope, including Player displays (ADR-0240).

## Requirements

### Requirement: Session-bound stage reads and events

Every `partyStage` read and screen-scoped stage event subscription SHALL name the caller's
`screenID` registration and SHALL be served only when that registration belongs to the caller's
own auth `SessionID` and is active. A `SessionID` MAY hold multiple registrations, each keeping
its own flags; unknown, dropped, or unowned `screenID` requests SHALL fail with `"screen not
found"`.

#### Scenario: Stage read binds strictly to the owning Session

- **WHEN** Session A owns two screen registrations with different flags and Session B requests
  `partyStage` using one of Session A's `screenID` values
- **THEN** Session A's reads succeed for both registrations with each registration's own flags,
  while Session B's read is refused with `"screen not found"`

### Requirement: Per-screen eligibility filtering

Every `partyStage` read and stage event SHALL evaluate the screen's current Session scope: the
current entry's picture and stream facts SHALL require `PlaybackFilter()`, while `upNext` ticker
titles and artwork SHALL require `LibraryFilter()`. Ineligible `current` entries SHALL render as
`"Up next: <participant display name>"` with no title, artwork, `mediaID`, or playback anchor;
ineligible `upNext` rows SHALL show the participant name only.

#### Scenario: Ineligible screen redacts metadata while eligible screen keeps full metadata

- **WHEN** a joined screen reads a stage whose `current` entry fails `PlaybackFilter()` or whose
  `upNext` entry fails `LibraryFilter()`
- **THEN** the ineligible screen receives `"Up next: <participant display name>"` (or participant
  name only in `upNext`) with no `mediaID`, `title`, or `artworkURL`, while an eligible screen
  receives full metadata

### Requirement: Re-lock and switchProfile stage cleanup

When an ordinary joined screen calls `relock` or `switchProfile`, restricted stage metadata SHALL
clear on subsequent stage reads and the screen's affected playback grants SHALL be cancelled
(ADR-0004 §4).

#### Scenario: Relock clears restricted stage metadata and cancels playback grants

- **WHEN** a revealed joined screen holding restricted playback grants calls `relock`
- **THEN** its restricted playback grants are cancelled and subsequent `partyStage` reads clear
  restricted `current` and `upNext` metadata

### Requirement: Host Screens list, player display joining, and lead eligibility

A party host's Screens surface SHALL offer only player displays the host controls
(`IsPlayerController`), join an added display as a follower, and report on the join result whether
the display's Libraries cover the party's `libraryIDs`. A joined display MAY lead only when it can
play the current entry; otherwise the pop-out leads.

#### Scenario: Library coverage reported at join and ineligible display cannot lead

- **WHEN** a host joins a controlled display missing one of the party's Libraries while an
  ineligible entry is current
- **THEN** the join result reports `coversLibraries: false` with `missingLibraryIDs`, and choosing
  the display as lead is refused while the pop-out remains lead

### Requirement: Control while joined and D4 in-party lock

Party changes SHALL reach a joined display only while the host remains a controller. D4
`controlRendererSession` commands to a joined display SHALL return `"display in party"`
(`DISPLAY_IN_PARTY`) to controllers (and `"unknown session"` to non-controllers), except `stop`,
which removes the display from the party.

#### Scenario: D4 commands refused while in party except stop

- **WHEN** a joined player display receives a non-`stop` D4 command from a controller, then
  receives `stop`
- **THEN** the non-`stop` command returns `"display in party"`, whereas `stop` commits and releases
  the display from the party

### Requirement: Display release triggers and guest exclusion

A joined display SHALL leave the party and return to ready on party end, host removal, controller
`stop`, host controller removal (on `SetPlayerControllers` and lazily on party changes), or owner
rebind/revocation. Guests SHALL never receive a stage screen or D4 command.

#### Scenario: Controller removal and rebind release the display

- **WHEN** the host leaves the display's controllers list via `SetPlayerControllers` or the owner
  rebinds or revokes the display
- **THEN** the display is released from the party and receives no further party changes
