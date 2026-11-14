# Console access: create and manage Profiles — flow-diagram

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:082d93d1fb01651ea0d2deaff818dcc3 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/AccessAdmin\.tsx:6; symbol: AccessAdmin; span SHA256: c16358ed09362a9e222e69940c34cbc673fdce2e160503341857566a3346f415.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 1fb8c6a5ce70995ee6d0c1ffc25b0495
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/Profiles.tsx at line 65 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Profiles.tsx at line 66 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Profiles.tsx at line 67 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Profiles.tsx at line 68 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Profiles.tsx at line 69 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/Profiles.tsx at line 134 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Profiles.tsx at line 135 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Profiles.tsx at line 136 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Profiles.tsx at line 137 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/Profiles.tsx at line 138 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/Profiles.tsx at line 34 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/Profiles.tsx at line 48 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/Profiles.tsx at line 54 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/Profiles.tsx at line 55 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/lib/graphql/console.ts at line 749 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/Profiles.tsx at line 141 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/console/sections/Profiles.tsx at line 143 - order unresolved
    Note over S,U: Source contains call updateProfile in internal/web/app/src/areas/console/sections/Profiles.tsx at line 145 - order unresolved
    Note over S,U: Source contains call onDisabled in internal/web/app/src/areas/console/sections/Profiles.tsx at line 146 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/console/sections/Profiles.tsx at line 148 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/console/sections/Profiles.tsx at line 72 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/Profiles.tsx at line 73 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/console/sections/Profiles.tsx at line 75 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/console/sections/Profiles.tsx at line 76 - order unresolved
    Note over S,U: Source contains call createProfile in internal/web/app/src/areas/console/sections/Profiles.tsx at line 78 - order unresolved
    Note over S,U: Source contains call setName in internal/web/app/src/areas/console/sections/Profiles.tsx at line 79 - order unresolved
    Note over S,U: Source contains call setKind in internal/web/app/src/areas/console/sections/Profiles.tsx at line 80 - order unresolved
    Note over S,U: Source contains call onCreated in internal/web/app/src/areas/console/sections/Profiles.tsx at line 81 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/console/sections/Profiles.tsx at line 83 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/console/sections/Profiles.tsx at line 85 - order unresolved
```
