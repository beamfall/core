# Manage operations: jobs, schedules, folder watcher — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:32a97d2cf6162675c626e9cf9dbd73e5 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/lib/schedule\.ts:48; symbol: TriggerFields; span SHA256: ca19feaf51a5b803a084aabbd6aebdde46ffcd58516ea0c62e1b87465a3acd35.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow c0e0b5fbe9dbf1708db4cb766307d1e1
    Note over S,U: Source contains call Date in internal/web/app/src/areas/manage/lib/schedule.ts at line 85 - order unresolved
    Note over S,U: Source contains call isNaN in internal/web/app/src/areas/manage/lib/schedule.ts at line 86 - order unresolved
    Note over S,U: Source contains call getTime in internal/web/app/src/areas/manage/lib/schedule.ts at line 86 - order unresolved
    Note over S,U: Source contains call toLocaleString in internal/web/app/src/areas/manage/lib/schedule.ts at line 87 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/manage/lib/schedule.ts at line 23 - order unresolved
    Note over S,U: Source contains call padStart in internal/web/app/src/areas/manage/lib/schedule.ts at line 23 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 64 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 66 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 67 - order unresolved
    Note over S,U: Source contains call onChange in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 349 - order unresolved
    Note over S,U: Source contains call Number in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 349 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 212 - order unresolved
    Note over S,U: Source contains call triggerFieldsFrom in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 214 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 132 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 134 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 356 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 358 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 149 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 150 - order unresolved
    Note over S,U: Source contains call setScheduleEnabled in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 152 - order unresolved
    Note over S,U: Source contains call setSchedules in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 153 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 154 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 157 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 157 - order unresolved
```
