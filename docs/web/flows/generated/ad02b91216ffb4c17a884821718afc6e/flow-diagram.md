# Viewer See\-All: paginate a Source from a shelf — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:35dc7221ff6127a19e79c18c39766408 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/seeall\.ts:9; symbol: SeeAllContext; span SHA256: 921399fdb693119d57af0c253f1ee17c14f48420ced5c43e2f5ba1b72b0af4aa.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow ad02b91216ffb4c17a884821718afc6e
    Note over S,U: Source contains call useT at line 27 - order unresolved
    Note over S,U: Source contains call useDisplayLocale at line 28 - order unresolved
    Note over S,U: Source contains call useNavigate at line 29 - order unresolved
    Note over S,U: Source contains call useLocation at line 30 - order unresolved
    Note over S,U: Source contains call useState at line 34 - order unresolved
    Note over S,U: Source contains call useState at line 35 - order unresolved
    Note over S,U: Source contains call useState at line 36 - order unresolved
    Note over S,U: Source contains call useRef at line 37 - order unresolved
    Note over S,U: Source contains call useRef at line 39 - order unresolved
    Note over S,U: Source contains call useEngagementEpoch at line 40 - order unresolved
    Note over S,U: Source contains call useRef at line 41 - order unresolved
    Note over S,U: Source contains call useVirtualGrid at line 43 - order unresolved
    Note over S,U: Source contains call slice at line 50 - order unresolved
    Note over S,U: Source contains call navigate at line 128 - order unresolved
    Note over S,U: Source contains call useCallback at line 53 - order unresolved
    Note over S,U: Source contains call async at line 53 - order unresolved
    Note over S,U: Source contains call setBusy at line 56 - order unresolved
    Note over S,U: Source contains call fetchSectionItems at line 58 - order unresolved
    Note over S,U: Source contains call setItems at line 64 - order unresolved
    Note over S,U: Source contains call setHasMore at line 66 - order unresolved
    Note over S,U: Source contains call setFailed at line 67 - order unresolved
    Note over S,U: Source contains call setFailed at line 69 - order unresolved
    Note over S,U: Source contains call setBusy at line 71 - order unresolved
    Note over S,U: Source contains call useMemo at line 51 - order unresolved
    Note over S,U: Source contains call playQueueOf at line 51 - order unresolved
    Note over S,U: Source contains call useCallback at line 78 - order unresolved
    Note over S,U: Source contains call async at line 78 - order unresolved
    Note over S,U: Source contains call max at line 81 - order unresolved
    Note over S,U: Source contains call setBusy at line 84 - order unresolved
    Note over S,U: Source contains call fetchSectionItems at line 87 - order unresolved
    Note over S,U: Source contains call push at line 93 - order unresolved
    Note over S,U: Source contains call setItems at line 96 - order unresolved
    Note over S,U: Source contains call setHasMore at line 98 - order unresolved
    Note over S,U: Source contains call setBusy at line 102 - order unresolved
```
