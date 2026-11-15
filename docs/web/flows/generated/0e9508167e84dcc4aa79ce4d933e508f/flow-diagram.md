# Setup: pick a library type and folder, create the first Library and scan — flow-diagram

Source revision: b952880cf4519beaa9738e25e349aabf600c9a41. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:9c4b31b78600baf2f03fad35648f8643 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/setup/FolderPicker\.tsx:19; symbol: Crumb; span SHA256: 8dc484cd4428026505f71f33237f8927be749ec80598b5c47633346e6be964aa.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 0e9508167e84dcc4aa79ce4d933e508f
    Note over S,U: Source contains call breadcrumbs in internal/web/app/src/areas/setup/FolderPicker.tsx at line 57 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/setup/FolderPicker.tsx at line 58 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/setup/FolderPicker.tsx at line 62 - order unresolved
    Note over S,U: Source contains call focus in internal/web/app/src/areas/setup/FolderPicker.tsx at line 65 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/setup/FolderPicker.tsx at line 73 - order unresolved
    Note over S,U: Source contains call stopPropagation in internal/web/app/src/areas/setup/FolderPicker.tsx at line 74 - order unresolved
    Note over S,U: Source contains call onClose in internal/web/app/src/areas/setup/FolderPicker.tsx at line 75 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/setup/FolderPicker.tsx at line 81 - order unresolved
    Note over S,U: Source contains call onOpenFolder in internal/web/app/src/areas/setup/FolderPicker.tsx at line 96 - order unresolved
    Note over S,U: Source contains call cn in internal/web/app/src/areas/setup/FolderPicker.tsx at line 124 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/setup/FolderPicker.tsx at line 132 - order unresolved
    Note over S,U: Source contains call onOpenFolder in internal/web/app/src/areas/setup/FolderPicker.tsx at line 138 - order unresolved
    Note over S,U: Source contains call split in internal/web/app/src/areas/setup/FolderPicker.tsx at line 29 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/setup/FolderPicker.tsx at line 29 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/setup/FolderPicker.tsx at line 33 - order unresolved
    Note over S,U: Source contains call constructor in internal/web/app/src/areas/setup/orchestrate.ts at line 68 - order unresolved
    Note over S,U: Source contains call super in internal/web/app/src/areas/setup/orchestrate.ts at line 69 - order unresolved
    Note over S,U: Source contains call createLibrary in internal/web/app/src/areas/setup/orchestrate.ts at line 137 - order unresolved
    Note over S,U: Source contains call setLibraryRoots in internal/web/app/src/areas/setup/orchestrate.ts at line 143 - order unresolved
    Note over S,U: Source contains call scanLibrary in internal/web/app/src/areas/setup/orchestrate.ts at line 147 - order unresolved
    Note over S,U: Source contains call FirstLibraryError in internal/web/app/src/areas/setup/orchestrate.ts at line 150 - order unresolved
```
