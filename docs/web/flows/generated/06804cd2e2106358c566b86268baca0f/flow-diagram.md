# Manage restrictions — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:81591007b037041f06a2388546b4035c -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/RestrictionsPage\.tsx:15; symbol: RestrictionsPage; span SHA256: 417dbc6d2a1bbd8599adf1cdd5447cc8a1ddd58b2d00abca38ee259921f56a2a.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 06804cd2e2106358c566b86268baca0f
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 16 - order unresolved
    Note over S,U: Source contains call useSession in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 17 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 18 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 19 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 20 - order unresolved
    Note over S,U: Source contains call signalOrCommitted in internal/web/app/src/lib/graphql/viewer.ts at line 118 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/viewer.ts at line 119 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/viewer.ts at line 108 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 43 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 44 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 45 - order unresolved
    Note over S,U: Source contains call relock in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 47 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 49 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 22 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 23 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 24 - order unresolved
    Note over S,U: Source contains call setPin in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 26 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 28 - order unresolved
    Note over S,U: Source contains call reveal in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 30 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 31 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 31 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 33 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 35 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 36 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/RestrictionsPage.tsx at line 39 - order unresolved
```
