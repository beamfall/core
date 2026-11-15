# Manage field locks — flow-diagram

Source revision: a75e0f8437987a575a00a20b8d515be33b584f10. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:e35f7b44d54280c504b07d7dc8194888 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/FieldLocksPage\.tsx:32; symbol: FieldLocksPage; span SHA256: 629fe5a8e1682004ada517a9e50ed36652f52d34446b7e26053fc1b83ad6cb3b.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 0d4e8f99d8c0f2f17e34c9a664bcb1cf
    Note over S,U: Source contains call useT at line 33 - order unresolved
    Note over S,U: Source contains call useState at line 34 - order unresolved
    Note over S,U: Source contains call useState at line 36 - order unresolved
    Note over S,U: Source contains call t at line 23 - order unresolved
    Note over S,U: Source contains call async at line 38 - order unresolved
    Note over S,U: Source contains call setError at line 39 - order unresolved
    Note over S,U: Source contains call trim at line 40 - order unresolved
    Note over S,U: Source contains call setLocks at line 43 - order unresolved
    Note over S,U: Source contains call fetchMediaFieldLocks at line 43 - order unresolved
    Note over S,U: Source contains call setError at line 45 - order unresolved
    Note over S,U: Source contains call t at line 45 - order unresolved
    Note over S,U: Source contains call async at line 49 - order unresolved
    Note over S,U: Source contains call setError at line 50 - order unresolved
    Note over S,U: Source contains call trim at line 51 - order unresolved
    Note over S,U: Source contains call setLocks at line 53 - order unresolved
    Note over S,U: Source contains call setMediaFieldLock at line 53 - order unresolved
    Note over S,U: Source contains call setError at line 55 - order unresolved
    Note over S,U: Source contains call t at line 55 - order unresolved
```
