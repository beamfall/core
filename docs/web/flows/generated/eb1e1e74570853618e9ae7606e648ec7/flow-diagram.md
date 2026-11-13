# Console identification and layout capabilities — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:b6216acf00de71903620456bc61f35b4 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Capabilities\.tsx:19; symbol: Capabilities; span SHA256: 32a8e7803eaa9f383ff15e6655ced66770a6b1877e806367d42975679b21d5a5.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow eb1e1e74570853618e9ae7606e648ec7
    Note over S,U: Source contains call useT at line 20 - order unresolved
    Note over S,U: Source contains call useAsync at line 21 - order unresolved
    Note over S,U: Source contains call setBusy at line 25 - order unresolved
    Note over S,U: Source contains call setCapabilityEnabled at line 27 - order unresolved
    Note over S,U: Source contains call setData at line 28 - order unresolved
    Note over S,U: Source contains call setBusy at line 30 - order unresolved
```
