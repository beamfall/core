# Setup: pick a library type and folder, create the first Library and scan — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:9c4b31b78600baf2f03fad35648f8643 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/setup/FolderPicker\.tsx:41; symbol: FolderPicker; span SHA256: 56637f07f224a16796fbe78c7c3253cd5f8eb8f4f2563bf44e469fdd9b78fe4f.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 0e9508167e84dcc4aa79ce4d933e508f
    Note over S,U: Source contains call breadcrumbs in internal/web/app/src/areas/setup/FolderPicker.tsx at line 50 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/setup/FolderPicker.tsx at line 51 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/setup/FolderPicker.tsx at line 57 - order unresolved
    Note over S,U: Source contains call onOpenFolder in internal/web/app/src/areas/setup/FolderPicker.tsx at line 68 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/setup/FolderPicker.tsx at line 93 - order unresolved
    Note over S,U: Source contains call onOpenFolder in internal/web/app/src/areas/setup/FolderPicker.tsx at line 97 - order unresolved
    Note over S,U: Source contains call split in internal/web/app/src/areas/setup/FolderPicker.tsx at line 22 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/setup/FolderPicker.tsx at line 22 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/setup/FolderPicker.tsx at line 26 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/setup/orchestrate.ts at line 58 - order unresolved
    Note over S,U: Source contains call Error in internal/web/app/src/areas/setup/orchestrate.ts at line 59 - order unresolved
```
