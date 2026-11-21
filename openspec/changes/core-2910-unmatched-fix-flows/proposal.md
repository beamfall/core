# Unmatched: grouped, plain-language fix flows

Ticket: CORE-2910 (`queue:core:main`).

## Why

Manage → Unmatched lists queue rows one by one with scoring jargon (confidence, threshold,
scraper id). The search box starts from the item's own title, which for a child item is the child
title, not the parent's. On Tower (2026-11-21) the first page held 50 rows from two parents:

- ten children of one parent were `ambiguous`: six catalog entries share its exact title, and the
  year in the folder name was not used to tell them apart;
- forty children of another were `below-threshold` (64% against 75%) because the folder name
  carries a country qualifier such as `(US)`.

Fixing them meant one search and one apply per row, and nothing explained the problem.

## What Changes

- The web page groups queue rows that share plugin, scraper and best candidate (or best-candidate
  title when there is none) and fixes a group in one step. It loads every queue page, not just the
  first 50 rows.
- Each group says in plain words what is wrong (several entries fit equally well; the best guess
  only matched N%; nothing matched; no online catalog) and adds a naming hint read from the file
  path: a folder year that the match did not use, or a qualifier in brackets.
- The search title and year start from the parent folder name. Candidates load when a group opens,
  and the candidate whose year matches the folder year is preselected.
- "Match all" applies the chosen candidate to every item in the group through `fixMatch`, shows
  progress and reports any that failed. Paste-a-catalog-id sits behind a disclosure; preview
  stays optional.
- `fixMatchCandidates` takes an optional `year` variable. When it is set, the search fills a
  scraper's `{year}` template slot (with the existing retry without the year) and the scorer
  gets it as `ParsedMedia.Year`.
- A naming panel shows the file path and links to Manage → Libraries (folder rename) and
  Manage → Rename (file rename). A per-group rescan is left out: the selected-rescan path today
  resolves one library and does not reparse a renamed path, so it is a follow-up ticket.
- Hide works per item and per group and says the item may come back on the next scan.

No schema, wire-contract or migration change: `year` is an optional GraphQL variable read like the
existing ones.

## Impact

- `internal/graph/graph_identify.go`, `internal/identify/identify.go`
- `internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx` and `pages/unmatched/`
- `internal/web/app/src/lib/graphql/manage.ts`, `internal/web/app/src/strings/`
