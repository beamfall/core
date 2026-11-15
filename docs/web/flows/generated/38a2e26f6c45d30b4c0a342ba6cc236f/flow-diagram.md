# Home\-mode admin password sign\-in on the enroll page — flow-diagram

Source revision: 03e521f81ef085ec14c6d7d5d76533692ecba36c. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:bddfcc93862f2d798645e97dbb57ea4a -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/enroll/EnrollPage\.tsx:206; symbol: AdminSignInForm; span SHA256: 40ab7553bfdf2f87650f6fa92685559c630579854d7486f125171c6e0991e9bf.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 38a2e26f6c45d30b4c0a342ba6cc236f
    Note over S,U: Source contains call useT in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 207 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 208 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 209 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 212 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 213 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 219 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 353 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 354 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 355 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 356 - order unresolved
    Note over S,U: Source contains call postJSON in internal/web/app/src/lib/graphql/auth.ts at line 140 - order unresolved
    Note over S,U: Source contains call settleRebind in internal/web/app/src/lib/graphql/auth.ts at line 141 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/auth.ts at line 28 - order unresolved
```
