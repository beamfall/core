# Enroll a browser with a pairing code — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:f0990662dc3d6a908622cc8299903ec4 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/enroll/EnrollPage\.tsx:40; symbol: EnrollPage; span SHA256: c47f9fbfc591d8600ffb4e5bf8ad928e38cf084157f16d18d570897e045204fa.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 5e8a562af7af514eb8f5ec046fdb0d99
    Note over S,U: Source contains call useT in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 41 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 42 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 43 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 44 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 45 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 46 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 49 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 50 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 53 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 55 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 61 - order unresolved
    Note over S,U: Source contains call fetchSetupStatus in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 63 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 63 - order unresolved
    Note over S,U: Source contains call setSecurityMode in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 64 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/enroll/PairingCodeHelp.tsx at line 8 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/PairingCodeHelp.tsx at line 9 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/PairingCodeHelp.tsx at line 13 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/PairingCodeHelp.tsx at line 17 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/PairingCodeHelp.tsx at line 19 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/PairingCodeHelp.tsx at line 20 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/PairingCodeHelp.tsx at line 23 - order unresolved
    Note over S,U: Source contains call postJSON in internal/web/app/src/lib/graphql/auth.ts at line 101 - order unresolved
    Note over S,U: Source contains call settleRebind in internal/web/app/src/lib/graphql/auth.ts at line 102 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/auth.ts at line 28 - order unresolved
```
