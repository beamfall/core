# Console access: devices and pairing codes — flow-diagram

Source revision: a75e0f8437987a575a00a20b8d515be33b584f10. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:627edae7273401dd1b8e57469578b420 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Devices\.tsx:148; symbol: Devices; span SHA256: 91122bd481a61a50c8979c2c0c3e5c18a73f7bcb28014fd595308c1dee7ffbf5.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 3b11e3e716888d193bc1263fccd0bd3b
    Note over S,U: Source contains call useT at line 149 - order unresolved
    Note over S,U: Source contains call useAsync at line 150 - order unresolved
    Note over S,U: Source contains call useAsync at line 151 - order unresolved
    Note over S,U: Source contains call useState at line 153 - order unresolved
    Note over S,U: Source contains call useT at line 62 - order unresolved
    Note over S,U: Source contains call useSecondsLeft at line 63 - order unresolved
    Note over S,U: Source contains call t at line 66 - order unresolved
    Note over S,U: Source contains call useState at line 67 - order unresolved
    Note over S,U: Source contains call useId at line 69 - order unresolved
    Note over S,U: Source contains call useEffect at line 70 - order unresolved
    Note over S,U: Source contains call focus at line 72 - order unresolved
    Note over S,U: Source contains call select at line 73 - order unresolved
    Note over S,U: Source contains call setBusy at line 156 - order unresolved
    Note over S,U: Source contains call setMinted at line 158 - order unresolved
    Note over S,U: Source contains call mintPairingCode at line 158 - order unresolved
    Note over S,U: Source contains call setBusy at line 160 - order unresolved
    Note over S,U: Source contains call revokeDevice at line 165 - order unresolved
    Note over S,U: Source contains call refresh at line 166 - order unresolved
    Note over S,U: Source contains call revokeAllDevices at line 170 - order unresolved
    Note over S,U: Source contains call refresh at line 171 - order unresolved
    Note over S,U: Source contains call setDeviceRole at line 175 - order unresolved
    Note over S,U: Source contains call refresh at line 176 - order unresolved
    Note over S,U: Source contains call setConsoleConfig at line 180 - order unresolved
    Note over S,U: Source contains call setData at line 181 - order unresolved
```
