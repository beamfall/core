# Native app\-store host bridge: host messages, open in native player — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:9a46164cd4ce384f69cb1d719414785a -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/lib/beamfallHost\.ts:59; symbol: getBeamfallHost; span SHA256: 28f1c7291b785b4f820dda3e6f6585e500042d93b42b9fc7c780d15edca7253b.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow b57e3004758b0665ab3edbbe31cbe909
    Note over S,U: Source contains call getBeamfallHost at line 72 - order unresolved
    Note over S,U: Source contains call isObject at line 32 - order unresolved
    Note over S,U: Source contains call isObject at line 33 - order unresolved
    Note over S,U: Source contains call getBeamfallHost at line 85 - order unresolved
    Note over S,U: Source contains call post at line 88 - order unresolved
    Note over S,U: Source contains call getBeamfallHost at line 92 - order unresolved
    Note over S,U: Source contains call takeInitialMessages at line 95 - order unresolved
    Note over S,U: Source contains call isArray at line 96 - order unresolved
    Note over S,U: Source contains call flatMap at line 97 - order unresolved
    Note over S,U: Source contains call parseHostMessage at line 98 - order unresolved
```
