# App routing: /web basename, area code\-splitting, initial\-route preload — flow-diagram

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:ed57e48c7b2c0e4680ad319b8db8255d -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/App\.tsx:84; symbol: App; span SHA256: 85c130fae59b090533bc933b93a59e21e0235c72161a524b22ade1ce4a0b10fe.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 42aaaef56492e1510b51c8caf3e1b3f0
    Note over S,U: Source contains call useLocation in internal/web/app/src/App.tsx at line 80 - order unresolved
    Note over S,U: Source contains call lazyRoute in internal/web/app/src/App.tsx at line 27 - order unresolved
    Note over S,U: Source contains call lazyRoute in internal/web/app/src/App.tsx at line 30 - order unresolved
    Note over S,U: Source contains call lazyRoute in internal/web/app/src/App.tsx at line 24 - order unresolved
    Note over S,U: Source contains call lazyRoute in internal/web/app/src/App.tsx at line 38 - order unresolved
    Note over S,U: Source contains call lazyRoute in internal/web/app/src/App.tsx at line 35 - order unresolved
    Note over S,U: Source contains call lazyRoute in internal/web/app/src/App.tsx at line 21 - order unresolved
    Note over S,U: Source contains call exec in internal/web/app/src/App.tsx at line 47 - order unresolved
    Note over S,U: Source contains call preload in internal/web/app/src/App.tsx at line 60 - order unresolved
    Note over S,U: Source contains call catch in internal/web/app/src/App.tsx at line 60 - order unresolved
    Note over S,U: Source contains call replace in internal/web/app/src/main.tsx at line 17 - order unresolved
    Note over S,U: Source contains call getElementById in internal/web/app/src/main.tsx at line 12 - order unresolved
    Note over S,U: Source contains call slice in internal/web/app/src/main.tsx at line 51 - order unresolved
    Note over S,U: Source contains call createRoot in internal/web/app/src/main.tsx at line 28 - order unresolved
    Note over S,U: Source contains call render in internal/web/app/src/main.tsx at line 28 - order unresolved
    Note over S,U: Source contains call render in internal/web/app/src/main.tsx at line 49 - order unresolved
    Note over S,U: Source contains call load in internal/web/app/src/lib/lazyRoute.ts at line 16 - order unresolved
    Note over S,U: Source contains call then in internal/web/app/src/lib/lazyRoute.ts at line 16 - order unresolved
```
