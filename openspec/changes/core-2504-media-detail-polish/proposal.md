# Media detail page: linked category chips, a fuller Details section, no generic shelf, steady layout

Tickets: CORE-2504, CORE-2507, CORE-2508, CORE-2509 (`queue:core:main`).

## Why

On the web Media detail page, category chips are plain text, so a viewer cannot go from a film
to its studio's page (CORE-2504). A film opened from Continue Watching shows a "Keep browsing" shelf
of unrelated items from that row (CORE-2507). The Details section lists only stored fields. It leaves
out the item's categories, its credited crew and the file being played (CORE-2508). And the page
shifts while it loads: the stage grows when the backdrop and facts arrive, and rows that load later
push the Details section down (CORE-2509).

## What changes

- **Facts row.**
  - Category credits are grouped by kind, in credit order. The row shows the first two kinds.
  - Each kind shows at most two chips, then a "+N" chip that counts the rest. The full list is in
    Details.
  - Every category chip is a keyboard-focusable link to `/entity/:id` and keeps the chip look.
  - Recorded layout: two chips per kind with a +N overflow per kind, for at most two kinds in the
    hero. Further kinds appear only in Details.
- **Details section.**
  - Lists each category kind, labelled with the kind's display name, with every credit linked.
  - Lists person credits that carry a `job` attribute, grouped under the job, with each person
    linked.
  - Lists the viewer-facing fields as before.
  - Lists the selected Version's file facts: its label, video codec and HDR format, and its default
    audio track's language, codec and channel layout.
  - Empty rows are omitted.
  - The page reads the track listing once and shares it between the pickers and Details.
- **Lower rows.** The "Keep browsing" shelf of cached section siblings is removed. Rows below the
  stage relate to the item: Cast, Details, Extras and Editions, More with, Related.
- **Layout while loading.**
  - The skeleton has the stage's shape: backdrop padding, poster frame, title, facts, actions, item
    actions and overview lines.
  - A stage painted from the opening card keeps that space until the item arrives.
  - The Details section renders before the rows that load on their own.
  - The track listing is requested together with the item, not after it.

## Non-goals

- Crew import. Director and writer credits come from CORE-2503; this change renders what a plugin
  writes.
- Resolution, container and file size in Details. `mediaTracks` does not carry them, and adding
  them is a client-core wire minor (follow-up ticket).
- Native clients.

## Failure modes

- A failed track listing leaves the Version, video and audio rows out of Details and hides the
  pickers, as before.
- A credit with no entity id renders as text, not a link.
- An item with no category credits, no job credits, no viewer-facing fields and no file facts shows
  no Details section.
