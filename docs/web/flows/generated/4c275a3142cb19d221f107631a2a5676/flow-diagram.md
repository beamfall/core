# Viewer reveal and relock of restricted content — flow-diagram

Source revision: a75e0f8437987a575a00a20b8d515be33b584f10. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:155aa66bc436e5243a600319294accf5 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/components/RevealControls\.tsx:39; symbol: RevealControls; span SHA256: af47a5d0f814e0002788bd166a6a35cbe7e495208ec4ff0bb72fe2686a99cc24.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 4c275a3142cb19d221f107631a2a5676
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 40 - order unresolved
    Note over S,U: Source contains call useSession in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 41 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 42 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 43 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 45 - order unresolved
    Note over S,U: Source contains call usePending in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 46 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 50 - order unresolved
    Note over S,U: Source contains call carry in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 52 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 53 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 53 - order unresolved
    Note over S,U: Source contains call setOpen in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 54 - order unresolved
    Note over S,U: Source contains call signalOrCommitted in internal/web/app/src/lib/graphql/viewer.ts at line 123 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/viewer.ts at line 124 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/viewer.ts at line 113 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 81 - order unresolved
    Note over S,U: Source contains call prepareRenderFocusRestore in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 83 - order unresolved
    Note over S,U: Source contains call relock in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 84 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 86 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 58 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 59 - order unresolved
    Note over S,U: Source contains call carry in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 60 - order unresolved
    Note over S,U: Source contains call setPin in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 62 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 64 - order unresolved
    Note over S,U: Source contains call prepareRenderFocusRestore in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 66 - order unresolved
    Note over S,U: Source contains call reveal in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 67 - order unresolved
    Note over S,U: Source contains call setOpen in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 69 - order unresolved
    Note over S,U: Source contains call carry in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 71 - order unresolved
    Note over S,U: Source contains call carry in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 74 - order unresolved
    Note over S,U: Source contains call revealErrorKey in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 74 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 76 - order unresolved
```
