# Console server and transcode configuration — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:b89e56ddc682034d64055b3467b90caa -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Config\.tsx:142; symbol: Config; span SHA256: cf87b5d0b3c2a8081c41ce54a5f24e8c47b3f6572dfc88925186ee95c34fa071.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 91a647a6ca10e8235c4e87bffc10a2a4
    Note over S,U: Source contains call useT at line 33 - order unresolved
    Note over S,U: Source contains call useAsync at line 34 - order unresolved
    Note over S,U: Source contains call useEffect at line 44 - order unresolved
    Note over S,U: Source contains call setForm at line 45 - order unresolved
```
