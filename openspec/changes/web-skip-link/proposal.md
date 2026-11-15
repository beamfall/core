# Skip-to-content link on viewer and console pages

Ticket: CORE-1718 (`queue:core:main`).

## Why

Keyboard and screen-reader users must Tab through the header, library sidebar and account controls
on every page before reaching its content. The web app has no skip link.

## What Changes

- A shared skip link is the first Tab stop on every page the app shell renders (viewer browse,
  Manage and console), on the profile picker and on the player. It stays off-screen until it has
  keyboard focus, then shows top-left above the header.
- Activating it moves focus to the page's main region (`#bf-main-content`, focusable with
  `tabIndex=-1`), so the next Tab lands on the first control in the content.
- Its label is a new localized string, `skipToContent`, in every shipped locale.
