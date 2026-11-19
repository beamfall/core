# Enforce optional file workflows

Ticket CORE-2453 (`queue:core:main`).

## Why

The Console saves the File conversion and Rename files settings. The existing
workflow mutations need to respect those settings while Core stays on
client-core-wire 2.37.0.

## What Changes

- Check the persisted capability before retrying a conversion or applying a
  single or batch rename. Use the existing GraphQL conflict code with a localized
  message when the workflow is off.
- Read the existing admin capability states in Manage when an Admin Session is
  available. Hide disabled workflow links from navigation and search. Managers
  without that read can open the pages and see refusals when they submit an action.
- Keep the Optional features help accurate in every shipped locale.

The existing conversion queue can display prior jobs. The 2.37.0 contract
supports retrying a prior conversion job. Disabling a workflow preserves
already admitted jobs and the server operation for undoing completed batches.
A configuration read failure prevents mutation admission.
