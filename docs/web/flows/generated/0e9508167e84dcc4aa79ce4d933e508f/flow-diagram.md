# Setup: pick a library type and folder, create the first Library and scan — flow-diagram

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:9c4b31b78600baf2f03fad35648f8643 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/setup/FolderPicker\.tsx:12; symbol: Crumb; span SHA256: 8dc484cd4428026505f71f33237f8927be749ec80598b5c47633346e6be964aa.

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
    Note over S,U: Source contains call constructor in internal/web/app/src/areas/setup/orchestrate.ts at line 86 - order unresolved
    Note over S,U: Source contains call super in internal/web/app/src/areas/setup/orchestrate.ts at line 87 - order unresolved
    Note over S,U: Source contains call libraryTypeSpec in internal/web/app/src/areas/setup/orchestrate.ts at line 140 - order unresolved
    Note over S,U: Source contains call createLibrary in internal/web/app/src/areas/setup/orchestrate.ts at line 145 - order unresolved
    Note over S,U: Source contains call setLibraryRoots in internal/web/app/src/areas/setup/orchestrate.ts at line 148 - order unresolved
    Note over S,U: Source contains call scanLibrary in internal/web/app/src/areas/setup/orchestrate.ts at line 151 - order unresolved
    Note over S,U: Source contains call FirstLibraryError in internal/web/app/src/areas/setup/orchestrate.ts at line 154 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/setup/orchestrate.ts at line 58 - order unresolved
    Note over S,U: Source contains call Error in internal/web/app/src/areas/setup/orchestrate.ts at line 59 - order unresolved
```
