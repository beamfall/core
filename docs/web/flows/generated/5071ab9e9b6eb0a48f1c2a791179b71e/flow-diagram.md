# Console access: sign\-in security mode — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:4e6f76a2bfc906a4baf513653da37290 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/SignInSecurity\.tsx:18; symbol: MODES; span SHA256: 90e6161f267e5263014b4b349fff23872e6b22df4a24310a8031f8712616117d.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 5071ab9e9b6eb0a48f1c2a791179b71e
    Note over S,U: Source contains call useT at line 38 - order unresolved
    Note over S,U: Source contains call useId at line 39 - order unresolved
    Note over S,U: Source contains call useId at line 40 - order unresolved
    Note over S,U: Source contains call useState at line 44 - order unresolved
    Note over S,U: Source contains call useState at line 45 - order unresolved
    Note over S,U: Source contains call useState at line 47 - order unresolved
    Note over S,U: Source contains call useState at line 51 - order unresolved
    Note over S,U: Source contains call useState at line 52 - order unresolved
    Note over S,U: Source contains call useEffect at line 54 - order unresolved
    Note over S,U: Source contains call setLoadFailed at line 56 - order unresolved
    Note over S,U: Source contains call fetchSetupStatus at line 57 - order unresolved
    Note over S,U: Source contains call then at line 57 - order unresolved
    Note over S,U: Source contains call setLoadFailed at line 60 - order unresolved
    Note over S,U: Source contains call setCurrent at line 63 - order unresolved
    Note over S,U: Source contains call setSelected at line 64 - order unresolved
    Note over S,U: Source contains call preventDefault at line 72 - order unresolved
    Note over S,U: Source contains call setBusy at line 74 - order unresolved
    Note over S,U: Source contains call setError at line 75 - order unresolved
    Note over S,U: Source contains call setSaved at line 76 - order unresolved
    Note over S,U: Source contains call setSignInSecurityMode at line 78 - order unresolved
    Note over S,U: Source contains call setCurrent at line 79 - order unresolved
    Note over S,U: Source contains call setSelected at line 80 - order unresolved
    Note over S,U: Source contains call setSaved at line 81 - order unresolved
    Note over S,U: Source contains call setError at line 83 - order unresolved
    Note over S,U: Source contains call refusalMessage at line 83 - order unresolved
    Note over S,U: Source contains call requestAnimationFrame at line 84 - order unresolved
    Note over S,U: Source contains call focus at line 84 - order unresolved
    Note over S,U: Source contains call setPassword at line 86 - order unresolved
    Note over S,U: Source contains call setBusy at line 87 - order unresolved
```
