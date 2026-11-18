# Library health counts what a library's plugin can actually fix

Tickets: CORE-2340, CORE-2383 (`queue:core:main`)

## Why

Library health judges every library as if a film-and-TV scraper owned it. On the tower, a music
library shows all 7932 items as unmatched, scrape failures and missing artwork: its items are
identified from local tags and carry square cover art, and no scorer auto-identifies them, so
`title_identified` stays 0 for all of them. A TV library shows 1589 items missing artwork because
episodes take a thumb, not a poster. A library with no plugin shows 1499 on every dimension and
offers Fix match, Rescan and Fetch buttons that cannot help.

## What changes

- Core derives a per-plugin health profile from the loaded bundle: whether a plugin owns the
  library, whether it has a searchable scraper, whether it auto-identifies, the conventional
  class field, and the artwork slots its scrapers supply for each Media archetype (the scraper's
  implied class). Core holds no domain words; the profile is built from bundle declarations.
- A Media counts as identified when its title was identified, it carries an external ID, or the
  plugin's local hydration marked it complete and the plugin has no auto-identification.
- `unmatched` applies only when the plugin has a searchable scraper. `scrape-failure` applies only
  when the plugin auto-identifies, and counts unidentified items with an attempted scrape that
  queued them as unmatched (not manual-review queueing).
- New dimension `no-scraper`: unidentified items in a library no plugin can search for.
- `missing-artwork` applies only when the plugin's scrapers supply artwork. An item is missing
  artwork when none of its archetype's slots (or the poster that those slots fall back to) has an
  applied candidate or a stored, non-generated file (embedded or sidecar art counts).
- A dimension that does not apply is left out of the counts map, and its drill-down, cache rows
  and whole-library bulk actions are empty or refused.
- The console shows a not-applicable dimension as "Not applicable" with no buttons, shows
  "No plugin identifies this library" when `no-scraper` is non-zero, and disables Rescan unmatched
  with a reason when the plugin does not auto-identify.

## Non-goals

- No wire shape change: the counts map is already keyed by dimension name, and an absent key is
  read as not applicable.
- No auto-identification scorer for plugins that lack one.
- No change to duplicate or broken detection.

## Failure modes

- A plugin whose bundle is not loaded is treated as no plugin: identification dimensions do not
  apply and `no-scraper` counts its unidentified items.
- With no profiler installed (tests, tools), a library with a plugin keeps the earlier behaviour
  with poster as its only artwork slot.
- An artwork file that cannot be read is treated as missing.
