# Manage restrictions — flow-diagram

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:81591007b037041f06a2388546b4035c -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/RestrictionsPage\.tsx:16; symbol: RestrictionsPage; span SHA256: 417dbc6d2a1bbd8599adf1cdd5447cc8a1ddd58b2d00abca38ee259921f56a2a.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 06804cd2e2106358c566b86268baca0f
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 17 - order unresolved
    Note over S,U: Source contains call useSession in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 18 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 19 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 20 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 21 - order unresolved
    Note over S,U: Source contains call signalOrCommitted in internal/web/app/src/lib/graphql/viewer.ts at line 117 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/viewer.ts at line 118 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/viewer.ts at line 107 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 44 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 45 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 46 - order unresolved
    Note over S,U: Source contains call relock in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 48 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 50 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 23 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 24 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 25 - order unresolved
    Note over S,U: Source contains call setPin in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 27 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 29 - order unresolved
    Note over S,U: Source contains call reveal in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 31 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 32 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 32 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 34 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 36 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 37 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 40 - order unresolved
```
