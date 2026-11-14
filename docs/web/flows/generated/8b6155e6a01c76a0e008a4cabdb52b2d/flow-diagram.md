# Player auto\-skip of intro/credits segments with skipped toast — flow-diagram

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:efbe5dbe6004fab2e549142092b9223e -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/transport\.ts:227; symbol: AUTO\_SKIP\_SEGMENT\_KINDS; span SHA256: 519db138d4e94f43eaab0dfd646f015da47f1acb9e06f0829aa2b9e0c001d6c9.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 8b6155e6a01c76a0e008a4cabdb52b2d
    Note over S,U: Source contains call Set in internal/web/app/src/areas/viewer/lib/transport.ts at line 227 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/areas/viewer/lib/transport.ts at line 16 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/player/SkippedOverlay.tsx at line 31 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/player/SkippedOverlay.tsx at line 33 - order unresolved
    Note over S,U: Source contains call setTimeout in internal/web/app/src/areas/viewer/player/SkippedOverlay.tsx at line 34 - order unresolved
    Note over S,U: Source contains call clearTimeout in internal/web/app/src/areas/viewer/player/SkippedOverlay.tsx at line 35 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/SkippedOverlay.tsx at line 42 - order unresolved
    Note over S,U: Source contains call skippedLabelKey in internal/web/app/src/areas/viewer/player/SkippedOverlay.tsx at line 42 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/player/SkippedOverlay.tsx at line 45 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 42 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 43 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 45 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 49 - order unresolved
    Note over S,U: Source contains call setSkipped in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 53 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 56 - order unresolved
    Note over S,U: Source contains call setSkipped in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 57 - order unresolved
    Note over S,U: Source contains call autoSkipSegmentAt in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 82 - order unresolved
    Note over S,U: Source contains call autoSkipSegmentKey in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 90 - order unresolved
    Note over S,U: Source contains call setSkipped in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 96 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 60 - order unresolved
    Note over S,U: Source contains call autoSkipSegmentKey in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 67 - order unresolved
    Note over S,U: Source contains call setSkipped in internal/web/app/src/areas/viewer/player/useAutoSkip.ts at line 70 - order unresolved
```
