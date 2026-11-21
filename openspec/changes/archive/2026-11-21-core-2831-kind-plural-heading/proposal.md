# Head related-entity groups with the kind's plural display name

Ticket: CORE-2831 (`queue:core:main`). SDK side: CORE-2820 (plugin-sdk v0.4.7).

## Why

An artist page heads its albums "Album": the group heading is the kind's singular display name.
Plex and every other library app head a list of albums "Albums". Plugin SDK v0.4.7 lets a plugin
declare `EntityKind.PluralDisplayName`, and the first-party plugins re-released against it declare
plurals for their kinds.

## What Changes

- Each `relatedEntities` group on the `entity(id)` read carries `kindPluralDisplayName`, the owning
  plugin's plural display name for the kind, omitted when the declaration carries none.
- The web Entity view heads a related group (and labels the sibling switcher) with the plural,
  then the display name, then the kind.
- Core pins plugin-sdk v0.4.7 and the plugin releases that declare SDK range [0.4.7, 0.4.7].
- Wire contract 2.40.0 records the additive field.

## Non-goals

- Library browse kind tabs keep their existing label rule.
- Person groups shown as cast-row departments keep the singular display name per credit.
- Native clients adopt the field in their own repositories.
