# Playing next: Play now advances at once and fades, the next item's picture and artist

Ticket: CORE-3348 (`queue:core:main`). Follows CORE-3216 (the web Playing next card).

## Why

Owner report (2026-11-23): during music playback, Play now on the Playing next card seemed to do
nothing, and a skip cut the audio and the visual hard. On the server Play Queue the audio stage was
keyed by the playing Media id, so every track change remounted the stage and its visualizer: the
visual cut to a new renderer instead of handing over, which breaks the existing requirement "The
visualizer keeps moving across a track change". Play now also called the raw advance with no fade
and left the card up until the next item loaded. The owner also asked for the next item's picture
and artist on the card.

## What changes

- Play now hides the card at once and moves to the next item on both the card queue and the server
  Play Queue. On the server Play Queue it plays the item the card names directly, without the fresh
  queue read Next waits for: on a slow server that read held the skip until the countdown ran out,
  which is how Play now looked dead.
- A skip from Play now fades the playing item out and the next one in (equal power, about 350 ms
  each way) on the element volume, like the sleep timer fade (CORE-2960). A volume the listener sets
  mid-fade is kept and the skip still happens; the fade is never saved as the listener's volume
  (a mute during the fade saves the listener's level). A track change that is not the fading skip
  (Back, another item, leaving the player) drops the pending skip.
- A gapless join already scheduled for the next track is stopped before the skip, so the outgoing
  decoded track never plays on under the next one (CORE-3352).
- On the server Play Queue the audio stage keeps one identity per stage owner instead of per Media,
  so the stage and its Oscillux visual stay mounted across tracks (restores the existing visualizer
  requirement; no new effect code in the client).
- The card reads the next item (only while the card shows) and shows its poster or thumb and its
  person credits; a picture that fails to load is dropped. The region label names the artist.
  Each appearance of the card reads afresh, for the viewer it shows to. A held stage title links to
  its own item, not the incoming one (CORE-3353).
- New string `playingNextBy` in every locale.

## Non-goals

- Apple and Android cards: tracked by CORE-3218, CORE-3219 and CORE-3220.
- Gapless crossfade of overlapping sources; this fades one element out and in.
