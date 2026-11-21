# Party session API

This is the contract the server and web build against. Core identifiers stay domain-neutral:
party session, participant, entry, screen, join code. Plugin labels (for a karaoke Library:
singer, song) appear only in web copy.

## Graph (session role; the caller's Profile is the host unless noted)

Types:

```
PartySession {
  id, joinCode, joinPath            # joinPath = "/web/join/<CODE>"
  fairness: "round-robin" | "fifo", autoAdd: Boolean, joinOpen: Boolean
  libraryIDs: [String], startedAt, expiresAt   # RFC 3339
  changeoverSeconds: Int
  current: PartyEntry | null
  playback: PartyPlayback | null
  entries: [PartyEntry]       # waiting, in rotation order, with position and etaSeconds
  requests: [PartyEntry]      # pending host approval, oldest first
  recent: [PartyEntry]        # done, newest first, at most 20
  removed: [PartyEntry]       # removed in the last 10 minutes, for undo
  participants: [PartyParticipant]
  screens: [PartyScreen]
  skipped: [PartyEntry]       # skipped because the Media is no longer visible, newest first
}
PartyEntry {
  id, mediaID, title, subtitle, durationSeconds (0 when unknown), artworkURL
  participantID, participantName
  state: "pending" | "waiting" | "current" | "done" | "removed" | "declined" | "skipped"
  pinned: Boolean, position: Int (1-based among waiting, 0 otherwise), etaSeconds: Int
  addedAt, lastPlayedAt (when this Media was last current earlier in the session, else "")
}
PartyParticipant { id, name, guest: Boolean, waiting: Int, lastTurnAt }
PartyPlayback {
  entryID, state: "playing" | "paused", positionMS: Int, anchorAtMS: Int (server Unix ms),
  tempo: Float (1 = original), pitchSemitones: Int
}
PartyScreen {
  id, name, kind: "popout" | "joined", carrier: "local" | "relay", lead: Boolean,
  ticker: Boolean, offsetMS: Int, rttMS: Int, reportedAt ("" before the first report)
}
PartyStage {
  sessionID, serverNowMS, current, playback, upNext: [PartyEntry] (at most 3),
  ticker: Boolean (for the screenID asked about; true when none), joinCode, joinPath
}
```

Fields (lower-cased in the registries):

| Field | Kind | Arguments | Result |
|---|---|---|---|
| `partySession` | query | | `PartySession` or null |
| `partyStage` | query | `sessionID`, `screenID?` | `PartyStage` (any signed-in Profile on the server) |
| `partyStart` | mutation | `libraryIDs: [String]`, `fairness?`, `autoAdd?` | `PartySession` |
| `partyEnd` | mutation | | `Boolean` |
| `partyUpdate` | mutation | `fairness?`, `autoAdd?`, `joinOpen?` | `PartySession` |
| `partyRotateCode` | mutation | | `PartySession` |
| `partyAdd` | mutation | `mediaID`, `participantID?`, `participantName?` | `PartySession` |
| `partyReview` | mutation | `entryID`, `approve: Boolean` | `PartySession` |
| `partyMove` | mutation | `entryID`, `position: Int` (1-based) | `PartySession` |
| `partyRemove` | mutation | `entryID` | `PartySession` |
| `partyRestore` | mutation | `entryID` | `PartySession` |
| `partyAdvance` | mutation | `fromEntryID?` | `PartySession` |
| `partyPlayback` | mutation | `entryID`, `state`, `positionMS`, `tempo?`, `pitchSemitones?` | `PartySession` |
| `partyRemoveParticipant` | mutation | `participantID` | `PartySession` |
| `partyScreenRegister` | mutation | `sessionID`, `name`, `kind`, `carrier` | `PartyScreen` (any signed-in Profile) |
| `partyScreenReport` | mutation | `screenID`, `offsetMS`, `rttMS` | `Boolean` (the registering Profile) |
| `partyScreenTicker` | mutation | `screenID`, `ticker: Boolean` | `PartySession` |
| `partyScreenDrop` | mutation | `screenID` | `PartySession` |

`partyAdvance` with no current entry starts the first waiting entry. With `fromEntryID` that is
not current it returns the session unchanged. A new current entry gets a paused anchor at 0; the
host page starts it.

The first popout screen registered is the lead; a joined screen is never lead while a popout is
registered. Screens not reporting for 2 minutes are dropped from `screens`.

Clock: clients calibrate with the existing `/api/group/calibrate` endpoint and take the lowest-RTT
sample; `partyStage.serverNowMS` is a fallback.

Target position at server time `now`: `positionMS + (now - anchorAtMS)` when playing (song
milliseconds; a tempo-adjusted stream maps through the applied tempo), else `positionMS`.

## Guest HTTP (no session; `X-Beamfall-Party-Guest: <token>` header)

| Route | Body / query | Response |
|---|---|---|
| `POST /api/party/join` | `{code, name}` | `{token, sessionID, participantID, name, expiresAt}`; 404 `{"error":"join code not recognised"}` for any unusable code; 429 when rate-limited; 409 when full |
| `GET /api/party/guest` | | `{sessionID, joinCode, autoAdd, current, entries, mine, maxWaiting, expiresAt}`. `current`/`entries` are PartyEntry with only id, title, subtitle, participantName, state, position, etaSeconds; `mine` adds the guest's pending, waiting and declined entries |
| `GET /api/party/guest/search?q=` | | `{items: [{mediaID, title, subtitle, durationSeconds, artworkURL, lastPlayedAt}]}`, at most 30 |
| `POST /api/party/guest/request` | `{mediaID}` | `{entry}`; 429 rate-limited; 409 at the waiting limit |
| `POST /api/party/guest/cancel` | `{entryID}` | `{ok: true}` |

A refused or expired token answers 401 `{"error":"party session ended"}`.

Guest token: 32 random bytes, base64url, prefix `bfg_`, stored as SHA-256.

## Storage

Events migration `0026_party_session.sql`: `party_session`, `party_participant`, `party_entry`,
`party_screen`. Rotation order is computed on read from entries, pins and turn history.
