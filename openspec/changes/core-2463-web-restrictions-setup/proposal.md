# Set up Restrictions from the web

Ticket: CORE-2463 (`queue:core:main`).

## Why

The server already supports Restrictions (ADR-0004): a Restrictions PIN set with `setRevealPin`, and
Libraries marked restricted with `setLibraryRestricted`, which disappear for an unrevealed session
until it is unlocked. The web app offers neither control. Manage › Restrictions only shows an unlock
field, which cannot succeed on a server with no PIN, and Console › Libraries has no Restricted
switch. A manager therefore cannot set Restrictions up at all from the web.

## What Changes

- Manage › Restrictions shows one of three states:
  - **No PIN set** (the session reports `revealPinSet: false`, CORE-2462): a short explanation of
    the two setup steps and a Set PIN form (new PIN and confirmation) instead of an unlock field.
  - **Locked with a PIN set**, or a server that does not report `revealPinSet`: the existing unlock
    field.
  - **Unlocked**: Relock, Change PIN (the same form) and a link to Console › Libraries.
- The Set PIN form refuses two different PINs without calling the server, clears both fields once
  read, calls `setRevealPin`, says "PIN saved." and reloads the session. A profile with no reveal
  path gets fixed copy saying it cannot have a PIN.
- Console › Libraries gets a per-Library "Restricted" switch calling `setLibraryRestricted`. It is
  disabled with help pointing to Manage › Restrictions until the session is unlocked, because the
  server requires a revealed session. Saving refreshes the Navigation Manifest.
- `settingsIndex` lists `manage.restrictions.pin` and `console.libraries.restricted`.
- Strings ship in every shipped locale.

No server, graph or wire change: both mutations already exist. The `revealPinSet` flag comes from
CORE-2462, which this change stacks on.

## Non-goals

- Moving Restrictions into the account menu or splitting Console and Manage Libraries (CORE-2449).
  The new controls carry `data-setting` ids so that move can relocate them.
- Clearing a PIN, or per-profile Restricted Libraries.

## Capabilities

### New Capabilities

- `web-restrictions-setup`: a manager can set and change the Restrictions PIN and mark Libraries
  restricted from the web app.

### Modified Capabilities

None.
