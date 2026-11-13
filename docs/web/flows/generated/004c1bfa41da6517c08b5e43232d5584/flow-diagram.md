# Server activity indicator \(running jobs and rescans\) in the shell — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:3e597afee5d80bfa22564d704454121e -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/components/beamfall/ServerActivity\.tsx:22; symbol: ServerActivity; span SHA256: fa0d4a07644d2f7afae61ab1daf203693a17f2c36308e0a3f7dadee7beaa286c.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 004c1bfa41da6517c08b5e43232d5584
    Note over S,U: Source contains call useT at line 23 - order unresolved
    Note over S,U: Source contains call useAnyRescanActive at line 24 - order unresolved
    Note over S,U: Source contains call useEffect at line 29 - order unresolved
    Note over S,U: Source contains call refresh at line 55 - order unresolved
    Note over S,U: Source contains call clearTimeout at line 34 - order unresolved
    Note over S,U: Source contains call fetchJobs at line 43 - order unresolved
    Note over S,U: Source contains call filter at line 44 - order unresolved
    Note over S,U: Source contains call setRunning at line 50 - order unresolved
    Note over S,U: Source contains call setTimeout at line 52 - order unresolved
    Note over S,U: Source contains call refresh at line 52 - order unresolved
```
