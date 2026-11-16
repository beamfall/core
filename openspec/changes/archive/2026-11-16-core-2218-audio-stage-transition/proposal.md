# Track changes on the Web Viewer audio stage transition instead of snapping

Ticket: CORE-2218 (`queue:core:main`).

## Why

The owner asked for the artwork and the words on the audio stage to transition nicely when the
track changes, and to look slick (2026-11-16). Today the stage swaps the artwork, its blurred
backdrop and the title and credit lines in place: a track change snaps, and the frame shows its
empty fallback while the next artwork loads.

## What Changes

- The artwork crossfades: the incoming image stays hidden until it has loaded, then settles in
  (fade, a slight zoom down and a focus pull) over the outgoing one, which sinks back and dims
  underneath and is dropped once the motion ends. The blurred backdrop crossfades the same way.
- The outgoing title and credit lines lift away while the incoming lines rise in one after another,
  laid over each other so the card does not grow.
- An item without artwork fades the old artwork out to the fallback frame.
- `prefers-reduced-motion` swaps instantly.
- Host UI motion only: this animates page chrome, not a visual effect, so it stays in the web app
  (ADR-0228).
- The visualizer keeps moving across a track change. Its renderer stays up while the next source
  loads, and it keeps drawing while that source loads instead of waiting for play. It decodes the
  next item's analysis ahead and eases its audio values from one item to the next. The visual
  switch keeps the previous frame as feedback and keeps the clock running, so the new Oscillux
  visual's own trails hand over instead of a cut to black. Items that share artwork keep one
  visual, and the other visuals compile while the page is idle. All of this is host glue and
  selection policy: no effect code is added. A designed visual-to-visual transition would come
  from Oscillux.
- Narrow and short windows: the artwork fits the height the words leave, the transport no longer
  repeats the title over the card, and on a phone the play controls sit in the transport row
  instead of over the card.

## Impact

- Web app only (`AudioStage`, a small `useLayers` hook, `player-deferred.css`); no wire, store or
  contract change.
