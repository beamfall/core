# Search quality — matching and ranking contract

Binding behavior spec for catalog/music search (MUSIC-PARITY-SEARCH-QUALITY). A 2026-07-05 expert
panel found search behavior — central to daily use — was unspecified in any binding ADR (absent in
ADR-0083). This document specifies the implemented contract; the ranking primitives live in
[`internal/store/search_quality.go`](../../internal/store/search_quality.go) and the cross-entity
merge in [`internal/graph/graph_enrichment.go`](../../internal/graph/graph_enrichment.go).

## 1. Text normalization (diacritic folding)

`foldSearchText` folds both the query and candidate text identically before any comparison:
lowercase → Unicode NFD decomposition → drop combining marks (`unicode.Mn`) → keep letters/digits,
map every other rune to a space → collapse runs of whitespace. This makes matching
diacritic-insensitive and punctuation-insensitive (`beyonce` matches `Beyoncé`; `ac dc` matches
`AC/DC`).

## 2. Match scoring (typo / fuzzy tolerance)

`SearchRank(query, parts...)` returns a **rank cost where lower is better**, or `-1` when the query
does not match at all. The folded query is split into terms; every term must match, and its cost is
the best (lowest) of:

- substring hit anywhere in the concatenated candidate text → `10`;
- per-word `wordSearchScore(term, word)`: exact `0`, prefix `1`, substring `4`, else a bounded
  Levenshtein distance `d` → `20 + d`.

Edit-distance tolerance scales with term length: `maxEdits = 1`, or `2` for terms longer than 5
runes (`boundedEditDistance` early-exits once the bound is exceeded). A term with no match under any
rule fails the whole query (`-1`). Per-term costs are summed, so more-exact matches rank ahead of
fuzzier ones.

## 3. Visibility before ranking

Profile visibility is a **hard pre-filter, not a rank penalty**. `fuzzySearchMediaLocked` and
`fuzzySearchEntitiesLocked` resolve the caller's visible libraries via `libraryVisible(filter)` and
constrain the scan to them (plus certification / playback-gate filters) **before** any scoring, so a
hidden or Sealed entity is never scored and can never surface — even when it would out-rank a visible
match.

## 4. Cross-entity merge

`searchResult` unions media results (`SearchMedia`) and generic entities
(`SearchEntities`, Artist/Album/Track/Playlist and other archetypes), then re-sorts the combined set
by `SearchRank(query, Title)`. Unmatched items (`-1`) sort last via a large sentinel; ties break by
`Kind`, then `ID`, for a stable order. The merged list is truncated to the response limit (50).

## 5. Graceful degradation

Fuzzy scoring runs in Go, so the SQL scan is capped at `fuzzySearchScanLimit = 5000` rows per source
(applied as a `LIMIT` on the visibility-filtered, `title`-ordered query). Large libraries therefore
degrade to a bounded, deterministic candidate window instead of an unbounded full-table fuzzy scan.

## Verification

Covered by `internal/store/sqlite_test.go`, `internal/store/entity_index_test.go`, and
`internal/graph/graph_test.go`: diacritic (`beyonce`→`Beyoncé`) and typo (`halp`, `sasha fierc`,
`lemonde`) queries match; a hidden-library exact match is excluded while a visible fuzzy match ranks;
exact-title entities rank ahead of fuzzy-title media.
