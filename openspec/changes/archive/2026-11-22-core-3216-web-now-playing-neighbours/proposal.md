# Now Playing neighbours, playing-next countdown and an OLED-safe Ambient mode

Ticket: CORE-3216 (`queue:core:main`).

## Why

The web audio stage never says what Previous or Next will play, gives no warning before an audio
track hands over to the next one, and keeps a bright, fixed layout on screen for a whole album,
which marks OLED panels. The apple-visual-lab mockups already set the direction: a played
before / playing now / playing next ribbon, an Up Next card in the final 12 seconds, and an
ambient posture that dims when idle and moves its anchor.

## What Changes

- Previous and Next show the neighbour title on hover and focus, and name it in their accessible
  label. An Up next drawer lists the previous, current and upcoming items; a row plays that item.
- In the final 12 seconds of an audio track with a next item, a Playing next card shows the next
  title and a countdown ring. It does not change auto-advance; Play now advances at once and
  Hide dismisses it for that track.
- An Ambient toggle on the audio stage blacks out the backdrop and visual, dims the stage after
  45 seconds without input and moves the card slowly between safe anchors. Input or a track
  change restores full info. The drift and the dim come from Oscillux curves (ADR-0228); the
  host maps their values to a transform and opacity. The choice is remembered per browser.

## Non-goals

- Other clients (CORE-3217..3220) and an Oscillux burn-in primitive (CORE-3221).
- Editing the queue from the drawer; the server-queue popover keeps that.
