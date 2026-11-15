# Player route: lazy load, timestamp links, info overlay, audio stage — flow-diagram

Source revision: a75e0f8437987a575a00a20b8d515be33b584f10. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:8f08f6465223fb0da070854aacfafa6d -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/pages/PlayerPage\.tsx:94; symbol: PlayerPage; span SHA256: 23cf3dbe101367b8eb37216ba93bd2f3395527a8ecb2a14a2b116775aff2b593.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow d950458d8d21d8cd6527c9e9982e3442
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 95 - order unresolved
    Note over S,U: Source contains call useLocation in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 96 - order unresolved
    Note over S,U: Source contains call useParams in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 97 - order unresolved
    Note over S,U: Source contains call usePlayerHost in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 98 - order unresolved
    Note over S,U: Source contains call URLSearchParams in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 99 - order unresolved
    Note over S,U: Source contains call has in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 99 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 105 - order unresolved
    Note over S,U: Source contains call AbortController in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 107 - order unresolved
    Note over S,U: Source contains call resolve in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 108 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 109 - order unresolved
    Note over S,U: Source contains call fetchPrePlayQueue in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 109 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 110 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 111 - order unresolved
    Note over S,U: Source contains call setDecided in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 112 - order unresolved
    Note over S,U: Source contains call abort in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 114 - order unresolved
    Note over S,U: Source contains call URL in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 866 - order unresolved
    Note over S,U: Source contains call set in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 870 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 870 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 870 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 870 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 860 - order unresolved
    Note over S,U: Source contains call Number in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 861 - order unresolved
    Note over S,U: Source contains call isFinite in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 862 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 862 - order unresolved
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
    Note over S,U: Source contains call navigate in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 650 - order unresolved
    Note over S,U: Source contains call navigate in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 651 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 419 - order unresolved
    Note over S,U: Source contains call goToQueueItem in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 421 - order unresolved
    Note over S,U: Source contains call advanceToNext in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 422 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 431 - order unresolved
    Note over S,U: Source contains call goToQueueItem in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 432 - order unresolved
    Note over S,U: Source contains call playItem in internal/web/app/src/areas/viewer/pages/PlayerPage.tsx at line 433 - order unresolved
```
