# Home hero yields to Continue Watching

Ticket: CORE-2825 (time-to-play audit P2).

## Why

On a 1440x900 window the Recently Added hero fills the first screen, so a
returning Profile scrolls past a newest-arrival feature before reaching the
item it wants to resume. The hero also appears only after its artwork settles,
so it can arrive late and push shelves down.

## What changes

- When the Home layout has a Continue Watching shelf with items, Home shows
  that shelf first and shows no Recently Added hero.
- While that shelf is still loading, Home shows neither the hero nor its
  placeholder, so nothing appears and then disappears.
- When the shelf is empty, failed, or absent, Home keeps the hero as today.

## Impact

Web Viewer only (`LayoutView`). No wire or server change.
