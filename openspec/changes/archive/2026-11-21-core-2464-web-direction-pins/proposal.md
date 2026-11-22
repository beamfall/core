# Direction PIN setup on the web

Ticket CORE-2464.

## Why

TV remotes can enter a PIN through four directions. Web setup needs to let people
choose those directions and tell them when a numeric PIN cannot be entered that way.

## What changes

- Manage Restrictions and Console Profiles offer digits and directions when setting a
  PIN. Console Profiles uses the existing entry PIN update for adult Profiles.
- Direction setup displays arrows and submits their phone-keypad digits. Both PIN
  entries must match, and a direction PIN needs at least six steps.
- Digit setup warns when the PIN is incompatible with a TV remote. Setup explains
  that swipes are easier to shoulder-surf than keypad taps.

## Non-goals

This change is limited to web setup. It uses the existing GraphQL mutations,
verifiers, throttling and storage.

## Failure modes and evidence

Incomplete or mismatched direction entries cannot be saved. A TV compatibility
warning permits saving. Save failures use localized messages. Focused web tests
cover both setup paths, the keypad mapping, minimum length and the same numeric
value at setup and entry.

## Rollback

Reverting the web changes restores digit setup. PINs saved using directions remain
ordinary digit strings and work with the existing digit entry fields.
