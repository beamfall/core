# Beamfall accessibility and privacy UX contract

Status: draft v0.1  
Date: 2026-06-11

This document turns the accessibility and privacy review findings into implementation
acceptance criteria. It complements ADR-0004, ADR-0012, ADR-0014, the design system, and
the visual contracts.

It is the **component-level companion** to the cross-surface accessibility contract
([`accessibility-contract.md`](accessibility-contract.md), realizing ADR-0100): that spec names the
WCAG 2.2 AA target, the required-behavior set, and the per-renderer parity ledger; this document is
the per-component checklist and the Reveal/Restrictions negative-test matrix those behaviors are
enforced against.

## Component accessibility table

Every component spec must define:

| Component | Required fields |
|---|---|
| Button/IconButton | Role, accessible name, default/hover/focus/active/disabled/loading states, keyboard action, screen-reader phrase |
| MediaCard/EntityCard | Role by platform, title, subtitle, progress, status badge, focus behavior, selected state |
| Section | Heading semantics, item count semantics when reveal-safe, empty collapse behavior, first focus |
| DataTable | Row/column semantics, sortable columns, row actions, multi-select, selection announcement |
| Slider/Scrubber | Adjustable role, min/max/current value, spoken time, keyboard/remote/touch behavior |
| Menu | Trigger name, selected item, disabled reason, escape path, focus return |
| Banner/Toast/UndoNotice | Announcement priority, duration, focus behavior, action label |
| DryRunReview | Summary semantics, per-row planned action, blocked reasons, irreversible state |

## Minimum accessibility evidence

Every visual contract packet must include:

- Keyboard focus screenshots.
- TV focus screenshots where applicable.
- VoiceOver/TalkBack or screen-reader focus order notes.
- Reduced-motion state.
- Contrast check for text and meaningful controls.
- Dynamic Type or font-scaling evidence on Apple/mobile surfaces.
- Captions and subtitle style evidence where player work is touched.

## Restrictions negative test matrix

Unrevealed states must not leak restricted content through:

- Navigation entries.
- Search suggestions.
- Search empty-state wording.
- Source counts.
- "See all" counts.
- Continue-watching rows.
- Top Shelf, Engage, widgets, Live Activities, Siri/Spotlight, SharePlay.
- Jobs and job errors.
- Activity logs.
- Tombstones.
- Trash rows.
- Notifications.
- Device presence.
- Artwork fallbacks.
- Sidecar Track names.
- Stream Token or byte endpoints.
- Screenshots and preview assets.

Allowed unrevealed language:

- "Restricted Libraries are hidden for this Profile."
- "Relocked. Restricted Libraries are hidden again."
- "No results." when the distinction between none and filtered would leak.

Forbidden unrevealed language:

- Any title, count, path, source name, failed job, artwork, or recovery action that proves
  restricted content exists.

## Destructive operation templates

Every destructive or bulk mutation needs these fields:

- Summary of selected count.
- Changed count.
- Unchanged count.
- Blocked count.
- Per-item planned action.
- Trash state: on, off, or not applicable.
- Restore path when Trash is on.
- Irreversible warning when Trash is off.
- Audit/activity ID after execution.
- Undo expiry or permanence.
- Collision state for restore.

Default button state:

- Safe action is primary only when Trash is on or the operation is non-destructive.
- Irreversible action is never the default focused action.
- Confirmation copy names the operation, not the sensitive content.

## Account and relay privacy copy

Account surfaces say:

- "Beamfall Account knows purchases and devices, never your libraries or playback."
- "Beamfall Core and the self-hosted Web UI do not require a Beamfall Account."

Remote access surfaces say:

- "Self-hosted Relay" for launch relay configuration.
- "The relay cannot see your media. It can see hostnames, connection metadata, and traffic
  volume."

Do not show Hosted Relay, claim-code status, or relay pricing in launch UI unless a future ADR
graduates the hosted feature.
