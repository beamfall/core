# Console server and transcode configuration — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:b89e56ddc682034d64055b3467b90caa -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Config\.tsx:539; symbol: Config; span SHA256: cf87b5d0b3c2a8081c41ce54a5f24e8c47b3f6572dfc88925186ee95c34fa071.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 91a647a6ca10e8235c4e87bffc10a2a4
    Note over S,U: Source contains call useT at line 90 - order unresolved
    Note over S,U: Source contains call useAsync at line 91 - order unresolved
    Note over S,U: Source contains call useState at line 93 - order unresolved
    Note over S,U: Source contains call useState at line 94 - order unresolved
    Note over S,U: Source contains call useState at line 95 - order unresolved
    Note over S,U: Source contains call useState at line 96 - order unresolved
    Note over S,U: Source contains call useState at line 99 - order unresolved
    Note over S,U: Source contains call useEffect at line 103 - order unresolved
    Note over S,U: Source contains call setForm at line 105 - order unresolved
    Note over S,U: Source contains call setCap at line 106 - order unresolved
    Note over S,U: Source contains call capText at line 106 - order unresolved
    Note over S,U: Source contains call parseCap at line 110 - order unresolved
    Note over S,U: Source contains call trim at line 114 - order unresolved
    Note over S,U: Source contains call capText at line 114 - order unresolved
    Note over S,U: Source contains call trim at line 120 - order unresolved
```
