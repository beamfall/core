# Shell and per\-area error boundaries with reload panel — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:649fcde6db25e31b1d40b6f848e8a733 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/components/chrome/ErrorBoundary\.tsx:77; symbol: AreaErrorBoundary; span SHA256: 4dbf81195283cac8eb9990a580a78f24388c2e4d6586a5164893c5524057071f.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 326dfab3aa8af08b1b6eac5719aceba6
```
