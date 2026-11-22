# Music lyrics: a `mediaLyrics` read and a web Lyrics view

Ticket: CORE-1638 (`queue:core:main`), lyrics half only; its ReplayGain half stays open there.

## Why

The owner wants a Lyrics view in music playback like Tidal's: a full-screen now-playing view with
the artwork on the left and the lyrics on the right. Time-synced lyrics brighten the current line,
dim past and upcoming lines and scroll along; a click on a synced line seeks there. Plain lyrics
show as text.

Core has no lyrics read today. The music plugin already hydrates a `lyrics` Media field from Vorbis
and MP4 lyric tags and a same-name `.lrc` sidecar, but FFmpeg reports ID3 USLT under
language-suffixed keys the plugin does not read, FFmpeg does not report ID3 SYLT at all, and `.txt`
sidecars are not read.

## What changes

- A new session-role graph read, `mediaLyrics(mediaID)`, answers `{ synced, source, lines {
  startMs text } }` for an audio Media. It resolves, in order: a same-name `.lrc` sidecar, an
  embedded ID3v2 SYLT frame, the plugin-hydrated `lyrics` field, an embedded ID3v2 USLT frame and a
  same-name `.txt` sidecar. LRC text, wherever it comes from, is parsed into timed lines; untimed
  text becomes plain lines. Everything is read at request time from the item's primary file and
  stored fields: no migration and no stored lyrics record.
- The web audio stage gets a Lyrics toggle in the transport. Open, the stage puts the artwork on
  the left and the lyrics on the right. Synced lyrics highlight the current line, dim the rest,
  auto-scroll, and seek on click; plain lyrics show as text; an item without lyrics shows a quiet
  "No lyrics".
- The client-core wire contract gets an additive minor version for the new field at landing.

## Non-goals

- An online lyrics provider (CORE-1975: needs an outbound-egress decision).
- Word timing, line end times and Oscillux lyric effects (CORE-0163 and the LYRICFX tickets).
- A stored lyrics record with field locks (CORE-0399 keeps that).
- Native clients.

## Failure modes

- A Media the session cannot see answers `unknown media`, like `mediaFiles`.
- Non-audio Media, missing files, unreadable or oversized sidecars and malformed ID3 tags fall
  through to the next source and finally answer no lines; they never fail the read.
- A failed `mediaLyrics` read in the web shows "No lyrics"; playback is unaffected.

## Acceptance evidence

Go tests for LRC parsing, ID3 SYLT/USLT reading and resolution order; graph test for the read and
its access rule; vitest for the view (synced highlight, click to seek, plain text, no lyrics);
a screenshot of the view.

## Rollback

Revert the commit. The read is additive and stateless, so nothing persists to undo.
