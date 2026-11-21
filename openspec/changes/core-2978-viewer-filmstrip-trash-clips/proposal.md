# Web image viewer: filmstrip, trash with undo, inline clips

Tickets: CORE-2978 (web photo viewer), CORE-2977 (camera-roll clips), CORE-0542 (favourite race
follow-up) (`queue:core:main`). Design: `core-private/docs/design/photos.md` §3.1 W1 and W3, §5.2.
Builds on `core-0542-web-image-viewer`.

## Why

The first viewer cut (CORE-0542) steps through stills only. A camera roll is 10-20% short clips,
and the viewer steps over them, so they can only be reached by leaving the viewer for the video
player. The viewer has no way to see what is coming next, and no way to throw a bad shot away.
Clip cards in a photo grid look like films: no duration on the picture. Rapid favourite toggles
can land out of order and leave the heart wrong.

## What changes

- **Filmstrip.** A strip of thumbnails of the queue's neighbours sits at the bottom of the stage.
  A click jumps to that item. The strip keeps the current item in view. It hides with the rest of
  the chrome when the pointer goes idle and comes back on any pointer move or key.
- **Trash with undo.**
  - On a manager device, a trash control and the Delete key move the item to server trash through
    the existing audited `trashMedia` mutation.
  - The viewer then steps to the next item (the previous one at the end of the queue, or closes
    when nothing is left). A toast offers Undo, which calls `restoreMedia` and returns to the item.
  - Devices without the manager role do not see the control, matching the mutation's role gate.
- **Inline clips.** A video-class item in the viewer's queue is part of stepping. It plays muted
  and inline on the stage from the direct stream that `/api/playback-info` issues, with a
  play/pause control and Space. A clip the browser cannot play says so and offers the full player.
  A video opened at `/view` outside a queue still goes to the player.
- **Clip cards.** In a grid whose queue holds images, a video-class card shows its duration as a
  badge on the picture, draws a clip glyph instead of the film glyph, and opens the viewer.
- **Favourite race.** The favourite control is disabled while that item's toggle is in flight, so
  two toggles of one item never overlap.

## Non-goals

- Slideshow and Ambient (CORE-2981), selection-mode bulk trash (W2), Live Photo motion (CORE-2987).
- Transitions between items or strip animation: plain show and hide only (Oscillux owns effects).
- Clip transport beyond play and pause (scrubbing, volume) and HLS fallback for clips.

## Failure modes

- A failed trash keeps the item and says so in the toast. A failed restore says the item is still
  in Trash, where the Manage trash page can restore it.
- A thumbnail that fails to load shows the item's glyph.
- A clip with no direct stream, or one the browser cannot decode, keeps its poster with a notice
  and a link to the full player.
