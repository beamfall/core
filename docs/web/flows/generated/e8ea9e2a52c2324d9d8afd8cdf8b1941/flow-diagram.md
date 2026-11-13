# Viewer search page: modes \(text, semantic, recommend\) and grouped results — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

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
    Note over S,U: Source contains call setQuery in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 81 - order unresolved
    Note over S,U: Source contains call focus in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 82 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 90 - order unresolved
    Note over S,U: Source contains call playQueueOf in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 90 - order unresolved
    Note over S,U: Source contains call flatMap in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 90 - order unresolved
    Note over S,U: Source contains call setParams in internal/web/app/src/areas/viewer/pages/SearchPage.tsx at line 77 - order unresolved
```
