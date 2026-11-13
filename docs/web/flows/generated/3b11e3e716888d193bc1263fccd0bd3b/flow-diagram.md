# Console access: devices and pairing codes — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:627edae7273401dd1b8e57469578b420 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Devices\.tsx:57; symbol: Devices; span SHA256: 91122bd481a61a50c8979c2c0c3e5c18a73f7bcb28014fd595308c1dee7ffbf5.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 3b11e3e716888d193bc1263fccd0bd3b
    Note over S,U: Source contains call useT at line 58 - order unresolved
    Note over S,U: Source contains call useAsync at line 59 - order unresolved
    Note over S,U: Source contains call useAsync at line 60 - order unresolved
    Note over S,U: Source contains call useState at line 62 - order unresolved
    Note over S,U: Source contains call useT at line 35 - order unresolved
    Note over S,U: Source contains call t at line 40 - order unresolved
    Note over S,U: Source contains call t at line 50 - order unresolved
    Note over S,U: Source contains call setBusy at line 65 - order unresolved
    Note over S,U: Source contains call setMinted at line 67 - order unresolved
    Note over S,U: Source contains call mintPairingCode at line 67 - order unresolved
    Note over S,U: Source contains call setBusy at line 69 - order unresolved
    Note over S,U: Source contains call revokeDevice at line 74 - order unresolved
    Note over S,U: Source contains call refresh at line 75 - order unresolved
    Note over S,U: Source contains call revokeAllDevices at line 79 - order unresolved
    Note over S,U: Source contains call refresh at line 80 - order unresolved
    Note over S,U: Source contains call setDeviceRole at line 84 - order unresolved
    Note over S,U: Source contains call refresh at line 85 - order unresolved
    Note over S,U: Source contains call setConsoleConfig at line 89 - order unresolved
    Note over S,U: Source contains call setData at line 90 - order unresolved
```
