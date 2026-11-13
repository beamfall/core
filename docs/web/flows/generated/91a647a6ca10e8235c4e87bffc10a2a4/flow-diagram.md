# Console server and transcode configuration — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:b89e56ddc682034d64055b3467b90caa -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Config\.tsx:171; symbol: Config; span SHA256: cf87b5d0b3c2a8081c41ce54a5f24e8c47b3f6572dfc88925186ee95c34fa071.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 91a647a6ca10e8235c4e87bffc10a2a4
    Note over S,U: Source contains call useT at line 22 - order unresolved
    Note over S,U: Source contains call useAsync at line 23 - order unresolved
    Note over S,U: Source contains call useState at line 24 - order unresolved
    Note over S,U: Source contains call useEffect at line 26 - order unresolved
    Note over S,U: Source contains call setWorkers at line 27 - order unresolved
    Note over S,U: Source contains call String at line 27 - order unresolved
    Note over S,U: Source contains call useT at line 72 - order unresolved
    Note over S,U: Source contains call useAsync at line 73 - order unresolved
    Note over S,U: Source contains call useEffect at line 83 - order unresolved
    Note over S,U: Source contains call setForm at line 84 - order unresolved
```
