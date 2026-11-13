# Viewer search page: modes \(text, semantic, recommend\) and grouped results — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:7869fcca506e4a6b12f352808d0ad882 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/searchModes\.ts:33; symbol: availableModes; span SHA256: 5f4a403960512cc16dcbe3f84c42469146299f7a38480c983ce1ffedf91d050c.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow e8ea9e2a52c2324d9d8afd8cdf8b1941
    Note over S,U: Source contains call semanticAvailable in internal/web/app/src/areas/viewer/lib/searchModes.ts at line 35 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/searchModes.ts at line 35 - order unresolved
    Note over S,U: Source contains call recommendAvailable in internal/web/app/src/areas/viewer/lib/searchModes.ts at line 36 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/viewer/lib/searchModes.ts at line 36 - order unresolved
    Note over S,U: Source contains call some in internal/web/app/src/areas/viewer/lib/searchModes.ts at line 24 - order unresolved
    Note over S,U: Source contains call some in internal/web/app/src/areas/viewer/lib/searchModes.ts at line 19 - order unresolved
    Note over S,U: Source contains call split in internal/web/app/src/areas/viewer/lib/searchRows.ts at line 28 - order unresolved
    Note over S,U: Source contains call values in internal/web/app/src/areas/viewer/lib/searchRows.ts at line 31 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/viewer/lib/searchRows.ts at line 60 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/viewer/lib/searchRows.ts at line 60 - order unresolved
    Note over S,U: Source contains call join in internal/web/app/src/areas/viewer/lib/searchRows.ts at line 60 - order unresolved
    Note over S,U: Source contains call encodeURIComponent in internal/web/app/src/areas/viewer/lib/searchRows.ts at line 88 - order unresolved
    Note over S,U: Source contains call useI18n in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 43 - order unresolved
    Note over S,U: Source contains call useDisplayLocale in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 44 - order unresolved
    Note over S,U: Source contains call useSearchParams in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 45 - order unresolved
    Note over S,U: Source contains call get in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 46 - order unresolved
    Note over S,U: Source contains call useLiveSearch in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 51 - order unresolved
    Note over S,U: Source contains call useSearchShortcut in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 52 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 56 - order unresolved
    Note over S,U: Source contains call allSettled in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 58 - order unresolved
    Note over S,U: Source contains call fetchSemanticCapability in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 58 - order unresolved
    Note over S,U: Source contains call fetchClientPrefs in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 58 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 58 - order unresolved
    Note over S,U: Source contains call setCaps in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 60 - order unresolved
    Note over S,U: Source contains call setPrefs in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 61 - order unresolved
    Note over S,U: Source contains call availableModes in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 68 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 71 - order unresolved
    Note over S,U: Source contains call includes in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 72 - order unresolved
    Note over S,U: Source contains call setMode in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 72 - order unresolved
    Note over S,U: Source contains call setQuery in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 81 - order unresolved
    Note over S,U: Source contains call focus in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 82 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 90 - order unresolved
    Note over S,U: Source contains call playQueueOf in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 90 - order unresolved
    Note over S,U: Source contains call flatMap in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 90 - order unresolved
    Note over S,U: Source contains call setParams in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 77 - order unresolved
```
