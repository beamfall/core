# Home\-mode admin password sign\-in on the enroll page — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:bddfcc93862f2d798645e97dbb57ea4a -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/enroll/EnrollPage\.tsx:261; symbol: AdminSignInForm; span SHA256: 40ab7553bfdf2f87650f6fa92685559c630579854d7486f125171c6e0991e9bf.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 38a2e26f6c45d30b4c0a342ba6cc236f
    Note over S,U: Source contains call useT in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 262 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 263 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 264 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 266 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 269 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 329 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 330 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 331 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/enroll/EnrollPage.tsx at line 332 - order unresolved
    Note over S,U: Source contains call postJSON in internal/web/app/src/lib/graphql/auth.ts at line 135 - order unresolved
    Note over S,U: Source contains call settleRebind in internal/web/app/src/lib/graphql/auth.ts at line 136 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/auth.ts at line 28 - order unresolved
```
