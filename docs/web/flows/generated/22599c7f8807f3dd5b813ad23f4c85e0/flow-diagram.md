# Manage rename: pattern preview, batch rename managed paths, undo — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:a7ee900810878723a59ba48b854a0455 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/lib/renamePattern\.ts:63; symbol: applyToStem; span SHA256: c1c4c153726740130c39d9ae2e15b73808657724c910cb51655d761791d61077.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 22599c7f8807f3dd5b813ad23f4c85e0
    Note over S,U: Source contains call split in internal/web/app/src/areas/manage/lib/renamePattern.ts at line 66 - order unresolved
    Note over S,U: Source contains call join in internal/web/app/src/areas/manage/lib/renamePattern.ts at line 66 - order unresolved
    Note over S,U: Source contains call padNumber in internal/web/app/src/areas/manage/lib/renamePattern.ts at line 69 - order unresolved
    Note over S,U: Source contains call includes in internal/web/app/src/areas/manage/lib/renamePattern.ts at line 72 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/manage/lib/renamePattern.ts at line 53 - order unresolved
    Note over S,U: Source contains call padStart in internal/web/app/src/areas/manage/lib/renamePattern.ts at line 54 - order unresolved
    Note over S,U: Source contains call lastIndexOf in internal/web/app/src/areas/manage/lib/renamePattern.ts at line 47 - order unresolved
    Note over S,U: Source contains call slice in internal/web/app/src/areas/manage/lib/renamePattern.ts at line 49 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 52 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 54 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 55 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 58 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 59 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 60 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 62 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 64 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 65 - order unresolved
    Note over S,U: Source contains call fetchLibraries in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 67 - order unresolved
    Note over S,U: Source contains call setLibraries in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 68 - order unresolved
    Note over S,U: Source contains call setLibraryID in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 69 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 71 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 71 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 77 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 77 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 76 - order unresolved
    Note over S,U: Source contains call previewRenames in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 76 - order unresolved
```
