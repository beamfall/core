# Show plugin-declared metadata provider attribution to end users

Ticket: CORE-1126 (`queue:core:main`).

## Why

Some metadata sources license their data on the condition that the people who see it are told
where it came from. One source's terms ask for an attribution line with a direct link to its site
wherever its metadata is shown; another asks for a notice on an about or credits surface. Plugins
can now declare that credit as data (`ScraperSpec.Attribution`, plugin-sdk v0.3.4: a locale-map
text and an https link), but Core records nothing about which scraper supplied a Media item's
metadata and never surfaces the declared credit, so no client can honour those terms.

## What Changes

- Identification records provenance. When `identify.Service.Apply` writes metadata onto a Media
  item it stores the plugin and scraper that supplied it in a new catalog table
  (`media_metadata_provenance`, one row per Media item, migration `0112`). A later identification
  overwrites the row; deleting the Media item deletes it (`ON DELETE CASCADE`). Core has no
  "unmatch" path that clears identification, so nothing else clears it.
- `media(id)` gains `metadataAttribution { providerName text url }`, resolved at read time from the
  active bundle's `ScraperSpec.Attribution` for the recorded plugin and scraper. It is absent when
  the item has no provenance, the plugin is not active, the scraper is gone, or the scraper declares
  no attribution. `text` and `providerName` are plugin locale maps, rendered by clients like every
  other plugin string.
- Console `PluginScraperInfo` gains `attribution { text url }` so operators can see what each
  scraper will credit.
- A new session-scoped read, `metadataCredits`, lists the attribution of every active plugin's
  scrapers that declare one, in activation then declaration order, with identical text and link
  pairs listed once.
- Web viewer: the player's Media info panel shows the attribution text as an external link
  (`target="_blank"`, `rel="noopener noreferrer"`), and Profile settings gains a Credits section
  listing `metadataCredits`. Only `https:` links are rendered as links; anything else renders as
  plain text.
- The attribution text and link come only from plugin data; Core carries no provider names.

Verification note: the two bundled declarations were checked against the sources' own terms as read
on 2026-11-11 (https://www.themoviedb.org/api-terms-of-use and
https://www.thetvdb.com/api-information#attribution). The wording lives in the plugin, not here.

## Non-goals

- Provider logos. One source's terms also ask for its logo; the SDK has no logo or asset field yet,
  so the logo is a follow-up (SDK field first, then Core and clients).
- Native clients (Apple, Android, TV). They consume the same wire fields in their own follow-up
  tickets.
- Per-field provenance. The record names the scraper of the most recent identification; a manual
  edit of title or summary does not remove the attribution, because the rest of the identification
  (fields, Entities, artwork) still came from that source.
- Entity-level attribution. Entity pages credit nothing until Entities record provenance.
- Backfilling provenance for items identified before this change; they show attribution after
  their next identification.

## Failure modes

- The provenance write fails after the metadata write: `Apply` returns the error, like a failed
  Media-field write, and re-running identification heals it.
- The plugin that identified an item is deactivated or upgraded to a release without the scraper or
  without an attribution: the read omits `metadataAttribution` and the credits list drops it; the
  stale provenance row is harmless and is overwritten by the next identification.
- A declared link that is not https (impossible for a bundle that passed SDK validation) renders as
  text, never as a link.

## Acceptance evidence

- `internal/store`: `TestMediaMetadataProvenanceUpsertAndCascade`.
- `internal/identify`: `TestApplyRecordsMetadataProvenance`,
  `TestApplyOverwritesMetadataProvenanceOnReidentify`.
- `internal/graph`: `TestMediaMetadataAttributionFromActiveScraper`,
  `TestMediaMetadataAttributionAbsentWhenPluginInactiveOrUndeclared`,
  `TestMetadataCreditsDedupesIdenticalAttribution`, `TestPluginScraperInfoCarriesAttribution`.
- Web: `InfoOverlay` and `ProfileSettingsPage` tests render the attribution link with
  `rel="noopener noreferrer"` and refuse non-https links.
- `internal/conformance` (vocabulary firewall, wire goldens) passes with regenerated goldens.

## Rollback

Revert the change. The `media_metadata_provenance` table stays behind (migrations are append-only)
and is unused; the wire fields disappear, which old clients already tolerate.

## Capabilities

### New Capabilities

- `metadata-provider-attribution`: recording which plugin scraper supplied a Media item's metadata
  and surfacing that scraper's declared attribution to viewers.

### Modified Capabilities

None.
