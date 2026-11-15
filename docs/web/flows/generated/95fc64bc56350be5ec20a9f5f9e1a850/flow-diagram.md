# Viewer area: session pending/error states, setup hand\-off, viewer routes — flow-diagram

Source revision: 314d15477e23e7491b4680a9142d17833b11db50. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:a37b18a6b52aee290eceb8133096f236 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/index\.tsx:75; symbol: ViewerArea; span SHA256: e8c23e5be0c66a622011605c335083f61b506242b50e7f5c4047f2d83ae145ca.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 95fc64bc56350be5ec20a9f5f9e1a850
    Note over S,U: Source contains call useRegister at line 76 - order unresolved
    Note over S,U: Source contains call useSession at line 77 - order unresolved
    Note over S,U: Source contains call useLocation at line 78 - order unresolved
    Note over S,U: Source contains call useScrollRestoration at line 86 - order unresolved
    Note over S,U: Source contains call useEffect at line 88 - order unresolved
    Note over S,U: Source contains call setRegister at line 88 - order unresolved
    Note over S,U: Source contains call useEffect at line 90 - order unresolved
    Note over S,U: Source contains call fetchSetupRequired at line 93 - order unresolved
    Note over S,U: Source contains call then at line 93 - order unresolved
    Note over S,U: Source contains call setSetupRequired at line 94 - order unresolved
    Note over S,U: Source contains call startsWith at line 133 - order unresolved
    Note over S,U: Source contains call profilePickedThisTab at line 134 - order unresolved
    Note over S,U: Source contains call useRemoteReceiver at line 146 - order unresolved
    Note over S,U: Source contains call useEngagementEvents at line 147 - order unresolved
    Note over S,U: Source contains call useT at line 38 - order unresolved
    Note over S,U: Source contains call onRetry at line 43 - order unresolved
    Note over S,U: Source contains call t at line 44 - order unresolved
    Note over S,U: Source contains call useT at line 57 - order unresolved
    Note over S,U: Source contains call t at line 61 - order unresolved
```
