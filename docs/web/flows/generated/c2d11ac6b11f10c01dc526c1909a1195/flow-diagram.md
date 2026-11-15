# Console activity log — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:6e81a010e4a044e5daa9f13522f02f91 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/ActivityLog\.tsx:307; symbol: ActivityEntryRows; span SHA256: 676a3a4b813614bd0d0b1d72debd68b18a0bbd07da249db98dad8119376437c5.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow c2d11ac6b11f10c01dc526c1909a1195
    Note over S,U: Source contains call parseActivityTime in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 324 - order unresolved
    Note over S,U: Source contains call formatUtcTime in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 325 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 326 - order unresolved
    Note over S,U: Source contains call join in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 326 - order unresolved
    Note over S,U: Source contains call formatRelativeTime in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 336 - order unresolved
    Note over S,U: Source contains call formatLocalTime in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 338 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 370 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 371 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 373 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 374 - order unresolved
    Note over S,U: Source contains call join in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 374 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 377 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 381 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 383 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 387 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 388 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 147 - order unresolved
    Note over S,U: Source contains call useDisplayLocale in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 148 - order unresolved
    Note over S,U: Source contains call useMinuteClock in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 149 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 150 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 152 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 154 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 156 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 158 - order unresolved
    Note over S,U: Source contains call describeActivity in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 160 - order unresolved
    Note over S,U: Source contains call all in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 89 - order unresolved
    Note over S,U: Source contains call fetchActivityRecords in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 90 - order unresolved
    Note over S,U: Source contains call namesBy in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 91 - order unresolved
    Note over S,U: Source contains call fetchProfilesAdmin in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 92 - order unresolved
    Note over S,U: Source contains call namesBy in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 96 - order unresolved
    Note over S,U: Source contains call fetchDevices in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 97 - order unresolved
    Note over S,U: Source contains call namesBy in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 101 - order unresolved
    Note over S,U: Source contains call fetchLibraries in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 102 - order unresolved
    Note over S,U: Source contains call mediaTitles in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 107 - order unresolved
    Note over S,U: Source contains call Map in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 107 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 135 - order unresolved
    Note over S,U: Source contains call Date in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 135 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 136 - order unresolved
    Note over S,U: Source contains call setInterval in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 137 - order unresolved
    Note over S,U: Source contains call setNow in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 137 - order unresolved
    Note over S,U: Source contains call Date in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 137 - order unresolved
    Note over S,U: Source contains call clearInterval in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 138 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/lib/graphql/console.ts at line 821 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 164 - order unresolved
    Note over S,U: Source contains call set in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 167 - order unresolved
    Note over S,U: Source contains call get in internal/web/app/src/areas/console/sections/ActivityLog.tsx at line 167 - order unresolved
```
