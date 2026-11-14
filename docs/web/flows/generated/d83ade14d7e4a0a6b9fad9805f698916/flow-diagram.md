# Unreached code: unmounted modules and GraphQL operations no UI calls — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:74b00a777fe8dc79a08c7bd7525e8607 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/lib/previewMedia\.ts:9; symbol: PreviewRoute; span SHA256: e627ff6ddae00450f20b2bc704daa8ac0dc345d71844ed1d9c0529d12e7b772a.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow d83ade14d7e4a0a6b9fad9805f698916
    Note over S,U: Source contains call requestOpenInNativePlayer in internal/web/app/src/areas/console/lib/previewMedia.ts at line 26 - order unresolved
    Note over S,U: Source contains call webPlayerPath in internal/web/app/src/areas/console/lib/previewMedia.ts at line 29 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 48 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 49 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 50 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 31 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 34 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 23 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 23 - order unresolved
    Note over S,U: Source contains call log in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 23 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 25 - order unresolved
    Note over S,U: Source contains call toFixed in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 25 - order unresolved
    Note over S,U: Source contains call useI18n in internal/web/app/src/areas/setup/MigrationOffer.tsx at line 39 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/setup/MigrationOffer.tsx at line 41 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/setup/MigrationOffer.tsx at line 42 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/setup/MigrationOffer.tsx at line 44 - order unresolved
    Note over S,U: Source contains call setImporters in internal/web/app/src/areas/setup/MigrationOffer.tsx at line 46 - order unresolved
    Note over S,U: Source contains call setFailed in internal/web/app/src/areas/setup/MigrationOffer.tsx at line 47 - order unresolved
    Note over S,U: Source contains call fetchImporters in internal/web/app/src/areas/setup/MigrationOffer.tsx at line 48 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/areas/setup/MigrationOffer.tsx at line 49 - order unresolved
    Note over S,U: Source contains call setImporters in internal/web/app/src/areas/setup/MigrationOffer.tsx at line 50 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/setup/MigrationOffer.tsx at line 52 - order unresolved
    Note over S,U: Source contains call setFailed in internal/web/app/src/areas/setup/MigrationOffer.tsx at line 53 - order unresolved
    Note over S,U: Source contains call evictPlaybackRenditionCache in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 61 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 62 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 53 - order unresolved
    Note over S,U: Source contains call precomputePlaybackRenditions in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 55 - order unresolved
    Note over S,U: Source contains call setMediaID in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 56 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/PlaybackCache.tsx at line 57 - order unresolved
    Note over S,U: Source contains call assign in internal/web/app/src/areas/setup/MigrationOffer.tsx at line 65 - order unresolved
```
