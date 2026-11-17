# A Library whose plugin does not declare images catalogues no image

Ticket: CORE-2361 (`queue:core:main`)

## Why

A Library bound to a plugin that declares classes but not `image` (a video plugin, say) still
catalogued an image the local artwork vocabulary did not recognise, such as `unknown.jpg`, as an
item. Sidecars an older scan had catalogued stayed in the catalogue too: a rescan only marked their
files missing. Those images show up as items in a Library that holds no images.

## What changes

- The scan picks one image rule per Library from the classes its bound plugin declares:
  - no plugin, or a plugin whose classes are unknown (unregistered, inactive, none declared):
    every image except recognised sidecars is an item, as today;
  - a plugin that declares `image`: every image is an item, as today;
  - a plugin that declares classes without `image`: no image is an item. An image there is a
    sidecar artwork candidate or is ignored.
- The rescan preview and the first-scan estimate take the same rule, so they count what the scan
  imports (CORE-2180).
- A rescan of a Library under the last rule retires every image-class Media whose live files all
  lie under the scanned root and that the rule no longer imports. Retirement is the recoverable
  soft delete behind `trashMedia`: the Media row and its files are kept for the trash retention
  window and can be restored. Each retired Media gets a `media.trash` activity entry with no actor
  and a `deleted` Media event.

## Non-goals

- No hard delete and no file removal.
- No change to Libraries with no plugin, or to Libraries whose plugin declares `image`.
- The rescan preview does not list retirements; it skips those images as it skips sidecars.

## Failure modes

- A walk that the empty-root guard treats as empty (no media file walked) retires nothing.
- With trash disabled the retirement is refused like every autonomous trash sweep (hard
  invariant 6): the scan logs a warning, counts a skipped sweep and retires nothing.
- A Media with a live file outside the scanned root, or a live file the rule still imports, is not
  retired.
- An image-class Media whose files the missing pass already marked missing is not retired; the
  grid already hides it.
