# Sleep timer: fade the audio out gradually before it stops

Ticket: CORE-3358 (`queue:core:main`). Follows CORE-2960 (web listening controls) and reuses the
CORE-3348 Play now fade.

## Why

Owner request (2026-11-23): the sleep timer should fade the audio out gradually as the stop time
approaches, so playback does not cut off abruptly. The CORE-2960 fade was 10 seconds, linear and
stepped every 250 ms, which still sounds like a hard stop.

## What changes

- The web sleep timer fades the element volume out over the final stretch with the equal-power
  curve the Play now fade uses: 45 seconds, or a quarter of a timer set with less than 3 minutes
  left. End-of-item and end-of-chapter timers fade over the item's or chapter's end.
- The volume steps every 50 ms during the fade.
- At the stop, playback pauses and the listener's own level comes back.
- Cancelling or extending the timer mid-fade gives the level back at once. A volume the listener
  sets, a pause, a play or a Play now skip ends the fade cleanly; what is left of the timer then
  fades afresh from the listener's level, so the stop never cuts.
- The sleep fade and the Play now fade share one volume hold per element and one curve
  (`trackFade.ts`). The transport slider and the saved volume show the listener's level, never a
  faded one.

## Non-goals

- Apple sleep timers (macOS player, tvOS Now Playing): CORE-3359.
- Android has no sleep timer yet (CORE-0340, CORE-3018); its fade is noted there.
