# Server activity indicator \(running jobs and rescans\) in the shell — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:3e597afee5d80bfa22564d704454121e -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/components/beamfall/ServerActivity\.tsx:19; symbol: ACTIVE\_POLL\_MS; span SHA256: 15aee462f01638cfa092a2b1c1be144490603ce140d97ff5961dfcbedeaf4d7f.

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
