# Media ratings on the detail page

Ticket: CORE-2399 (`queue:core:main`), the first slice of CORE-0363, CORE-0361 and CORE-1219.

## Why

Players such as Plex and Infuse show a community score and the content rating on every title.
Beamfall's movie and TV plugin already fetches the TMDB documents that carry both, but maps
neither, so the detail page has no rating at all for identified Movies and series.

## What changes

- plugin-filmtv (branch `feat/media-ratings`) declares two number Media fields,
  `community-rating` (the provider's 0 to 10 score; the field label names the provider, "TMDB
  rating") and `community-rating-votes`, and maps from the TMDB documents it already requests:
  - Movies: `vote_average`, `vote_count`, and the US theatrical (else digital, else first)
    release certification from `append_to_response=release_dates` into `certification` and
    `certification-display`.
  - Series: `vote_average`, `vote_count`, and the US content rating from
    `append_to_response=content_ratings` into `certification-display` only. TV ratings sit outside
    the MPAA ordering that drives Profile ceilings, so `certification` stays unset for series.
  - Episodes: their own `vote_average` and `vote_count`.
- Core gains no vocabulary. `community-rating`, `community-rating-votes` and
  `certification-display` join `certification` as core-conventional Media field names that the
  web detail page reads; their labels come from the plugin.
- Web detail page: the facts row shows the rating as a bordered badge, reading
  `certification-display` and falling back to `certification`, and a score chip "★ 7.3" whose
  accessible name gives the plugin's field label, the score out of 10 and the vote count. The
  score shows only when both the score and the vote count are above zero. These fields leave the
  generic field list.

## Non-goals

- No IMDb or Rotten Tomatoes scores: neither has a licensed source without a key, and both forbid
  scraping. An OMDb-backed provider stays with CORE-0363 and CORE-0370.
- No browse-card badge: `SourceItem` carries no fields, so that needs a wire contract change
  (follow-up ticket, after wire 2.23.0 lands).
- No change to Profile ceilings or to the certification scale.

## Failure modes

- A title with no US release certification or content rating shows no badge.
- A title TMDB has no votes for (score 0, votes 0) shows no score chip.
- Libraries identified before the plugin release keep showing nothing until they are refreshed.
- A plugin that declares neither field leaves the facts row as today.

## Licensing

TMDB data is used under the TMDB API terms; the plugin's existing attribution ("This product uses
the TMDB API but is not endorsed or certified by TMDB.") covers the added fields, and the field
label names TMDB as the score's source.

## Acceptance evidence

plugin-filmtv tests pinning the lookup templates, field declarations and mappings; vitest coverage
for the badge, the score chip and its accessible name, and the fallback; string hash pins
recomputed; `openspec validate --all --strict`.

## Rollback

Revert the web commit; the plugin fields are then listed as ordinary fields. Reverting the plugin
release stops new values; stored values stay until the next refresh.
