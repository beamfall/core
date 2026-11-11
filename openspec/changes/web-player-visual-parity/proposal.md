# Bring the web player transport up to the approved v-player mockup

Ticket: CORE-0045 (`queue:core:main`), roadmap item WEB-PLAYER-VISUAL-PARITY-1. Approval
`NEXTGEN-DESIGN-2026-07-12` (`mockups/web-app`, screen `v-player`). ADR-0007, ADR-0080 to ADR-0087.

## Why

The shipped Viewer player renders every control as a text-labelled rectangle in three wrapping rows on
a scrim gradient. The approved mockup is a single floating glass card with circular icon controls, a
cyan play disc, a thin beam scrubber and a pill-chip row. This change is the implementation half of
that approval; the control hierarchy from `mockups/web-player-ergonomics/SPEC.md` is unchanged.

## What Changes

- The transport becomes a floating rounded card inset from the stage edges, on a translucent surface
  with a hairline border and backdrop blur (solid under `prefers-reduced-transparency`). The scrim
  gradient is removed.
- Primary controls (back, play or pause, forward) and the per-action auxiliary buttons (chapters,
  fullscreen, mute, Picture in Picture, share) become circular icon-only controls. Each keeps its
  `aria-label` and a visually hidden text child. Play or pause is a filled beam disc.
- The scrubber track thins to 4px (6px on hover) with a round beam thumb; marker ticks are unchanged.
- Speed, skip-interval, skip-segment and the track, info and subtitle-style triggers render as pills.
  The native selects keep the `combobox` role.
- Track menus, the info overlay and the subtitle panel keep their anchors and take the same glass
  surface.
- Every transport button keeps a hit area of at least 44 by 44 CSS pixels.

## Non-goals

- Control behaviour, keyboard shortcuts, gestures, auto-hide, strings or design tokens.
- Moving the options panel away from its trigger anchors.

## Failure modes

- Without `backdrop-filter` support the card falls back to its translucent solid fill.
- With `prefers-reduced-transparency` the card and panels use an opaque fill.

## Acceptance evidence

- `PlayerTransport.test.tsx`: existing assertions unchanged; a new test asserts every icon-only button
  has a non-empty accessible name.
- `e2e/player.spec.ts`: a mobile assertion measures every transport button at 44 by 44 or larger.
- Regenerated `player-desktop` and `player-mobile` goldens, listed in the evidence file.
- Release evidence `WEB-PLAYER-VISUAL-PARITY-1.md`, retained outside this repository.

## Rollback

Revert the change. It is presentation only and holds no state.

## Capabilities

### New Capabilities

- `web-player-visual-parity`: the visual and target-size contract of the web player transport.

### Modified Capabilities

None.
