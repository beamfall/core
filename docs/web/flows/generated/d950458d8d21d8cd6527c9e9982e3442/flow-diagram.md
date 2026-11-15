# Player route: lazy load, timestamp links, info overlay, audio stage — flow-diagram

Source revision: 03e521f81ef085ec14c6d7d5d76533692ecba36c. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:8f08f6465223fb0da070854aacfafa6d -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/pages/PlayerPage\.tsx:86; symbol: PlayerPage; span SHA256: 23cf3dbe101367b8eb37216ba93bd2f3395527a8ecb2a14a2b116775aff2b593.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow d950458d8d21d8cd6527c9e9982e3442
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 87 - order unresolved
    Note over S,U: Source contains call useLocation in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 88 - order unresolved
    Note over S,U: Source contains call useParams in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 89 - order unresolved
    Note over S,U: Source contains call URLSearchParams in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 90 - order unresolved
    Note over S,U: Source contains call has in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 90 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 96 - order unresolved
    Note over S,U: Source contains call AbortController in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 98 - order unresolved
    Note over S,U: Source contains call resolve in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 99 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 100 - order unresolved
    Note over S,U: Source contains call fetchPrePlayQueue in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 100 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 101 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 102 - order unresolved
    Note over S,U: Source contains call setDecided in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 103 - order unresolved
    Note over S,U: Source contains call abort in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 105 - order unresolved
    Note over S,U: Source contains call URL in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 735 - order unresolved
    Note over S,U: Source contains call set in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 739 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 739 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 739 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 739 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 729 - order unresolved
    Note over S,U: Source contains call Number in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 730 - order unresolved
    Note over S,U: Source contains call isFinite in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 731 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 731 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/player/AudioStage.tsx at line 23 - order unresolved
    Note over S,U: Source contains call creditLine in internal/web/app/src/areas/viewer/player/AudioStage.tsx at line 25 - order unresolved
    Note over S,U: Source contains call creditLine in internal/web/app/src/areas/viewer/player/AudioStage.tsx at line 26 - order unresolved
    Note over S,U: Source contains call creditLine in internal/web/app/src/areas/viewer/player/AudioStage.tsx at line 27 - order unresolved
    Note over S,U: Source contains call setFailedURL in internal/web/app/src/areas/viewer/player/AudioStage.tsx at line 47 - order unresolved
    Note over S,U: Source contains call replace in internal/web/app/src/areas/viewer/player/AudioStage.tsx at line 75 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 41 - order unresolved
    Note over S,U: Source contains call buildRouteDetailModel in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 43 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 49 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 50 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 51 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 52 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 53 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 54 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 55 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 56 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 57 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 64 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 68 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 75 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 78 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 81 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 83 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/InfoOverlay.tsx at line 85 - order unresolved
    Note over S,U: Source contains call lazyRoute in internal/web/app/src/areas/viewer/player/LazyPlayerPage.ts at line 6 - order unresolved
    Note over S,U: Source contains call preload in internal/web/app/src/areas/viewer/player/preload.ts at line 9 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/player/preload.ts at line 9 - order unresolved
    Note over S,U: Source contains call navigate in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 564 - order unresolved
    Note over S,U: Source contains call navigate in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 565 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 366 - order unresolved
    Note over S,U: Source contains call goToQueueItem in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 368 - order unresolved
    Note over S,U: Source contains call advanceToNext in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 369 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 378 - order unresolved
    Note over S,U: Source contains call goToQueueItem in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 379 - order unresolved
    Note over S,U: Source contains call playItem in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 380 - order unresolved
```
