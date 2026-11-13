# Manage operations: jobs, schedules, folder watcher — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:32a97d2cf6162675c626e9cf9dbd73e5 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/lib/schedule\.ts:84; symbol: formatDateTime; span SHA256: 4c4f22aad3de466b51cd4fb27a8e47bb30b49fd73e9f5433d3cc6725e95cc64a.

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
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 65 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 67 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 68 - order unresolved
    Note over S,U: Source contains call onChange in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 352 - order unresolved
    Note over S,U: Source contains call Number in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 352 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 215 - order unresolved
    Note over S,U: Source contains call triggerFieldsFrom in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 217 - order unresolved
    Note over S,U: Source contains call jobKindLabel in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 227 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 229 - order unresolved
    Note over S,U: Source contains call onToggle in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 234 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 236 - order unresolved
    Note over S,U: Source contains call renderTrigger in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 241 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 241 - order unresolved
    Note over S,U: Source contains call formatDateTime in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 241 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 242 - order unresolved
    Note over S,U: Source contains call formatDateTime in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 242 - order unresolved
    Note over S,U: Source contains call setKind in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 246 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 247 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 251 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 252 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 253 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 260 - order unresolved
    Note over S,U: Source contains call setField in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 264 - order unresolved
    Note over S,U: Source contains call setField in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 268 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 270 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 274 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 275 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 283 - order unresolved
    Note over S,U: Source contains call setField in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 284 - order unresolved
    Note over S,U: Source contains call Number in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 284 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 286 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 290 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 291 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 292 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 302 - order unresolved
    Note over S,U: Source contains call setField in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 306 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 309 - order unresolved
    Note over S,U: Source contains call setField in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 313 - order unresolved
    Note over S,U: Source contains call onSaveTrigger in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 322 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 324 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 134 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 136 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 359 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 361 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 151 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 152 - order unresolved
    Note over S,U: Source contains call setScheduleEnabled in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 154 - order unresolved
    Note over S,U: Source contains call setSchedules in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 155 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 156 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 159 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/OperationsPage.tsx at line 159 - order unresolved
```
