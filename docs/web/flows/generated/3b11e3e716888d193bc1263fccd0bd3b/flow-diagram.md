# Console access: devices and pairing codes — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:627edae7273401dd1b8e57469578b420 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Devices\.tsx:58; symbol: Devices; span SHA256: 91122bd481a61a50c8979c2c0c3e5c18a73f7bcb28014fd595308c1dee7ffbf5.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 3b11e3e716888d193bc1263fccd0bd3b
    Note over S,U: Source contains call useT at line 59 - order unresolved
    Note over S,U: Source contains call useAsync at line 60 - order unresolved
    Note over S,U: Source contains call useAsync at line 61 - order unresolved
    Note over S,U: Source contains call useState at line 63 - order unresolved
    Note over S,U: Source contains call useT at line 36 - order unresolved
    Note over S,U: Source contains call t at line 41 - order unresolved
    Note over S,U: Source contains call writeText at line 48 - order unresolved
    Note over S,U: Source contains call t at line 51 - order unresolved
    Note over S,U: Source contains call setBusy at line 66 - order unresolved
    Note over S,U: Source contains call setMinted at line 68 - order unresolved
    Note over S,U: Source contains call mintPairingCode at line 68 - order unresolved
    Note over S,U: Source contains call setBusy at line 70 - order unresolved
    Note over S,U: Source contains call revokeDevice at line 75 - order unresolved
    Note over S,U: Source contains call refresh at line 76 - order unresolved
    Note over S,U: Source contains call revokeAllDevices at line 80 - order unresolved
    Note over S,U: Source contains call refresh at line 81 - order unresolved
    Note over S,U: Source contains call setDeviceRole at line 85 - order unresolved
    Note over S,U: Source contains call refresh at line 86 - order unresolved
    Note over S,U: Source contains call setConsoleConfig at line 90 - order unresolved
    Note over S,U: Source contains call setData at line 91 - order unresolved
```
