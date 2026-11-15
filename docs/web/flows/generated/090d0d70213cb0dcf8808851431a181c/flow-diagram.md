# Promote an enrolled browser to a manager device — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:4e9dde93fcd9f338a9dc234b54dddd72 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/enroll/PromotePage\.tsx:31; symbol: PromotePage; span SHA256: f5a6481e3b322f72550819d0b73f61a52ac2e6cc5a95d9eaf4f34dba04c53f31.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 090d0d70213cb0dcf8808851431a181c
    Note over S,U: Source contains call useT in internal/web/app/src/areas/enroll/PromotePage.tsx at line 32 - order unresolved
    Note over S,U: Source contains call useNavigate in internal/web/app/src/areas/enroll/PromotePage.tsx at line 33 - order unresolved
    Note over S,U: Source contains call useSession in internal/web/app/src/areas/enroll/PromotePage.tsx at line 34 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/PromotePage.tsx at line 35 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/PromotePage.tsx at line 36 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/PromotePage.tsx at line 37 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/PromotePage.tsx at line 40 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/PromotePage.tsx at line 42 - order unresolved
    Note over S,U: Source contains call postJSON in internal/web/app/src/lib/graphql/auth.ts at line 112 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/auth.ts at line 113 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/enroll/PromotePage.tsx at line 45 - order unresolved
    Note over S,U: Source contains call showError in internal/web/app/src/areas/enroll/PromotePage.tsx at line 48 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/PromotePage.tsx at line 48 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/enroll/PromotePage.tsx at line 51 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/enroll/PromotePage.tsx at line 52 - order unresolved
    Note over S,U: Source contains call promote in internal/web/app/src/areas/enroll/PromotePage.tsx at line 54 - order unresolved
    Note over S,U: Source contains call navigate in internal/web/app/src/areas/enroll/PromotePage.tsx at line 55 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/enroll/PromotePage.tsx at line 57 - order unresolved
    Note over S,U: Source contains call showError in internal/web/app/src/areas/enroll/PromotePage.tsx at line 58 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/PromotePage.tsx at line 60 - order unresolved
    Note over S,U: Source contains call pairingCodeErrorMessage in internal/web/app/src/areas/enroll/PromotePage.tsx at line 61 - order unresolved
```
