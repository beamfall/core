# Native app\-store host bridge: host messages, open in native player — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:9a46164cd4ce384f69cb1d719414785a -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/lib/beamfallHost\.ts:9; symbol: BeamfallHost; span SHA256: 24f509a1866fc546236c869936714236b211761800ec4d286d8c0ca7aea18835.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow b57e3004758b0665ab3edbbe31cbe909
    Note over S,U: Source contains call getBeamfallHost at line 72 - order unresolved
    Note over S,U: Source contains call isObject at line 32 - order unresolved
    Note over S,U: Source contains call isObject at line 33 - order unresolved
    Note over S,U: Source contains call getBeamfallHost at line 85 - order unresolved
    Note over S,U: Source contains call post at line 88 - order unresolved
    Note over S,U: Source contains call getBeamfallHost at line 92 - order unresolved
    Note over S,U: Source contains call takeInitialMessages at line 95 - order unresolved
    Note over S,U: Source contains call isArray at line 96 - order unresolved
    Note over S,U: Source contains call flatMap at line 97 - order unresolved
    Note over S,U: Source contains call parseHostMessage at line 98 - order unresolved
```
