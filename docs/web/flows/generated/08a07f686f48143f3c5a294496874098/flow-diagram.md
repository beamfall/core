# Console qBittorrent ingest connection and outcomes — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:aebd45d90840183e184200d63458ba48 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/RemoteAccess\.tsx:378; symbol: QBittorrentIngest; span SHA256: 992e79270af5b78e4f93ef0fca68f5332d54838483cf1baa4c3b10df9417f2b3.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 08a07f686f48143f3c5a294496874098
    Note over S,U: Source contains call useT at line 379 - order unresolved
    Note over S,U: Source contains call useAsync at line 380 - order unresolved
    Note over S,U: Source contains call refresh at line 390 - order unresolved
    Note over S,U: Source contains call t at line 392 - order unresolved
    Note over S,U: Source contains call t at line 407 - order unresolved
    Note over S,U: Source contains call t at line 408 - order unresolved
    Note over S,U: Source contains call map at line 411 - order unresolved
    Note over S,U: Source contains call t at line 420 - order unresolved
    Note over S,U: Source contains call t at line 423 - order unresolved
    Note over S,U: Source contains call t at line 424 - order unresolved
    Note over S,U: Source contains call map at line 427 - order unresolved
    Note over S,U: Source contains call t at line 432 - order unresolved
```
