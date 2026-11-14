# Console overview: owner tasks, setup checklist, needs\-attention queue — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

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
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/index.tsx at line 108 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/index.tsx at line 109 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/index.tsx at line 121 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 135 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 137 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 144 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 149 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 160 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 162 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 163 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 101 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 102 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 103 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 106 - order unresolved
    Note over S,U: Source contains call migrationResumeJob in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 107 - order unresolved
    Note over S,U: Source contains call fetchSourceItems in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 66 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 85 - order unresolved
    Note over S,U: Source contains call getItem in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 86 - order unresolved
    Note over S,U: Source contains call split in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 87 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 88 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 91 - order unresolved
    Note over S,U: Source contains call has in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 95 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 113 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 114 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/SetupChecklist.tsx at line 115 - order unresolved
```
