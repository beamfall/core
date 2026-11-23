# Party stage screen eligibility and player display joining

Ticket: CORE-3313 (`queue:core:main`), implementing ADR-0240 D10 (amends ADR-0237 §2.4, core server only).

## Why

Party stage screens (`internal/graph/graph_party.go`, `internal/store/party.go`) currently read
through the host's visibility without binding `partyStage` reads or SSE stage events to the
calling Session's own screen registration, and without filtering the current entry or ticker rows
against each screen's live scope. Player displays (ADR-0240 D1–D9) also need to be offered to the
host's Screens list, joined as followers, allowed to lead only when eligible for the current entry,
shielded from conflicting D4 transport commands while joined (except `stop`), and automatically
released on party end, host removal, controller `stop`, controller removal, or binding change.

## What Changes

- **Schema & store (`events` migration `0032_party_screen_eligibility.sql`)**:
  - Add `auth_session_id`, `device_id`, `binding_generation`, and `chosen_lead` columns to
    `party_screen` and bump `schemaFreezeEventsMigrations`.
  - Add `OutboxAggregateParty` (`"party"`) / `PartyStageEventKind` (`"party.stage"`) for per-screen
    SSE delivery bound to `auth_session_id` and `profile_id`.
- **Session-bound stage reads & events (ADR-0240 D10)**:
  - Require `screenID` on `partyStage` and serve it (and `?screenID=` / `party.stage` SSE delivery
    and `partyScreenReport`) only when `screenID` belongs to the caller's own `SessionID` (a
    Session may own several registrations, each keeping its own flags). Unknown or unowned
    registrations receive the non-leaking `"screen not found"` refusal.
- **Per-screen eligibility & re-lock/switchProfile cleanup (ADR-0240 D10, ADR-0004 §4)**:
  - Evaluate each joined/stage screen's current Session scope on every `partyStage` read and stage
    event: `current` picture/stream require `PlaybackFilter()`; `upNext` ticker titles/artwork
    require `LibraryFilter()`. Ineligible `current` entries render as `"Up next: <participant
    display name>"` (`ineligible: true`, `label`), with `mediaID`, `title`, `subtitle`,
    `artworkURL`, `durationSeconds`, and `playback` cleared; ineligible `upNext` rows show the
    participant name only.
  - On `relock` (`SetSessionRevealed(..., false)`) and `switchProfile` (`SetSessionProfile*`),
    cancel restricted/session playback grants in `stream_token_ledger` and detach/terminate
    transcodes so restricted stage metadata and playback grants do not outlive re-lock or switch.
  - Allow joined player displays with walk-up off to read/play the current eligible party entry
    while joined and the host remains a controller.
- **Host Screens list, joining, lead eligibility, and Library coverage (ADR-0240 D10)**:
  - Expose controlled player displays (`IsPlayerController`) on `PartySessionInfo.availableDisplays`
    with presence and party Library coverage (`coversLibraries`, `missingLibraryIDs`).
  - Add host mutations `partyScreenAddDisplay` (joins a controlled display as a follower and reports
    `coversLibraries` / `missingLibraryIDs` on the join result) and `partyScreenLead` (chooses lead
    only when the screen can play the current entry; `markPartyLead` falls back to pop-out when a
    chosen lead cannot play the current entry).
- **Control while joined & release triggers (ADR-0240 D10)**:
  - Re-check `IsPlayerController` and `playerBindingGeneration` before pushing party changes to
    joined player displays and lazily on reads/party writes.
  - Refuse D4 `controlRendererSession` commands to a display in a party with typed `CONFLICT` /
    `DISPLAY_IN_PARTY` (`"display in party"`) for controllers (non-controllers still receive
    `"unknown session"`), except `stop`, which commits and removes the display from the party.
  - Release joined displays back to ready when the party ends, the host drops the screen, a
    controller sends `stop`, the host leaves the controllers list (eagerly on `SetPlayerControllers`
    and lazily on next party change/read), or the owner rebinds/revokes the display.
  - Enforce that guests never get a stage screen or D4 command.

## Non-goals

- Web UI (`HostLive` `PartyScreens`, `PartyStagePage`) and native Apple/Android client changes.
- Publishing a new versioned wire contract artifact or modifying `contracts/registry.json`.
