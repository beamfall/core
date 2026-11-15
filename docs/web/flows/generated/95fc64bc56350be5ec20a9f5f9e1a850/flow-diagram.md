# Viewer area: session pending/error states, setup hand\-off, viewer routes — flow-diagram

Source revision: 684328a3dab29cbba9cf1058ce382b864f4b6b2d. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:a37b18a6b52aee290eceb8133096f236 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/index\.tsx:70; symbol: ViewerArea; span SHA256: e8c23e5be0c66a622011605c335083f61b506242b50e7f5c4047f2d83ae145ca.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 95fc64bc56350be5ec20a9f5f9e1a850
    Note over S,U: Source contains call useRegister at line 71 - order unresolved
    Note over S,U: Source contains call useSession at line 72 - order unresolved
    Note over S,U: Source contains call useLocation at line 73 - order unresolved
    Note over S,U: Source contains call useScrollRestoration at line 81 - order unresolved
    Note over S,U: Source contains call useEffect at line 83 - order unresolved
    Note over S,U: Source contains call setRegister at line 83 - order unresolved
    Note over S,U: Source contains call useEffect at line 85 - order unresolved
    Note over S,U: Source contains call fetchSetupRequired at line 88 - order unresolved
    Note over S,U: Source contains call then at line 88 - order unresolved
    Note over S,U: Source contains call setSetupRequired at line 89 - order unresolved
    Note over S,U: Source contains call startsWith at line 128 - order unresolved
    Note over S,U: Source contains call profilePickedThisTab at line 129 - order unresolved
    Note over S,U: Source contains call useRemoteReceiver at line 141 - order unresolved
    Note over S,U: Source contains call useEngagementEvents at line 142 - order unresolved
    Note over S,U: Source contains call useT at line 33 - order unresolved
    Note over S,U: Source contains call onRetry at line 38 - order unresolved
    Note over S,U: Source contains call t at line 39 - order unresolved
    Note over S,U: Source contains call useT at line 52 - order unresolved
    Note over S,U: Source contains call t at line 56 - order unresolved
```
