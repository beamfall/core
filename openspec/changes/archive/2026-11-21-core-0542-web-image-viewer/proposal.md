# Web image viewer: image Media opens in a viewer, not the video player

Tickets: CORE-0542, CORE-2978 (`queue:core:main`). Design: `core-private/docs/design/photos.md` §3.1 W3.

## Why

Opening an image-class Media on the web sends it to `/watch`, the video player. The player has no
picture stage for a still: the viewer gets a black page with transport controls and no way to step
through the album. The server already classifies Media as `image` and serves the original as
direct bytes behind a Stream Token (`imageDecision`), and every image carries a 1280-pixel poster
derivative. Only the client routing and a viewer page are missing.

## What changes

- **Route.** A new viewer route `/view/<address>` uses the same readable address and bare-ID
  fallback as `/watch` and `/media` (CORE-2329).
- **Entry points.**
  - A card for an image-class Media opens the viewer, whether or not the card would otherwise
    play on activation, and its quick Play opens the viewer too.
  - The detail page's Play for an image opens the viewer.
  - Any other path that reaches `/watch` for an image (search rows, remote commands, shared links)
    is replaced by `/view` once the player learns the class, so no path leaves an image in the
    video player.
- **Viewer page.**
  - A full-bleed black stage paints the item's poster derivative at once.
  - Zooming loads the original through the playback decision's direct stream. The original
    replaces the derivative only once the browser has decoded it. A format the browser cannot
    decode (HEIC, RAW, TIFF on most browsers) keeps the derivative and says so.
  - Previous and next step through the image items of the queue the opening card carried. Arrow
    keys, on-screen buttons and a horizontal swipe all step.
  - Z, a double click, or the wheel zooms. Dragging pans a zoomed image. 0 resets.
  - R rotates the view by a quarter turn. The rotation is not saved (saved orientation is
    CORE-2991).
  - F toggles the Profile's favourite through `setFavorite`.
  - I opens an info pane with the title, year and the plugin-declared fields.
  - Escape or Back closes the viewer and returns to where it was opened.
- **Glyph.** An image item with no artwork shows an image glyph instead of the film glyph.

## Non-goals

- Filmstrip, trash with undo, inline clip playback in the viewer, slideshow and Ambient
  (CORE-0096, CORE-2981, CORE-0106). These are separate tickets in the photos design §5.
- Native clients (tvOS viewer is CORE-2979).
- Thumbnail correctness for RAW, orientation and ICC (CORE-0310, a separate change).

## Failure modes

- An item the server reports unknown or hidden shows the generic "Not available" state.
- A failed playback decision leaves the derivative on screen and zoom works on it alone.
- An item with no poster derivative shows the image glyph until the original loads.
