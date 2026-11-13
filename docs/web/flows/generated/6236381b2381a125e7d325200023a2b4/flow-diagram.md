# Manage area shell: route table, nav groups, admin lock — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:6d8542ad14b6a67b4e935b8d909e9802 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/index\.tsx:142; symbol: settle; span SHA256: 805d130ed5a0b8c016f4019d490ffa176df97596e5124bc0586275a0ce594356.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6236381b2381a125e7d325200023a2b4
    Note over S,U: Source contains call useCallback at line 142 - order unresolved
    Note over S,U: Source contains call splice at line 143 - order unresolved
    Note over S,U: Source contains call resolve at line 143 - order unresolved
```
