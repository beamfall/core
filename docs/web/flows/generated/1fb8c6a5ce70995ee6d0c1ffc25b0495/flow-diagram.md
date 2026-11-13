# Console access: create and manage Profiles — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:082d93d1fb01651ea0d2deaff818dcc3 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/AccessAdmin\.tsx:6; symbol: AccessAdmin; span SHA256: c16358ed09362a9e222e69940c34cbc673fdce2e160503341857566a3346f415.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 1fb8c6a5ce70995ee6d0c1ffc25b0495
    Note over S,U: Source contains call useAsync at line 34 - order unresolved
    Note over S,U: Source contains call map at line 54 - order unresolved
    Note over S,U: Source contains call refresh at line 55 - order unresolved
    Note over S,U: Source contains call useT at line 65 - order unresolved
    Note over S,U: Source contains call useState at line 66 - order unresolved
    Note over S,U: Source contains call useState at line 67 - order unresolved
    Note over S,U: Source contains call useState at line 68 - order unresolved
    Note over S,U: Source contains call useState at line 69 - order unresolved
    Note over S,U: Source contains call trim at line 141 - order unresolved
    Note over S,U: Source contains call setBusy at line 143 - order unresolved
    Note over S,U: Source contains call updateProfile at line 145 - order unresolved
    Note over S,U: Source contains call onDisabled at line 146 - order unresolved
    Note over S,U: Source contains call setBusy at line 148 - order unresolved
    Note over S,U: Source contains call preventDefault at line 72 - order unresolved
    Note over S,U: Source contains call trim at line 73 - order unresolved
    Note over S,U: Source contains call setBusy at line 75 - order unresolved
    Note over S,U: Source contains call setError at line 76 - order unresolved
    Note over S,U: Source contains call createProfile at line 78 - order unresolved
    Note over S,U: Source contains call setName at line 79 - order unresolved
    Note over S,U: Source contains call setKind at line 80 - order unresolved
    Note over S,U: Source contains call onCreated at line 81 - order unresolved
    Note over S,U: Source contains call setError at line 83 - order unresolved
    Note over S,U: Source contains call setBusy at line 85 - order unresolved
```
