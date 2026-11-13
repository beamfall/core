# Manage bulk edit and organize with preview and confirm — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:cd6b0423d52695913011c6c64a67c4af -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/BulkOpsPage\.tsx:107; symbol: BulkEditSection; span SHA256: 2fdd3a354bb9959f851d60892c33a2f153d82e32ddbe401be5706fc20f31eb93.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6dfceb70ed5574fc0dd74165b30a0088
    Note over S,U: Source contains call useT at line 114 - order unresolved
    Note over S,U: Source contains call useState at line 115 - order unresolved
    Note over S,U: Source contains call useState at line 116 - order unresolved
    Note over S,U: Source contains call useState at line 117 - order unresolved
    Note over S,U: Source contains call useState at line 121 - order unresolved
    Note over S,U: Source contains call useT at line 60 - order unresolved
    Note over S,U: Source contains call useState at line 61 - order unresolved
    Note over S,U: Source contains call useState at line 63 - order unresolved
    Note over S,U: Source contains call map at line 49 - order unresolved
    Note over S,U: Source contains call join at line 49 - order unresolved
    Note over S,U: Source contains call trim at line 130 - order unresolved
    Note over S,U: Source contains call setBatch at line 66 - order unresolved
    Note over S,U: Source contains call setUndone at line 67 - order unresolved
    Note over S,U: Source contains call async at line 70 - order unresolved
    Note over S,U: Source contains call setError at line 72 - order unresolved
    Note over S,U: Source contains call undoBatch at line 74 - order unresolved
    Note over S,U: Source contains call setBatch at line 75 - order unresolved
    Note over S,U: Source contains call setUndone at line 76 - order unresolved
    Note over S,U: Source contains call setError at line 78 - order unresolved
    Note over S,U: Source contains call t at line 78 - order unresolved
```
