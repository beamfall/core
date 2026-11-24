# Player playback attribution to originating Profile and engagement silence

Ticket: CORE-3312 (`queue:core:main`), implementing ADR-0240 D5 and D8 (core server only).

## Why

With Player Profiles, durable Device bindings, and cross-Profile Renderer Session control in place
(CORE-3310, CORE-3311), playback driven on a shared Player display must attribute watch state,
resume position, play history, and scrobbling to the household Profile that commanded it rather
than to the shared display's Player Profile, while walk-up plays and Ambient Display remain
engagement-silent.

## What Changes

- **Server-bound originator recording and playback grant binding (ADR-0240 D5)**:
  - Events migration `0032_player_playback_attribution.sql` extending `player_commanded_media` with
    server-recorded `originator_profile_id`, `originator_session_id`, `player_binding_generation`,
    and `active_for_attribution`, and adding `playback_grant` and `playback_grant_report`.
  - When `controlRendererSession` accepts `play-media` for a Player display, the server records the
    caller's Profile and Session as the entry's originator and resolves the originator's resume
    position onto `play-media` when `positionMs` is omitted. Transport commands by other controllers
    never change the originator.
  - When the entry becomes current (`playbackInfo`), the server re-checks becoming-current playback
    authorization (and originator existence), resolves the originator's resume timeline, and issues
    a `playback_grant` (`grantID` on `InfoResponse` / `v1PlaybackInfoResponse` and `streamtoken.Grant`)
    bound to the originator and current `player_binding_generation`.
- **Idempotent and generation/state-guarded report handling (ADR-0240 D5)**:
  - `reportPlayback` and `PUT /v1/media/{mediaID}/progress` accept optional `grantID` and `sequence`
    arguments (never accepting a `profileID` from the display).
  - Player display reports are deduplicated per `(grantID, sequence)` and dropped when the grant
    has ended, was cancelled, expired, or belongs to an older `player_binding_generation`.
- **Originator engagement, consent, deletion, and Player Profile isolation (ADR-0240 D5, D8)**:
  - Accepted reports write `playback_progress`, engagement history, SSE invalidations, and
    ADR-0049 scrobble transitions under the originator Profile's identity and consent settings.
  - If the originator Profile is deleted, an in-flight item finishes on its grant and records nothing,
    while pending commanded entries for that originator are skipped when they would become current.
  - Walk-up plays and Ambient Display on a Player display (both visual and audio axes) record no
    engagement, and core engagement writers (`UpsertPlaybackProgressWithProvenance`, etc.) refuse
    to write rows under any `player` Profile ID.

## Non-goals

- Watch Party per-screen stage eligibility and household-Profile party-entry attribution (ADR-0240 D10).
- Web Console UI, Web Player UI, and native clients.
- Publishing a new versioned wire contract or editing `contracts/registry.json`.
