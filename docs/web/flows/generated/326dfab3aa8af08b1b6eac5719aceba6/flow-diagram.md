# Shell and per\-area error boundaries with reload panel — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:649fcde6db25e31b1d40b6f848e8a733 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/components/chrome/ErrorBoundary\.tsx:77; symbol: AreaErrorBoundary; span SHA256: 4dbf81195283cac8eb9990a580a78f24388c2e4d6586a5164893c5524057071f.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 326dfab3aa8af08b1b6eac5719aceba6
    Note over S,U: Source contains call getDerivedStateFromError at line 36 - order unresolved
    Note over S,U: Source contains call componentDidUpdate at line 40 - order unresolved
    Note over S,U: Source contains call setState at line 42 - order unresolved
    Note over S,U: Source contains call render at line 46 - order unresolved
    Note over S,U: Source contains call useT at line 56 - order unresolved
    Note over S,U: Source contains call t at line 61 - order unresolved
    Note over S,U: Source contains call t at line 62 - order unresolved
    Note over S,U: Source contains call t at line 64 - order unresolved
    Note over S,U: Source contains call reload at line 52 - order unresolved
```
