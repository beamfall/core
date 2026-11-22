# Web screensaver: an idle browser opens the photo slideshow

Ticket: CORE-2981 (`queue:core:main`), web leg. Design: `core-private/docs/design/photos.md` §3.1
("Dark Room … can be set as the Beamfall screensaver") and §5.2 (PHOTOS-AMBIENT-1). The tvOS leg
built the same hook on Home (`TVScreensaver`); this brings it to the web client.

## Why

A browser left open on a TV, a wall tablet or a spare monitor shows a static page. The slideshow
already plays any Source full screen with Dark Room and the docked player's music; nothing opens
it on its own. Browsers have no third-party screensaver API, so, as on tvOS, the Beamfall
screensaver is the client's own idle surface.

## What changes

- **Setting.** The slideshow setup sheet gets a Screensaver option, "Open this slideshow when idle",
  stored per device in browser storage. It is off by default.
- **Source.** The screensaver plays the slideshow Source this device last played, with the
  slideshow's saved interval, overlay and Dark Room settings.
- **Idle.** With the option on, five minutes with no key, pointer, wheel, touch or scroll input
  opens `/slideshow` on that Source in screensaver mode. Idle counts only while the tab is visible,
  on a browsing route (not the player, the image viewer, the slideshow, the Profile picker or the
  remote), with no dialog open and no text field focused. Audio in the docked player keeps playing
  under it.
- **Screensaver mode.** The control bar and setup sheet stay hidden. The first key press, click,
  touch, wheel or deliberate pointer move closes it and returns to the page it opened over; that
  input does nothing else.
- **Fallback.** When the last Source is missing, fails to load or has no images, the screensaver
  closes at once and idle counts again from zero. The web client has no Home Ambient Display to
  fall back to.
- **Household.** The slideshow's own rules hold: slides are what the server lists for the acting
  Profile and Reveal state, hidden and locked items are dropped, and nothing is recorded as played.

## Non-goals

- An operating-system screensaver, the Screen Wake Lock outside the slideshow, or a configurable
  idle delay.
- A visualiser screensaver mode (the tvOS Visualiser mode has no web counterpart).

## Failure modes

- Without browser storage the option cannot be saved and the screensaver never opens.
- A navigation away during the five minutes restarts the count.
