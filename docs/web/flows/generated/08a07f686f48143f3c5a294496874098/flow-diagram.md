# Console qBittorrent ingest connection and outcomes — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:aebd45d90840183e184200d63458ba48 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/RemoteAccess\.tsx:117; symbol: QB\_OUTCOME; span SHA256: 974482f2a9553d3517a2b9ec34f3668717f270709c24d1e6ee63eeada0c060da.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 08a07f686f48143f3c5a294496874098
    Note over S,U: Source contains call useT at line 369 - order unresolved
    Note over S,U: Source contains call useAsync at line 370 - order unresolved
    Note over S,U: Source contains call refresh at line 379 - order unresolved
    Note over S,U: Source contains call t at line 381 - order unresolved
    Note over S,U: Source contains call t at line 396 - order unresolved
    Note over S,U: Source contains call t at line 397 - order unresolved
    Note over S,U: Source contains call map at line 400 - order unresolved
    Note over S,U: Source contains call t at line 409 - order unresolved
    Note over S,U: Source contains call t at line 412 - order unresolved
    Note over S,U: Source contains call t at line 413 - order unresolved
    Note over S,U: Source contains call map at line 416 - order unresolved
    Note over S,U: Source contains call t at line 421 - order unresolved
```
