# Enforce optional file workflows

Ticket CORE-2453 (`queue:core:main`).

## Why

The Console saves the File conversion and Rename files settings, but Manage and
the server's file workflows ignore the saved values. The Convert page already
calls an enqueue mutation and needs a server handler for that request.

## What Changes

- Check the persisted capability before admitting a conversion or applying a
  single or batch rename. Return a typed conflict when the workflow is off.
- Wire the existing Convert client request to the server's conversion queue.
- Give Manage a manager-scoped view of the same workflow state read by the
  Console. Hide disabled workflow links and redirect direct visits to their pages.
- Update the Optional features help in every shipped locale.

The settings govern new workflow requests. Already admitted jobs and the
ability to undo completed batches remain available. A configuration read
failure prevents mutation admission. Reverting the implementation restores the
previous behaviour and preserves the saved settings.

Focused GraphQL and web tests cover the settings, refusals and navigation.
