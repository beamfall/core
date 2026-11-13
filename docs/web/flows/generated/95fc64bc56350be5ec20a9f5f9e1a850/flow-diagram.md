# Viewer area: session pending/error states, setup hand\-off, viewer routes — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:a37b18a6b52aee290eceb8133096f236 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/index\.tsx:26; symbol: ViewerSessionError; span SHA256: 15e7bb4f0c6cd0210e45e8d78744ef6e8bf99159c99e202a3085a6d1ead15fbf.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 95fc64bc56350be5ec20a9f5f9e1a850
    Note over S,U: Source contains call useT at line 27 - order unresolved
    Note over S,U: Source contains call onRetry at line 32 - order unresolved
    Note over S,U: Source contains call t at line 33 - order unresolved
    Note over S,U: Source contains call useT at line 46 - order unresolved
    Note over S,U: Source contains call t at line 50 - order unresolved
    Note over S,U: Source contains call useRegister at line 65 - order unresolved
    Note over S,U: Source contains call useSession at line 66 - order unresolved
    Note over S,U: Source contains call useLocation at line 67 - order unresolved
    Note over S,U: Source contains call useEffect at line 76 - order unresolved
    Note over S,U: Source contains call setRegister at line 76 - order unresolved
    Note over S,U: Source contains call useEffect at line 78 - order unresolved
    Note over S,U: Source contains call fetchSetupRequired at line 81 - order unresolved
    Note over S,U: Source contains call then at line 81 - order unresolved
    Note over S,U: Source contains call setSetupRequired at line 82 - order unresolved
    Note over S,U: Source contains call startsWith at line 121 - order unresolved
    Note over S,U: Source contains call profilePickedThisTab at line 122 - order unresolved
    Note over S,U: Source contains call useEngagementEvents at line 133 - order unresolved
```
