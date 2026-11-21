# Source item library provenance

CORE-1496 covers the Core producer for library provenance on cross-library client surfaces.

## Why

A client applying library policy to Home, Search, or a paged source needs to know which library
owns each row, even when multiple libraries contain items with the same title.

## What Changes

- Expose optional `libraryID` on `SourceItem` and `StationCandidate`. Catalog-backed rows carry the
  library from the same read as the title. A rolled-up entity carries its own library.
- Keep the existing `searchResult.items.libraryID` wire key and existing session visibility.
- Prepare the additive client-core-wire 2.39.0 producer artifact for consumer repins.
