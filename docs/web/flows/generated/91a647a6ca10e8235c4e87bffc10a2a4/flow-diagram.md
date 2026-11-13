# Console server and transcode configuration — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:b89e56ddc682034d64055b3467b90caa -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Config\.tsx:21; symbol: ServerConfig; span SHA256: 91c2f1f80d5a565af74b899c64ba777887f22f98e9df47ff0856e73b00ae336b.

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
```
