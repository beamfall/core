# Console API tokens and keys: scopes, create, revoke, one\-time secret — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:a91ca23b52b97df28174abf09ddef556 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/ApiCredentials\.tsx:293; symbol: ApiCredentials; span SHA256: b24c879471c66833e48c191aa9d6810b01f9d37491ed40287fcaef8f8135d587.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 79231f795366585d78321cfe8ea9004e
    Note over S,U: Source contains call useT at line 165 - order unresolved
    Note over S,U: Source contains call useAsync at line 166 - order unresolved
    Note over S,U: Source contains call useState at line 168 - order unresolved
    Note over S,U: Source contains call fromEntries at line 172 - order unresolved
    Note over S,U: Source contains call map at line 173 - order unresolved
    Note over S,U: Source contains call t at line 173 - order unresolved
    Note over S,U: Source contains call useT at line 75 - order unresolved
    Note over S,U: Source contains call useAsync at line 76 - order unresolved
    Note over S,U: Source contains call useState at line 78 - order unresolved
    Note over S,U: Source contains call useT at line 51 - order unresolved
    Note over S,U: Source contains call t at line 56 - order unresolved
    Note over S,U: Source contains call writeText at line 63 - order unresolved
    Note over S,U: Source contains call t at line 66 - order unresolved
    Note over S,U: Source contains call get at line 43 - order unresolved
    Note over S,U: Source contains call humanizeScope at line 44 - order unresolved
    Note over S,U: Source contains call trim at line 37 - order unresolved
    Note over S,U: Source contains call replace at line 37 - order unresolved
    Note over S,U: Source contains call charAt at line 39 - order unresolved
    Note over S,U: Source contains call toUpperCase at line 39 - order unresolved
    Note over S,U: Source contains call slice at line 39 - order unresolved
    Note over S,U: Source contains call setScopes at line 177 - order unresolved
    Note over S,U: Source contains call filter at line 177 - order unresolved
```
