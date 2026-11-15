# Console qBittorrent ingest connection and outcomes — flow-diagram

Source revision: b952880cf4519beaa9738e25e349aabf600c9a41. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:aebd45d90840183e184200d63458ba48 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/RemoteAccess\.tsx:162; symbol: QB\_OUTCOME; span SHA256: 974482f2a9553d3517a2b9ec34f3668717f270709c24d1e6ee63eeada0c060da.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 08a07f686f48143f3c5a294496874098
    Note over S,U: Source contains call useT at line 654 - order unresolved
    Note over S,U: Source contains call useAsync at line 655 - order unresolved
    Note over S,U: Source contains call refresh at line 665 - order unresolved
    Note over S,U: Source contains call t at line 667 - order unresolved
    Note over S,U: Source contains call t at line 682 - order unresolved
    Note over S,U: Source contains call t at line 683 - order unresolved
    Note over S,U: Source contains call map at line 686 - order unresolved
    Note over S,U: Source contains call t at line 695 - order unresolved
    Note over S,U: Source contains call t at line 698 - order unresolved
    Note over S,U: Source contains call t at line 699 - order unresolved
    Note over S,U: Source contains call map at line 702 - order unresolved
    Note over S,U: Source contains call t at line 707 - order unresolved
```
