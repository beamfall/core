# Manage rename: pattern preview, batch rename managed paths, undo — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:a7ee900810878723a59ba48b854a0455 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/lib/powertools\.ts:94; symbol: BATCH\_RENAME\_MUTATION; span SHA256: e5dd76e8dbf66d08a67333fab549d2fe731dd892ba3f96313995cd7a069e2b96.

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
    Note over S,U: Source contains call onChange in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 324 - order unresolved
    Note over S,U: Source contains call onChange in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 354 - order unresolved
    Note over S,U: Source contains call Number in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 354 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 51 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 53 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 54 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 57 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 58 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 59 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 61 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 63 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 64 - order unresolved
    Note over S,U: Source contains call fetchLibraries in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 66 - order unresolved
    Note over S,U: Source contains call setLibraries in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 67 - order unresolved
    Note over S,U: Source contains call setLibraryID in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 68 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 70 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 70 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 76 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 76 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 75 - order unresolved
    Note over S,U: Source contains call previewRenames in internal/web/app/src/areas/manage/pages/RenamePage.tsx at line 75 - order unresolved
```
