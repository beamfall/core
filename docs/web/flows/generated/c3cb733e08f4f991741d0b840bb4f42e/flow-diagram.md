# Manage entities — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:521fb7e985d78cfda5d23ee318b3f641 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/EntityPage\.tsx:24; symbol: EntityPage; span SHA256: 397517278d0181aa246f1fee6658b0eb41f983aa5dc7b254ae00d382ea6d7ad6.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow c3cb733e08f4f991741d0b840bb4f42e
    Note over S,U: Source contains call useT at line 25 - order unresolved
    Note over S,U: Source contains call useState at line 26 - order unresolved
    Note over S,U: Source contains call useState at line 28 - order unresolved
    Note over S,U: Source contains call useState at line 29 - order unresolved
    Note over S,U: Source contains call async at line 31 - order unresolved
    Note over S,U: Source contains call setError at line 32 - order unresolved
    Note over S,U: Source contains call trim at line 33 - order unresolved
    Note over S,U: Source contains call fetchEntity at line 36 - order unresolved
    Note over S,U: Source contains call setEntity at line 37 - order unresolved
    Note over S,U: Source contains call setLoaded at line 38 - order unresolved
    Note over S,U: Source contains call setEntity at line 40 - order unresolved
    Note over S,U: Source contains call setLoaded at line 41 - order unresolved
```
