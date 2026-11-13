# Sign out of the web session — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:0089f25b280d0f82c7687e665a4c36ba -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/enroll/SignOutButton\.tsx:13; symbol: SignOutButton; span SHA256: 8be48e3c955d261c9c7609846e6775ee2ce7194e40ef2fd2a5c3e900e3486eba.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 7086235a1aed16e99089f12c4833bcc2
    Note over S,U: Source contains call useT in internal/web/app/src/areas/enroll/SignOutButton.tsx at line 18 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/enroll/SignOutButton.tsx at line 19 - order unresolved
    Note over S,U: Source contains call postJSON in internal/web/app/src/lib/graphql/auth.ts at line 158 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/enroll/SignOutButton.tsx at line 23 - order unresolved
    Note over S,U: Source contains call logout in internal/web/app/src/areas/enroll/SignOutButton.tsx at line 25 - order unresolved
    Note over S,U: Source contains call assign in internal/web/app/src/areas/enroll/SignOutButton.tsx at line 29 - order unresolved
```
