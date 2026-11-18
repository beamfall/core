# Settings clarity: help, dropdowns, distinct Console and Manage, settings search

Ticket: CORE-2445 (`queue:core:main`).

## Why

An audit of every Console and Manage setting (2026-11-17) found settings named in server jargon ("Low-power mode", "Remote enrollment", "UPnP or NAT-PMP", "Scopes", "Retention count") with no explanation, values typed as raw numbers that have a handful of sensible choices (background workers, bitrate caps in Mbit/s, access windows in minutes since midnight), and Console and Manage menus that overlap: both had Backups, Console "Schedules" meant parental controls while Manage had job schedules, and both had a "Profiles". Nothing let a person find a setting without knowing which page held it.

## What Changes

- A shared (?) `HelpTip` popover, wired into the workbench field primitives (`ManageField`, `Fieldset`, `ControlRow`), and help text for the unclear settings on Console and Manage pages, in every shipped locale.
- Dropdowns for settings with a small set of useful values, keeping any saved off-preset value selectable; better defaults where the old one was empty or meaningless.
- Console and Manage menus each open with a one-line purpose and a link to the other area. Backups is Console-only (`/manage/backups` redirects). Renames: Console "Schedules" → "Parental controls"; Manage "Trash policy" → "Trash", "Profiles" → "Switch profile", "Restrictions" → "Unlock restricted content". The Console "Layout & Navigation" item, which rendered the same page as Identification, is removed and its address redirects.
- A settings search in the Console rail and the Manage menu over a static index of settings and destinations from both areas; choosing a result opens the page and scrolls to and highlights the setting.

## Non-goals

- Server API changes. Values the server does not return (per-profile stream cap, extension pre-authorization) are filed as follow-ups, not faked here.
- Moving job schedules from Manage to the Console, moving profile switching to the account menu, or splitting library setup from the file tree. Filed as follow-ups.

## Capabilities

### New Capabilities

- `web-settings-clarity`: help buttons, preset dropdowns, distinct Console and Manage purposes, settings search.

### Modified Capabilities

- `web-advanced-settings`: the Normal menu scenario no longer names the removed "Layout & Navigation" destination.
