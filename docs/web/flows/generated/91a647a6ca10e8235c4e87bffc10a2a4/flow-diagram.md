# Console server and transcode configuration — flow-diagram

Source revision: 25a58b2a60b350f2a582cb407acbbbc52274a39b. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:b89e56ddc682034d64055b3467b90caa -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Config\.tsx:295; symbol: Config; span SHA256: cf87b5d0b3c2a8081c41ce54a5f24e8c47b3f6572dfc88925186ee95c34fa071.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 91a647a6ca10e8235c4e87bffc10a2a4
    Note over S,U: Source contains call useT at line 38 - order unresolved
    Note over S,U: Source contains call useAsync at line 39 - order unresolved
    Note over S,U: Source contains call useEffect at line 49 - order unresolved
    Note over S,U: Source contains call setForm at line 50 - order unresolved
```
