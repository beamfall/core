# Player OS media\-session integration \(lock\-screen controls\) — flow-diagram

Source revision: 314d15477e23e7491b4680a9142d17833b11db50. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:44e0c94c18165c1f66a949213f14b5c2 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/player/useMediaSession\.ts:23; symbol: MediaSessionOptions; span SHA256: 95576f28669ad505c5392f40a89e0b690355ee7fe776f9c634f395d223406529.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow ab85b00b0ef1be60ae43722b7edab162
    Note over S,U: Source contains call useEffect at line 66 - order unresolved
    Note over S,U: Source contains call MediaMetadata at line 73 - order unresolved
    Note over S,U: Source contains call useEffect at line 85 - order unresolved
    Note over S,U: Source contains call setActionHandler at line 93 - order unresolved
    Note over S,U: Source contains call isFinite at line 149 - order unresolved
    Note over S,U: Source contains call setPositionState at line 153 - order unresolved
    Note over S,U: Source contains call min at line 155 - order unresolved
```
