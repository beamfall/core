# Cinematic player overlay

Ticket: CORE-1141 (`queue:core:main`).
Branch: `player-overlay`, stacked on `admin-findability` (CORE-1131).

## Why

Dogfooding on Unraid (2026-11-11), the owner watched a film over a server HLS transcode and called
the web player overlay unattractive. Today it is a large opaque card across the bottom of the video.
The card holds the title and source line, the transport, the scrubber, a row of round buttons (seek
−10/+10, chapters, fullscreen, mute, a volume pill, a "Playback speed" select, PiP, share) and a
second row of pills (skip interval, Audio, Subtitles, Quality, info). It hides a third of the
picture and reads as a form, not a player.

## What Changes

- **The video is the hero.** The card is replaced by a translucent gradient scrim that rises from
  the bottom of the stage. The title, the source line, the scrubber and one row of controls sit on
  the scrim with no card behind them.
- **Slim scrubber.** A thin track that thickens on hover and focus. It shows the buffered range,
  chapter and segment ticks, a time tooltip at the pointer (above the trickplay frame when the
  server has one), and elapsed and remaining time.
- **One transport row.** Play/pause, seek back and forward, previous and next chapter, and volume sit
  on the left. Skip-segment, share, settings, PiP and fullscreen sit on the right. Icons are refined
  and controls glow on hover in the house (CORE-1114) style.
- **Volume slides out.** Volume is an icon button. Its slider slides out on hover or focus and stays
  keyboard-operable.
- **One settings sheet.** Speed, audio, subtitles, subtitle styling, quality, the per-show skip
  interval and playback details move from the pill row into one glass settings popover. Each row
  shows its current value. On phones the popover becomes a bottom sheet.
- **Idle and motion.** Controls and the scrim fade out after idle playback, as they do today. They
  never hide while paused or while a menu is open. Reduced motion removes the transitions, and
  reduced transparency makes the scrim solid.

Every control and behaviour stays, with its accessible name, keyboard shortcuts, touch gestures,
Up Next and the skipped-segment overlay. Player chrome is host UI, not a visual effect, so it stays
outside Oscillux (ADR-0228).

## Non-goals

- New playback features, new settings or server/API changes.
- The Viewer shell, browse pages or the player top bar outside the stage.

## Capabilities

### New Capabilities

- `web-player-overlay`: the web player's control overlay layout, settings sheet and idle behaviour.

### Modified Capabilities

None.
