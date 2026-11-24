# web-player-displays Specification

## Purpose
TBD - created by archiving change core-3314-web-player-displays. Update Purpose after archive.

## Requirements

### Requirement: Web UI requests player-control

The embedded Core Web UI contract SHALL request the `player-control` capability in both the
server's `EmbeddedUIContract` and the browser's `CLIENT_CONTRACT`.

#### Scenario: Web caller sees player displays

- **WHEN** a signed-in web caller who controls a bound, connected player display queries `playerDisplays`
- **THEN** the display is returned instead of an empty list

### Requirement: Console manages Player Profiles and bindings

The console SHALL let the owner create a `player` Profile, edit its controllers (All adults, include
and exclude lists) and walk-up setting, and bind or clear a Device's Player Profile with a display
name. A Player Profile's kind SHALL be shown as fixed. Turning walk-up on SHALL require confirming a
warning that anyone holding the display's remote can browse and play everything the Player Profile's
Libraries allow, with no engagement recorded.

#### Scenario: Owner lists Player Profiles

- **WHEN** the owner opens Profiles in the console
- **THEN** the console reads Player Profiles from the owner-only `playerProfiles` query, which
  returns only enabled `player` Profiles with their walk-up setting and controllers, and a non-owner
  caller gets FORBIDDEN

#### Scenario: Walk-up needs confirmation

- **WHEN** the owner turns walk-up on for a Player Profile
- **THEN** the console shows the D7 warning and calls `setPlayerWalkUp` only after confirmation

#### Scenario: Player kind is fixed

- **WHEN** the console lists a Player Profile
- **THEN** its kind is shown as Player and no kind selector or entry PIN is offered for it

### Requirement: Play on lists controlled player displays

The Play on picker SHALL list only the displays returned by `playerDisplays`, with ready, playing or
offline presence. A playing display whose item the caller may not see SHALL show "Private item". An
offline display SHALL show "Open Beamfall on <name>" and SHALL NOT be selectable.

#### Scenario: Offline display

- **WHEN** a listed display's presence is `offline`
- **THEN** the picker shows "Open Beamfall on <name>" for it and offers no play action

#### Scenario: Command a ready display

- **WHEN** the caller chooses a ready display for the current item
- **THEN** the web app sends `play-media` with `controlRendererSession` for that display's Session and retries once with the returned revision on a conflict

### Requirement: QR handle resolution

`/web/play-on?display=<handle>` SHALL resolve the handle against `playerDisplays` after sign-in. Any
handle that does not resolve SHALL show the same "display not available" message.

#### Scenario: Unknown handle

- **WHEN** the handle is unknown, revoked, unbound or not controlled by the caller
- **THEN** the page shows "display not available" and reveals nothing else

### Requirement: Player displays in party Screens

The host's party Screens list SHALL offer the session's `availableDisplays` with presence and library
coverage, add one with `partyScreenAddDisplay`, and let the host make a screen the lead with
`partyScreenLead`.

#### Scenario: Add a display

- **WHEN** the host adds an available, connected display
- **THEN** the web app calls `partyScreenAddDisplay` with its handle and the display appears in the Screens list marked as a player display
