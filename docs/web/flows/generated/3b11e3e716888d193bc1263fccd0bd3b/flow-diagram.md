# Console access: devices and pairing codes — flow-diagram

Source revision: 25a58b2a60b350f2a582cb407acbbbc52274a39b. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:627edae7273401dd1b8e57469578b420 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Devices\.tsx:192; symbol: Devices; span SHA256: 91122bd481a61a50c8979c2c0c3e5c18a73f7bcb28014fd595308c1dee7ffbf5.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 3b11e3e716888d193bc1263fccd0bd3b
    Note over S,U: Source contains call useT at line 193 - order unresolved
    Note over S,U: Source contains call useAsync at line 194 - order unresolved
    Note over S,U: Source contains call useAsync at line 195 - order unresolved
    Note over S,U: Source contains call useState at line 197 - order unresolved
    Note over S,U: Source contains call useT at line 105 - order unresolved
    Note over S,U: Source contains call useSecondsLeft at line 106 - order unresolved
    Note over S,U: Source contains call t at line 109 - order unresolved
    Note over S,U: Source contains call useState at line 110 - order unresolved
    Note over S,U: Source contains call useId at line 112 - order unresolved
    Note over S,U: Source contains call useEffect at line 113 - order unresolved
    Note over S,U: Source contains call focus at line 115 - order unresolved
    Note over S,U: Source contains call select at line 116 - order unresolved
    Note over S,U: Source contains call setBusy at line 200 - order unresolved
    Note over S,U: Source contains call setMinted at line 202 - order unresolved
    Note over S,U: Source contains call mintPairingCode at line 202 - order unresolved
    Note over S,U: Source contains call setBusy at line 204 - order unresolved
    Note over S,U: Source contains call revokeDevice at line 209 - order unresolved
    Note over S,U: Source contains call refresh at line 210 - order unresolved
    Note over S,U: Source contains call revokeAllDevices at line 214 - order unresolved
    Note over S,U: Source contains call refresh at line 215 - order unresolved
    Note over S,U: Source contains call setDeviceRole at line 219 - order unresolved
    Note over S,U: Source contains call refresh at line 220 - order unresolved
    Note over S,U: Source contains call setConsoleConfig at line 224 - order unresolved
    Note over S,U: Source contains call setData at line 225 - order unresolved
```
