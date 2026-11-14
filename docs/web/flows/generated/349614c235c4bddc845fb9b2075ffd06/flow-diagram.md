# Console play history: filters, history table and rankings — flow-diagram

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:1c42c21ee8caeaea6f5f5e8a17af475b -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/PlayHistory\.tsx:36; symbol: ALL; span SHA256: ff90f3c4c9e00f62141a4cbb65a7eb2dc81a58af6fd26e22ce851a90921cf523.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 349614c235c4bddc845fb9b2075ffd06
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 58 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 59 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 60 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 61 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 62 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 63 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 64 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 65 - order unresolved
    Note over S,U: Source contains call fetchPlayHistory in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 67 - order unresolved
    Note over S,U: Source contains call dayStart in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 71 - order unresolved
    Note over S,U: Source contains call dayStart in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 72 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 83 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 89 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 90 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 98 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 104 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 105 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 113 - order unresolved
    Note over S,U: Source contains call setFrom in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 120 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 123 - order unresolved
    Note over S,U: Source contains call setTo in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 130 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 143 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 144 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 145 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 146 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 147 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 151 - order unresolved
    Note over S,U: Source contains call Date in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 154 - order unresolved
    Note over S,U: Source contains call toLocaleString in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 154 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 164 - order unresolved
    Note over S,U: Source contains call watched in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 168 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 171 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 192 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 193 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 193 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 196 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 198 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 206 - order unresolved
    Note over S,U: Source contains call watched in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 206 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 216 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 218 - order unresolved
    Note over S,U: Source contains call fetchPlayStats in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 218 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 219 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 225 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 238 - order unresolved
    Note over S,U: Source contains call setDays in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 244 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 246 - order unresolved
    Note over S,U: Source contains call split in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 52 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 52 - order unresolved
    Note over S,U: Source contains call Date in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 54 - order unresolved
    Note over S,U: Source contains call toISOString in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 54 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 42 - order unresolved
    Note over S,U: Source contains call round in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 42 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 43 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/console/sections/PlayHistory.tsx at line 44 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/lib/graphql/console.ts at line 749 - order unresolved
```
