# Manage bulk edit and organize with preview and confirm — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:cd6b0423d52695913011c6c64a67c4af -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/BulkOpsPage\.tsx:106; symbol: BulkEditSection; span SHA256: 2fdd3a354bb9959f851d60892c33a2f153d82e32ddbe401be5706fc20f31eb93.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6dfceb70ed5574fc0dd74165b30a0088
    Note over S,U: Source contains call useT at line 113 - order unresolved
    Note over S,U: Source contains call useState at line 114 - order unresolved
    Note over S,U: Source contains call useState at line 115 - order unresolved
    Note over S,U: Source contains call useState at line 116 - order unresolved
    Note over S,U: Source contains call useState at line 120 - order unresolved
    Note over S,U: Source contains call useT at line 59 - order unresolved
    Note over S,U: Source contains call useState at line 60 - order unresolved
    Note over S,U: Source contains call useState at line 62 - order unresolved
    Note over S,U: Source contains call useT at line 382 - order unresolved
    Note over S,U: Source contains call t at line 394 - order unresolved
    Note over S,U: Source contains call t at line 398 - order unresolved
    Note over S,U: Source contains call useT at line 244 - order unresolved
    Note over S,U: Source contains call useState at line 246 - order unresolved
    Note over S,U: Source contains call useState at line 247 - order unresolved
    Note over S,U: Source contains call useState at line 249 - order unresolved
    Note over S,U: Source contains call useEffect at line 251 - order unresolved
    Note over S,U: Source contains call async at line 253 - order unresolved
    Note over S,U: Source contains call fetchLibraries at line 255 - order unresolved
    Note over S,U: Source contains call setLibraries at line 257 - order unresolved
    Note over S,U: Source contains call setLibraryID at line 258 - order unresolved
    Note over S,U: Source contains call map at line 48 - order unresolved
    Note over S,U: Source contains call join at line 48 - order unresolved
    Note over S,U: Source contains call trim at line 129 - order unresolved
    Note over S,U: Source contains call setBatch at line 65 - order unresolved
    Note over S,U: Source contains call setUndone at line 66 - order unresolved
    Note over S,U: Source contains call async at line 69 - order unresolved
    Note over S,U: Source contains call setError at line 71 - order unresolved
    Note over S,U: Source contains call undoBatch at line 73 - order unresolved
    Note over S,U: Source contains call setBatch at line 74 - order unresolved
    Note over S,U: Source contains call setUndone at line 75 - order unresolved
    Note over S,U: Source contains call setError at line 77 - order unresolved
    Note over S,U: Source contains call t at line 77 - order unresolved
```
