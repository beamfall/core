# Manage profile page — flow-diagram

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:84364f8020cc8f5596a0e5de1ed3a682 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/ProfilePage\.tsx:13; symbol: ProfilePage; span SHA256: 8352e86aab9b2df2d8cd9db9436977b6fe453d18647faf91fedb9b916b31b79d.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 57894dc67c94738140390052cc0a3567
    Note over S,U: Source contains call useT at line 14 - order unresolved
    Note over S,U: Source contains call useSession at line 15 - order unresolved
    Note over S,U: Source contains call useState at line 16 - order unresolved
    Note over S,U: Source contains call useState at line 17 - order unresolved
    Note over S,U: Source contains call async at line 21 - order unresolved
    Note over S,U: Source contains call setError at line 23 - order unresolved
    Note over S,U: Source contains call setBusyID at line 24 - order unresolved
    Note over S,U: Source contains call switchProfile at line 26 - order unresolved
    Note over S,U: Source contains call setError at line 28 - order unresolved
    Note over S,U: Source contains call t at line 28 - order unresolved
    Note over S,U: Source contains call setBusyID at line 30 - order unresolved
```
