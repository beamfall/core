# Remote control of sessions and "play on"

Ticket: CORE-1678 (`queue:core:main`), from the client parity audit of 2026-11-14.

## Why

A device can only pause, resume and skip another device's session in the same Profile. Controllers
expect seek, stop, volume, mute, audio and subtitle track selection, sending a short on-screen
message, and starting an item on another device ("play on"). Without them a phone cannot act as a
remote for a TV, and no client offers to move playback to another device.

## What Changes

- Widen the closed session-control vocabulary to `pause`, `resume`, `stop`, `skip-media`,
  `skip-track` (each skip with a `next` or `previous` direction), `seek`, `set-volume`, `mute`,
  `unmute`, `select-audio-track`, `select-subtitle-track`, `play-media` and `display-message`.
- Each verb takes exactly its own arguments, validated with fixed bounds in the store before any
  outbox write. The arguments travel in the existing `session.control` event payload as additive,
  omitted-when-unset fields.
- GraphQL `controlSession` takes the arguments as optional variables (`direction`, `positionMs`,
  `volume`, `trackID`, `mediaID`, `text`); its result shape is unchanged.
- `play-media` targets an idle or playing device session in the caller's Profile. The Media must be
  visible to the caller, and it becomes the event's reveal anchor, so a subscriber that cannot see
  it never receives the command.
- `POST /v1/playback-control` keeps its request body; it accepts the argument-free new verbs and
  answers 400 for verbs that need arguments.
- The web app gains a remote panel for the Profile's other sessions, a "Play on" action, and a
  receiver that applies commands addressed to its own session.

## Authorization

Per Profile and fail-closed, applied to every verb: the target must be in the caller's Profile (else
"unknown session"); a target playing Media the caller cannot reveal is "not controllable";
transport verbs need an active playback; `play-media` needs a live device session and Media the
caller can see (an unknown and a hidden Media answer identically).

## Non-goals

- Argument fields on `/v1/playback-control` and the home-automation media-player entity: they change
  the published wire contract and need consumer repins (child ticket).
- Receivers and controllers in the native clients (child tickets per client).
- A per-command capability report from receivers; a receiver ignores a verb it cannot apply.
