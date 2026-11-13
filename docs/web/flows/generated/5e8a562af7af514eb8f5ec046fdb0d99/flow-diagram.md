# Enroll a browser with a pairing code — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:f0990662dc3d6a908622cc8299903ec4 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/enroll/EnrollPage\.tsx:181; symbol: CardHeading; span SHA256: f7e73c414493680f27a87f3e71e6d41ea30bb012047e4a11e1bdbb2d33579ffc.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 5e8a562af7af514eb8f5ec046fdb0d99
    Note over S,U: Source contains call cn in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 198 - order unresolved
    Note over S,U: Source contains call cn in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 208 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 44 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 45 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 46 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 47 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 48 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 49 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 51 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 52 - order unresolved
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
    Note over S,U: Source contains call postJSON in internal/web/app/src/lib/graphql/auth.ts at line 96 - order unresolved
    Note over S,U: Source contains call settleRebind in internal/web/app/src/lib/graphql/auth.ts at line 97 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/auth.ts at line 28 - order unresolved
```
