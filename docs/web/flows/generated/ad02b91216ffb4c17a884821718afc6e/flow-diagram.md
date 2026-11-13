# Viewer See\-All: paginate a Source from a shelf — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:35dc7221ff6127a19e79c18c39766408 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/lib/seeall\.ts:9; symbol: SeeAllContext; span SHA256: 921399fdb693119d57af0c253f1ee17c14f48420ced5c43e2f5ba1b72b0af4aa.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow ad02b91216ffb4c17a884821718afc6e
    Note over S,U: Source contains call useT at line 24 - order unresolved
    Note over S,U: Source contains call useDisplayLocale at line 25 - order unresolved
    Note over S,U: Source contains call useNavigate at line 26 - order unresolved
    Note over S,U: Source contains call useLocation at line 27 - order unresolved
    Note over S,U: Source contains call useState at line 31 - order unresolved
    Note over S,U: Source contains call useState at line 32 - order unresolved
    Note over S,U: Source contains call useRef at line 33 - order unresolved
    Note over S,U: Source contains call useRef at line 35 - order unresolved
    Note over S,U: Source contains call useEngagementEpoch at line 36 - order unresolved
    Note over S,U: Source contains call useRef at line 37 - order unresolved
    Note over S,U: Source contains call useVirtualGrid at line 39 - order unresolved
    Note over S,U: Source contains call slice at line 46 - order unresolved
    Note over S,U: Source contains call navigate at line 118 - order unresolved
    Note over S,U: Source contains call useCallback at line 49 - order unresolved
    Note over S,U: Source contains call async at line 49 - order unresolved
    Note over S,U: Source contains call setBusy at line 52 - order unresolved
    Note over S,U: Source contains call fetchSectionItems at line 54 - order unresolved
    Note over S,U: Source contains call setItems at line 60 - order unresolved
    Note over S,U: Source contains call setHasMore at line 62 - order unresolved
    Note over S,U: Source contains call setHasMore at line 64 - order unresolved
    Note over S,U: Source contains call setBusy at line 66 - order unresolved
    Note over S,U: Source contains call useMemo at line 47 - order unresolved
    Note over S,U: Source contains call playQueueOf at line 47 - order unresolved
    Note over S,U: Source contains call useCallback at line 73 - order unresolved
    Note over S,U: Source contains call async at line 73 - order unresolved
    Note over S,U: Source contains call max at line 76 - order unresolved
    Note over S,U: Source contains call setBusy at line 79 - order unresolved
    Note over S,U: Source contains call fetchSectionItems at line 82 - order unresolved
    Note over S,U: Source contains call push at line 88 - order unresolved
    Note over S,U: Source contains call setItems at line 91 - order unresolved
    Note over S,U: Source contains call setHasMore at line 93 - order unresolved
    Note over S,U: Source contains call setBusy at line 97 - order unresolved
```
