# Viewer reveal and relock of restricted content — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:155aa66bc436e5243a600319294accf5 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/components/RevealControls\.tsx:26; symbol: RevealControls; span SHA256: af47a5d0f814e0002788bd166a6a35cbe7e495208ec4ff0bb72fe2686a99cc24.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 4c275a3142cb19d221f107631a2a5676
    Note over S,U: Source contains call useT at line 27 - order unresolved
    Note over S,U: Source contains call useSession at line 28 - order unresolved
    Note over S,U: Source contains call useState at line 29 - order unresolved
    Note over S,U: Source contains call useState at line 30 - order unresolved
    Note over S,U: Source contains call useState at line 32 - order unresolved
    Note over S,U: Source contains call setBusy at line 61 - order unresolved
    Note over S,U: Source contains call prepareRenderFocusRestore at line 63 - order unresolved
    Note over S,U: Source contains call relock at line 64 - order unresolved
    Note over S,U: Source contains call setBusy at line 66 - order unresolved
    Note over S,U: Source contains call preventDefault at line 35 - order unresolved
    Note over S,U: Source contains call setError at line 36 - order unresolved
    Note over S,U: Source contains call setPin at line 38 - order unresolved
    Note over S,U: Source contains call setBusy at line 40 - order unresolved
    Note over S,U: Source contains call prepareRenderFocusRestore at line 42 - order unresolved
    Note over S,U: Source contains call reveal at line 43 - order unresolved
    Note over S,U: Source contains call setOpen at line 45 - order unresolved
    Note over S,U: Source contains call setError at line 47 - order unresolved
    Note over S,U: Source contains call t at line 47 - order unresolved
    Note over S,U: Source contains call setError at line 50 - order unresolved
    Note over S,U: Source contains call includes at line 51 - order unresolved
    Note over S,U: Source contains call t at line 52 - order unresolved
    Note over S,U: Source contains call t at line 53 - order unresolved
    Note over S,U: Source contains call setBusy at line 56 - order unresolved
```
