# Manage field locks — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:e35f7b44d54280c504b07d7dc8194888 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/FieldLocksPage\.tsx:33; symbol: FieldLocksPage; span SHA256: 629fe5a8e1682004ada517a9e50ed36652f52d34446b7e26053fc1b83ad6cb3b.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 0d4e8f99d8c0f2f17e34c9a664bcb1cf
    Note over S,U: Source contains call useT at line 34 - order unresolved
    Note over S,U: Source contains call useState at line 35 - order unresolved
    Note over S,U: Source contains call useState at line 37 - order unresolved
    Note over S,U: Source contains call t at line 24 - order unresolved
    Note over S,U: Source contains call async at line 39 - order unresolved
    Note over S,U: Source contains call setError at line 40 - order unresolved
    Note over S,U: Source contains call trim at line 41 - order unresolved
    Note over S,U: Source contains call setLocks at line 44 - order unresolved
    Note over S,U: Source contains call fetchMediaFieldLocks at line 44 - order unresolved
    Note over S,U: Source contains call setError at line 46 - order unresolved
    Note over S,U: Source contains call t at line 46 - order unresolved
    Note over S,U: Source contains call async at line 50 - order unresolved
    Note over S,U: Source contains call setError at line 51 - order unresolved
    Note over S,U: Source contains call trim at line 52 - order unresolved
    Note over S,U: Source contains call setMediaFieldLock at line 54 - order unresolved
    Note over S,U: Source contains call setLocks at line 55 - order unresolved
    Note over S,U: Source contains call map at line 55 - order unresolved
    Note over S,U: Source contains call setError at line 57 - order unresolved
    Note over S,U: Source contains call t at line 57 - order unresolved
```
