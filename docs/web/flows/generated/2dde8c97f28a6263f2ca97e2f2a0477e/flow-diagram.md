# Viewer entity detail page and not\-available state — flow-diagram

Source revision: a75e0f8437987a575a00a20b8d515be33b584f10. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:006b61f503bce0588abf5d333fc16f6f -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/components/NotAvailable\.tsx:12; symbol: NotAvailable; span SHA256: bcc1fb11f85246e5aea8aaf1983829233de1717f09c48dc7c7b0bcd21e68c688.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 2dde8c97f28a6263f2ca97e2f2a0477e
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/components/NotAvailable.tsx at line 19 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/components/NotAvailable.tsx at line 22 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/components/NotAvailable.tsx at line 23 - order unresolved
    Note over S,U: Source contains call useParams in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 66 - order unresolved
    Note over S,U: Source contains call useLocation in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 67 - order unresolved
    Note over S,U: Source contains call useSession in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 68 - order unresolved
    Note over S,U: Source contains call useViewerIdentity in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 69 - order unresolved
    Note over S,U: Source contains call useDisplayLocale in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 70 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 71 - order unresolved
    Note over S,U: Source contains call useNavigate in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 72 - order unresolved
    Note over S,U: Source contains call entityCacheKey in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 74 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 76 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 77 - order unresolved
    Note over S,U: Source contains call entityMediaCacheKey in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 79 - order unresolved
    Note over S,U: Source contains call seeAllTargetFor in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 179 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 82 - order unresolved
    Note over S,U: Source contains call playQueueOf in internal/web/app/src/areas/viewer/pages/EntityPage.tsx at line 82 - order unresolved
```
