# Console retention rules — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:70a4b7a561e855b9bd630408774b8e25 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Retention\.tsx:21; symbol: Retention; span SHA256: e7fe8429b520baef04dd1941a40c2c4e44d2b3f3db6421b63b073b877342d5a1.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow a27521afd9a1aa9212ea439dbec3ef84
    Note over S,U: Source contains call useT at line 22 - order unresolved
    Note over S,U: Source contains call useAsync at line 23 - order unresolved
    Note over S,U: Source contains call useState at line 24 - order unresolved
    Note over S,U: Source contains call useState at line 25 - order unresolved
    Note over S,U: Source contains call preventDefault at line 34 - order unresolved
    Note over S,U: Source contains call createRetentionRule at line 35 - order unresolved
    Note over S,U: Source contains call setCreating at line 36 - order unresolved
    Note over S,U: Source contains call setDraft at line 37 - order unresolved
    Note over S,U: Source contains call refresh at line 38 - order unresolved
    Note over S,U: Source contains call deleteRetentionRule at line 47 - order unresolved
    Note over S,U: Source contains call refresh at line 48 - order unresolved
    Note over S,U: Source contains call updateRetentionRule at line 42 - order unresolved
    Note over S,U: Source contains call refresh at line 43 - order unresolved
```
