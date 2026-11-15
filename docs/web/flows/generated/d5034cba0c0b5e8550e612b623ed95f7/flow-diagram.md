# Console overview: owner tasks, setup checklist, needs\-attention queue — flow-diagram

Source revision: a75e0f8437987a575a00a20b8d515be33b584f10. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:9bb6966184c644d1449ead9efd2b1b95 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/OwnerTasks\.tsx:64; symbol: OwnerTasks; span SHA256: 0ad919b5f193d62405ffe6f8ed8bdf75a839aba68215e40ceedbd74362b07101.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow d5034cba0c0b5e8550e612b623ed95f7
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/OwnerTasks.tsx at line 65 - order unresolved
    Note over S,U: Source contains call useAnyRescanActive in internal/web/app/src/areas/console/OwnerTasks.tsx at line 66 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/OwnerTasks.tsx at line 67 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/OwnerTasks.tsx at line 68 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/OwnerTasks.tsx at line 69 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/OwnerTasks.tsx at line 70 - order unresolved
    Note over S,U: Source contains call every in internal/web/app/src/areas/console/OwnerTasks.tsx at line 73 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/OwnerTasks.tsx at line 77 - order unresolved
    Note over S,U: Source contains call flatMap in internal/web/app/src/areas/console/OwnerTasks.tsx at line 78 - order unresolved
    Note over S,U: Source contains call groupProviderKeys in internal/web/app/src/areas/console/OwnerTasks.tsx at line 78 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/OwnerTasks.tsx at line 79 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 88 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 89 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 93 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 97 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 107 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 108 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 113 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 114 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 118 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 126 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 127 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 132 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 133 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 141 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 142 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 147 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 148 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/OwnerTasks.tsx at line 149 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/index.tsx at line 120 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/index.tsx at line 121 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/index.tsx at line 126 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/index.tsx at line 127 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/index.tsx at line 128 - order unresolved
    Note over S,U: Source contains call firstValueOpen in internal/web/app/src/areas/console/index.tsx at line 135 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/index.tsx at line 145 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 160 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 162 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 169 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 174 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 185 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 192 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 194 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 197 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 199 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 200 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 150 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 151 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 152 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 155 - order unresolved
    Note over S,U: Source contains call migrationResumeJob in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 156 - order unresolved
    Note over S,U: Source contains call firstValueProgress in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 158 - order unresolved
    Note over S,U: Source contains call fetchSourceItems in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 92 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 134 - order unresolved
    Note over S,U: Source contains call getItem in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 135 - order unresolved
    Note over S,U: Source contains call split in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 136 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 137 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 140 - order unresolved
    Note over S,U: Source contains call has in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 144 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 165 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 166 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 167 - order unresolved
```
