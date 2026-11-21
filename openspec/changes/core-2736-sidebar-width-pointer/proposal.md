# Single-pointer control for the web sidebar width

Ticket: CORE-2736 (`queue:core:main`).

## Why

CORE-2734 made the web Viewer's sidebar resizable by dragging its separator or by keyboard. A
pointer user who cannot drag can only reset the width with a double-click; they cannot choose a
width. WCAG 2.2 SC 2.5.7 (Dragging Movements) requires a single-pointer alternative to dragging.

## What Changes

- A single click or tap on the sidebar separator, without moving it, opens a small popover beside
  the separator with Narrower, Wider and Default buttons. Narrower and Wider step 16 px, the same
  as the arrow keys, and are disabled at the limits; Default restores 200 px. Each press is
  persisted like any other width change.
- Escape, a press outside, or clicking the separator again closes the popover and returns focus
  to the separator.
- Drag, arrow keys, Home, End, Enter and double-click reset keep their current behaviour. A drag
  never opens the popover.

## Non-goals

- No new control on the sidebar itself or in Settings; the separator stays the only entry point.
