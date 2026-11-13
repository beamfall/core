# Viewer reveal and relock of restricted content — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:155aa66bc436e5243a600319294accf5 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/components/RevealControls\.tsx:26; symbol: RevealControls; span SHA256: af47a5d0f814e0002788bd166a6a35cbe7e495208ec4ff0bb72fe2686a99cc24.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 4c275a3142cb19d221f107631a2a5676
    Note over S,U: Source contains call useT in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 27 - order unresolved
    Note over S,U: Source contains call useSession in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 28 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 29 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 30 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 32 - order unresolved
    Note over S,U: Source contains call signalOrCommitted in internal/web/app/src/lib/graphql/viewer.ts at line 117 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/viewer.ts at line 118 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/viewer.ts at line 107 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 61 - order unresolved
    Note over S,U: Source contains call prepareRenderFocusRestore in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 63 - order unresolved
    Note over S,U: Source contains call relock in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 64 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 66 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 35 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 36 - order unresolved
    Note over S,U: Source contains call setPin in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 38 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 40 - order unresolved
    Note over S,U: Source contains call prepareRenderFocusRestore in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 42 - order unresolved
    Note over S,U: Source contains call reveal in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 43 - order unresolved
    Note over S,U: Source contains call setOpen in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 45 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 47 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 47 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 50 - order unresolved
    Note over S,U: Source contains call includes in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 51 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 52 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 53 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/viewer/components/RevealControls.tsx at line 56 - order unresolved
```
