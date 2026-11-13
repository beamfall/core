# Console API tokens and keys: scopes, create, revoke, one\-time secret — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:a91ca23b52b97df28174abf09ddef556 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/ApiCredentials\.tsx:42; symbol: apiScopeLabel; span SHA256: 82ae899bfdd27ffca06a52728a8ee3c7eb509e58ef1686ab4124bbf6b30f4abb.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 79231f795366585d78321cfe8ea9004e
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
