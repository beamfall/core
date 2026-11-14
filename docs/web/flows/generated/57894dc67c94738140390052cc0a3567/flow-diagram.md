# Manage profile page — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:84364f8020cc8f5596a0e5de1ed3a682 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/ProfilePage\.tsx:12; symbol: ProfilePage; span SHA256: 8352e86aab9b2df2d8cd9db9436977b6fe453d18647faf91fedb9b916b31b79d.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 57894dc67c94738140390052cc0a3567
    Note over S,U: Source contains call useT at line 13 - order unresolved
    Note over S,U: Source contains call useSession at line 14 - order unresolved
    Note over S,U: Source contains call useState at line 15 - order unresolved
    Note over S,U: Source contains call useState at line 16 - order unresolved
    Note over S,U: Source contains call async at line 20 - order unresolved
    Note over S,U: Source contains call setError at line 22 - order unresolved
    Note over S,U: Source contains call setBusyID at line 23 - order unresolved
    Note over S,U: Source contains call switchProfile at line 25 - order unresolved
    Note over S,U: Source contains call setError at line 27 - order unresolved
    Note over S,U: Source contains call t at line 27 - order unresolved
    Note over S,U: Source contains call setBusyID at line 29 - order unresolved
```
