# Admin Session unlock gate \(admin password\) reused by Console — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:0bcd5c8419cc5762207130812a4e38b3 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/lib/graphql/auth\.ts:26; symbol: settleRebind; span SHA256: b13faca8956ab229de82053f9ca2976e5f0b4a1b28d78439f5d3ec44657139bb.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow ae5c4d0cd7b43bd103cb7fb5eeb41d28
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/auth.ts at line 28 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 78 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 78 - order unresolved
    Note over S,U: Source contains call endAdminSession in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 80 - order unresolved
    Note over S,U: Source contains call setActive in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 84 - order unresolved
    Note over S,U: Source contains call setNotice in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 85 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 89 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 93 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 94 - order unresolved
    Note over S,U: Source contains call verifyAdminSession in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 96 - order unresolved
    Note over S,U: Source contains call setPassword in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 98 - order unresolved
    Note over S,U: Source contains call setActive in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 99 - order unresolved
    Note over S,U: Source contains call setNotice in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 100 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 100 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 102 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 102 - order unresolved
    Note over S,U: Source contains call requestAnimationFrame in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 103 - order unresolved
    Note over S,U: Source contains call focus in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 103 - order unresolved
    Note over S,U: Source contains call setPassword in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 106 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 107 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 107 - order unresolved
    Note over S,U: Source contains call requestAnimationFrame in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 108 - order unresolved
    Note over S,U: Source contains call focus in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 108 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 110 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 64 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 64 - order unresolved
    Note over S,U: Source contains call fetchAdminSession in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 66 - order unresolved
    Note over S,U: Source contains call setActive in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 67 - order unresolved
    Note over S,U: Source contains call setActive in internal/web/app/src/areas/enroll/AdminSessionGate.tsx at line 70 - order unresolved
```
