# Console access: sign\-in security mode — flow-diagram

Source revision: 03e521f81ef085ec14c6d7d5d76533692ecba36c. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:4e6f76a2bfc906a4baf513653da37290 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/SignInSecurity\.tsx:27; symbol: MODES; span SHA256: 90e6161f267e5263014b4b349fff23872e6b22df4a24310a8031f8712616117d.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 5071ab9e9b6eb0a48f1c2a791179b71e
    Note over S,U: Source contains call useT at line 47 - order unresolved
    Note over S,U: Source contains call useId at line 48 - order unresolved
    Note over S,U: Source contains call useId at line 49 - order unresolved
    Note over S,U: Source contains call useState at line 53 - order unresolved
    Note over S,U: Source contains call useState at line 54 - order unresolved
    Note over S,U: Source contains call useState at line 56 - order unresolved
    Note over S,U: Source contains call useState at line 60 - order unresolved
    Note over S,U: Source contains call useState at line 61 - order unresolved
    Note over S,U: Source contains call useState at line 64 - order unresolved
    Note over S,U: Source contains call useEffect at line 66 - order unresolved
    Note over S,U: Source contains call setLoadFailed at line 68 - order unresolved
    Note over S,U: Source contains call fetchSetupStatus at line 69 - order unresolved
    Note over S,U: Source contains call then at line 69 - order unresolved
    Note over S,U: Source contains call setLoadFailed at line 72 - order unresolved
    Note over S,U: Source contains call setCurrent at line 75 - order unresolved
    Note over S,U: Source contains call setSelected at line 76 - order unresolved
    Note over S,U: Source contains call preventDefault at line 84 - order unresolved
    Note over S,U: Source contains call setConfirmStrict at line 87 - order unresolved
    Note over S,U: Source contains call apply at line 90 - order unresolved
```
