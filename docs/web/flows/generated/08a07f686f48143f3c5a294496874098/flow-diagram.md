# Console qBittorrent ingest connection and outcomes — flow-diagram

Source revision: 684328a3dab29cbba9cf1058ce382b864f4b6b2d. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:aebd45d90840183e184200d63458ba48 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/RemoteAccess\.tsx:119; symbol: QB\_OUTCOME; span SHA256: 974482f2a9553d3517a2b9ec34f3668717f270709c24d1e6ee63eeada0c060da.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 08a07f686f48143f3c5a294496874098
    Note over S,U: Source contains call useT at line 442 - order unresolved
    Note over S,U: Source contains call useAsync at line 443 - order unresolved
    Note over S,U: Source contains call refresh at line 452 - order unresolved
    Note over S,U: Source contains call t at line 454 - order unresolved
    Note over S,U: Source contains call t at line 469 - order unresolved
    Note over S,U: Source contains call t at line 470 - order unresolved
    Note over S,U: Source contains call map at line 473 - order unresolved
    Note over S,U: Source contains call t at line 482 - order unresolved
    Note over S,U: Source contains call t at line 485 - order unresolved
    Note over S,U: Source contains call t at line 486 - order unresolved
    Note over S,U: Source contains call map at line 489 - order unresolved
    Note over S,U: Source contains call t at line 494 - order unresolved
```
