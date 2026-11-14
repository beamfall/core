# One Normal/Advanced switch for web settings

Ticket: CORE-1117 (`queue:core:main`).

## Why

The Console, Manage and profile settings show every destination and control at once. Expert
surfaces sit beside first-run tasks, so it is hard to find the few settings most people change.
Today three separate mechanisms hide things: a collapsed "Maintain" rail group, per-panel
"Advanced" disclosures, and ad-hoc `<details>` cards. None of them remembers the choice.

## What Changes

- Add one per-browser preference, Normal (default) or Advanced, stored in `localStorage` under
  `beamfall.settings.advanced`. Reads and writes are guarded, so a blocked or empty store falls
  back to Normal without breaking the page.
- Add one switch, "Show advanced settings", where people look for it: in the footer of the
  Console rail and the Manage menu (visible at every width, including the folded phone menu), and
  in the header of the profile settings page.
- Normal hides expert destinations and controls. Advanced shows everything. Routes stay
  registered, so a deep link to an Advanced page still opens it, and the current page always
  shows in the menu.
- Never hide anything needed for first run or recovery (libraries, profiles, sign-in, remote
  access, backups, migration, trash, updates).
- Keep an Advanced control visible in Normal while its value differs from the default, so a
  setting never takes effect out of sight.
- Replace the "Maintain" group, the `AdvancedDisclosure` panels and the ad-hoc relay and
  single-folder `<details>` with this one mechanism.
- Simplify while classifying: `/console/access` shows profiles, devices and sign-in; parental
  schedules, filters and grants move to `/console/schedules` (both routes rendered the same page);
  remove the duplicate background-workers field from Server configuration; drop status chips that
  repeat their switch.

## Classification

Normal is the default view. "Kept if set" marks an Advanced control that stays visible in Normal
when its value is not the default.

### Console destinations

| Destination | View | Reason |
|---|---|---|
| Overview | Normal | Landing page and setup checklist |
| Operations | Normal | Job queue and low-power switch |
| Live sessions | Normal | Common monitoring task |
| Play history | Normal | Common monitoring task |
| Library health | Normal | Recovery: unhealthy items |
| Activity | Advanced | Event log for diagnosis |
| Libraries | Normal | First run |
| Plugins | Normal | First run: metadata providers and keys |
| Identification | Advanced | Expert capability switches |
| Layout & Navigation | Advanced | Expert presentation tuning |
| Profiles & access | Normal | First run and recovery: profiles, devices, sign-in |
| Schedules | Normal | Parental controls |
| Remote access | Normal | First run: watch away from home |
| API tokens | Advanced | Integrations |
| Backups | Normal | Recovery |
| Retention rules | Advanced | Expert automation |
| Migration | Normal | First run: import from another server |
| Updates | Normal | Recovery |
| Server configuration | Normal | Hardware transcoding switch |

### Console controls

| Page | Control | View |
|---|---|---|
| Operations | Job queue | Normal |
| Operations | Low-power mode | Normal |
| Operations | Governor status tiles | Advanced |
| Operations | Background workers | Advanced, kept if set (not 1 or 2) |
| Operations | Event retention days | Advanced, kept if set (not 180) |
| Operations | Compact events now | Advanced |
| Server configuration | Hardware transcoding | Normal |
| Server configuration | Codec, quality, device | Advanced, kept if set (not empty) |
| Server configuration | Hardware probe list | Advanced |
| Server configuration | Background workers | Removed (duplicate of Operations) |
| Remote access | Status, overlay and direct HTTPS steps | Normal |
| Remote access | Relay details | Advanced, kept if set (relay enabled) |
| Remote access | qBittorrent ingest | Advanced |
| Libraries | Folders, library options | Normal |
| Libraries | Scan one folder | Advanced |
| Profiles & access | Profiles, sign-in security, device list and pairing | Normal |
| Profiles & access | Remote enrollment | Advanced, kept if set (on) |
| Schedules | Enforced filters, schedules, grants | Normal |
| Schedules | Managed stream cap, extension pre-auth | Advanced |
| Plugins | Activation switch, provider keys, config, status | Normal |
| Plugins | Warnings: unsigned, tampered, revoked, key needed | Normal |
| Plugins | Provenance and Trusted badges | Advanced |
| Plugins | Permissions, actions, uninstall | Advanced |
| Plugins | Active/Inactive chip | Removed (repeats the switch) |
| Plugins | Deactivate button | Removed (repeats the switch) |
| Identification | Enabled/Disabled chip beside each switch | Removed (repeats the switch) |

### Manage destinations

| Destination | View | Reason |
|---|---|---|
| Libraries, Jobs, Subscriptions, Unmatched records | Normal | Everyday library work |
| Trash, Backups | Normal | Recovery |
| Collections, Tags, Manage (entities), Artwork | Normal | Everyday curation |
| Restrictions, Profiles | Normal | Parental controls |
| Convert, Rename | Advanced | Bulk file operations |
| Bulk edit & file operations | Advanced | Bulk file operations |
| Curation (versions, extras, relations) | Advanced | Expert metadata structure |
| Markers | Advanced | Expert playback metadata |
| Field locks | Advanced | Expert metadata protection |
| Navigation override | Advanced | Expert presentation tuning |

### Profile settings

| Control | View |
|---|---|
| Avatar | Normal |
| Auto-skip intro, recap, credits | Normal |
| Playback speed | Normal |
| Reduce motion | Normal |
| Recommendations | Normal |
| Trim silence | Advanced, kept if set (on) |
| Skip interval | Advanced, kept if set (not 10 s) |
| Downstream audio | Advanced, kept if set (not stereo) |

## Non-goals

- A server-side or per-profile preference; the switch is per browser.
- Detecting configured state of a whole destination (for example existing retention rules or
  API tokens) to keep it in the menu. The current page always shows, and deep links work.
- Mounting the orphaned `PlaybackCache` section.

## Capabilities

### New Capabilities

- `web-advanced-settings`: the Normal/Advanced preference and what each view shows.

### Modified Capabilities

None.
