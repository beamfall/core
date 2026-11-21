## Why

CORE-2884: the Music › Categories page shows only the handful of genres that file tags carry (often
none, or ID3v1 codes such as "(12)"). Plugins can already describe a scraper that knows an entity's
classification, but core has no way to run a lookup per entity (rather than per Media item) and fan
the result out to that entity's member Media. Categories therefore stay sparse even when the library
is fully identified.

## What Changes

- Plugins contribute an entity enrichment through a server hook: the entity kind to enrich, the
  relation that lists its member Media, an optional context relation, the target entity kind, the
  relation to write, a declared scraper and the query, candidate-pick and name-selection callbacks.
- Core validates each enrichment against the plugin's declared entity kinds, relations and scrapers,
  and drops a faulty one with a warning.
- A deduplicated background job walks the shared catalog's entities of that kind (encrypted libraries
  are never walked), searches and looks up the scraper, resolves target entities by folded title and
  replaces the member Media's relation rows under the enrichment's own relation name.
- A new `entity_enrichment` table records each entity's last check: matched entities are not
  rechecked, unmatched ones are retried after 30 days, and a rate-limited or failing provider ends
  the pass and requeues it later.
- The job runs at boot and after each scan, governed like the other background sweeps.
