# Subtitle offset and audio delay on the web player

Ticket: CORE-1671 (`queue:core:main`). The Apple and Android players land the same controls in
their own repositories under the same ticket.

## Why

Out-of-sync subtitles and lip-sync drift are the most common playback complaints a media server
gets, and the web player could not fix either. Its subtitle sync nudger only moved in half-second
steps, said "unavailable" on the hls.js route (the route every transcode and most browsers use),
forgot the value on reload, and there was no audio delay control at all.

## What Changes

- **Subtitle offset**: the nudger steps by ±0.1 s and ±0.5 s up to ±60 s, shows the value with the
  direction it moves cues ("+0.3 s later"), and has its own reset. The settings row shows a non-zero
  offset without opening the panel.
- **hls.js route**: hls.js renders cues into native TextTracks by default, so the offset now applies
  there too. Cues arrive per subtitle fragment, so the offset is re-applied after each fragment, each
  in-band caption parse and each subtitle track switch, and after a sidecar track loads. Only an
  hls.js build configured to draw its own cue DOM keeps the "unavailable on this route" note.
- **Audio delay**: a new "Audio sync" settings row and panel route the element's sound through a Web
  Audio delay line (0 to +5 s, the same steps and reset). A browser cannot play sound early, so the
  web range starts at zero and the panel says so. Routes whose sound Web Audio cannot carry (a
  cross-origin source without CORS, or a browser that plays HLS through its own engine, Safari, where a
  media element source goes silent and would stay attached for the next item) disable the control with a note instead of
  pretending. Audio-only items do not show the row.
- **Persistence**: both values persist on the device per Profile and Media, so a fix holds the next
  time the same item plays, including from Up Next. The server's per-Profile sidecar nudge
  (ADR-0194 §4) is left alone until the playbackInfo wire carries its value (CORE-0782); applying
  both would shift twice and the player could not show the server's value.

## Out of scope

- Moving the subtitle half onto the server nudge once CORE-0782 exposes it.
- Native and libmpv players (their repositories, same ticket).
