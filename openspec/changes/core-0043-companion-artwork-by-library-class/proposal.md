# Companion artwork stays out of non-image Libraries, and the decode message stops blaming transcoding

Ticket: CORE-0043 (`queue:core:main`).

## Why

On a real share, companion artwork beside episodes (`<stem>-thumb.jpg` and friends) became 742
image Media against 353 video Media and filled the browse grid. Playing one fed a JPEG to the video
element, and the resulting decode error showed a message telling the operator to turn on
transcoding for the Profile, which was already on. CORE-1708 now keeps recognised sidecars out of
the catalogue, but it does so in every Library, so a Library whose content is images loses photos
that happen to carry a sidecar-shaped name, and `<stem>-banner` is still catalogued.

## What Changes

- `<stem>-banner` joins the local artwork vocabulary as a recognised file-level name with no slot,
  like `<stem>-logo`.
- The scan skips recognised sidecar images only in a Library whose bound plugin does not declare
  the `image` Media class. A Library whose plugin declares `image` holds images as content, so every
  image there stays an item, classified by extension as before. Media class is all core knows about
  a plugin's content, so the family scope value stays opaque. The plugin's declared classes reach the
  scan through an optional method on the composition root's Identifier adapter; a scan without it
  (or a Library with no plugin) keeps the CORE-1708 behaviour.
- The web player's unsupported-codec message says the browser cannot decode the format and
  suggests another browser or device. It no longer asks anyone to turn on conversion for the
  Profile; the Profile setting is named only by the existing `transcode-disallowed` refusal, which
  the server returns exactly when the Profile's `transcodeAllowed` is false.

## Impact

- `internal/scan`, the `scanIdentifier` adapter in `internal/app`, and web copy in
  `internal/web/app`. No schema, wire or migration change.
- The scan preview and the pre-creation estimate still skip every recognised sidecar, so in an
  image Library they can under-report sidecar-named photos the scan will import.
- The play affordance for image Media is CORE-0043's split sibling,
  PLAYBACK-IMAGE-MEDIA-NO-PLAY-AFFORDANCE-1.
