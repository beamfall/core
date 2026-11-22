# Web slideshow: a full-screen, engagement-silent slideshow over existing Sources

Ticket: CORE-2981 (`queue:core:main`), web leg. Design: `core-private/docs/design/photos.md` §2.4,
§3.1 W7 and §5.2 (PHOTOS-AMBIENT-1).

## Why

The web client can show one image at a time (`/view`, CORE-0542) but cannot play a collection or a
Source as a slideshow. The Jellyfin complaint the design answers is a fixed, too-fast interval with
no way to keep music going. Everything a slideshow needs already exists: Sources and ordered
Collections list Media for the acting Profile, every image has a poster derivative, a clip has a
generated preview clip, and the docked player keeps the play queue going across routes.

## What changes

- **Route.** A new viewer route `/slideshow` takes its Source from the query: a library-scoped or
  all-libraries Source kind (`all-items`, `favorites`, `on-this-day`) or an ordered Collection.
- **Setup sheet (W7).** A Source picker over the Libraries and Collections the session can see, an
  interval slider from 5 to 60 seconds (default 12), a Motion choice (Calm, Still, Visualiser), the
  overlay toggles and Dark Room.
- **Stage.** A full-screen dark stage shows each image's poster derivative. Clips play muted and
  inline from their generated preview clip, looping for the slide's interval; a clip without one
  shows its poster. The next slide's image is preloaded.
- **Timing.** Each slide stays up for the interval, measured from when it was painted, within
  ±0.5 s.
- **Overlay.** A clock, a caption with the date (the first plugin-declared `date` field) and the
  plugin's subtitle line when the item has them, and the track the play queue is playing (read
  from the Media Session the player publishes). Music is whatever the current queue plays: the
  docked player keeps playing under the slideshow.
- **Dark Room.** A schedule, on or off with a start and end time (default 22:00 to 07:00, a range
  may cross midnight), stored per device in browser storage. Inside the window a plain opaque black
  scrim at about 85% covers the stage and the clock and caption stay readable but dim. This is UI
  dimming, not a visual effect: no filter or shader. D dims or undims at once until the schedule
  next changes state.
- **Portrait pairing.** On a landscape screen a portrait photo shares the stage, side by side with a
  small gap, with the next photo in the queue when that one is a portrait too, as Apple does. A lone
  portrait (the next is not a portrait, or the queue ends) shows alone. A pair is one slide:
  Previous and Next step by slides, and the caption is the first photo's date and place. Orientation
  is read from the decoded image, since the wire has no dimensions. Pairing is layout only: two
  fitted images in two halves. The design's "blurred fill, never letterbox bars" is a visual effect,
  and Oscillux is the only source of those (ADR-0228), so a lone portrait keeps its dark bars; the
  Oscillux `fill` and `pair` layouts (CORE-2980) take over through the `oscilluxSlideMotion` seam.
- **Keyboard.** Space pauses, arrows step, O toggles the overlay, D toggles Dark Room, F toggles
  browser full screen, S opens the setup sheet, Escape closes the sheet or the slideshow.
- **Entry points.** A Slideshow action on an ordered Collection's page and on a Library's browse
  page, shown when the listed items include images.
- **Household.** Slides come only from what the server lists for the acting Profile and Reveal
  state; locked items and the Profile's hidden items are dropped as well, and a change of identity
  reloads the slides.
- **Engagement-silent.** The slideshow never requests a playback decision, so it opens no playback
  session and writes no play counts or history.
- **Motion seam.** Oscillux is the only source of visual effects (ADR-0228). The pinned Oscillux
  v0.2.10 has no photo-slideshow mode: its transition scheduler and switch styles serve the
  visualiser's beat-quantised visual switches. Slides change with a hard cut, and one function
  (`oscilluxSlideMotion`) is the seam where the calm photo-slideshow mode (CORE-2980) plugs in.
  Calm and Visualiser are listed but unavailable until it does.

## Non-goals

- Ambient Groups, multi-room sync, remotes and "send to TV" (AMBIENT-GROUP-SYNC-1, REMOTE-RELAY-1).
- The tvOS leg and its screensaver hook (CORE-2981 Apple leg, built in parallel).
- Blurred fill behind a lone portrait (an Oscillux layout, CORE-2980) and playlist picking.
- Selection-mode "Play slideshow" (W2) and Places as a Source.

## Failure modes

- A Source with no images or clips shows an empty state with the setup sheet.
- An image that fails to load is skipped; when every slide fails the empty state shows.
- A failed list read shows an error with Retry.
- Without the Media Session API the now-playing line is left out.
