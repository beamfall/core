# Player OS media\-session integration \(lock\-screen controls\) — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:44e0c94c18165c1f66a949213f14b5c2 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/player/useMediaSession\.ts:43; symbol: useMediaSession; span SHA256: ff8020ab582e5523fcf9b66f119ab9318c22b59e45a5ec9e95812fc057cbcfa6.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow ab85b00b0ef1be60ae43722b7edab162
    Note over S,U: Source contains call useEffect at line 57 - order unresolved
    Note over S,U: Source contains call MediaMetadata at line 64 - order unresolved
    Note over S,U: Source contains call useEffect at line 76 - order unresolved
    Note over S,U: Source contains call setActionHandler at line 84 - order unresolved
    Note over S,U: Source contains call isFinite at line 120 - order unresolved
    Note over S,U: Source contains call setPositionState at line 122 - order unresolved
    Note over S,U: Source contains call min at line 124 - order unresolved
```
