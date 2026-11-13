# Console API tokens and keys: scopes, create, revoke, one\-time secret — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:a91ca23b52b97df28174abf09ddef556 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/ApiCredentials\.tsx:34; symbol: API\_SCOPE\_LABELS; span SHA256: ff2b726eff11658140fc63668c837d96b0b9923876a2c2de81700c20a04bfbfb.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 79231f795366585d78321cfe8ea9004e
    Note over S,U: Source contains call Map at line 34 - order unresolved
    Note over S,U: Source contains call map at line 34 - order unresolved
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
    Note over S,U: Source contains call toLowerCase at line 37 - order unresolved
    Note over S,U: Source contains call charAt at line 39 - order unresolved
    Note over S,U: Source contains call toUpperCase at line 39 - order unresolved
    Note over S,U: Source contains call slice at line 39 - order unresolved
    Note over S,U: Source contains call setScopes at line 177 - order unresolved
    Note over S,U: Source contains call filter at line 177 - order unresolved
```
