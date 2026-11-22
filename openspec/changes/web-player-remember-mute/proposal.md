# Web player: remember mute and volume per device

Ticket: CORE-3078 (`queue:core:main`).

## Why

A mute set in the web player lived only in the transport's state and the `<video>` element, so the
next item in a fresh visit, and every reload, started at full volume. The music stage plays on the
same element, so it had the same gap.

## What changes

The player keeps the device's muted flag and volume in localStorage. A new player element starts from
them; mute and volume changes made in the transport are written back. Unmuting clears the mute. A
blocked or cleared store means full volume, unmuted. Native clients have no in-app mute control, so
they are unchanged.
