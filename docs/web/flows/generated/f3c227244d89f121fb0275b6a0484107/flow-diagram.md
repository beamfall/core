# Console library health: dimensions, drilldowns, bulk remediation — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:14ce4f588cdaccb3c45c9e9c6e03edfe -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Health\.tsx:77; symbol: aggregateCounts; span SHA256: 7e0dfa68a6bb9572a0a562d9d5fb58b2a524f2713ae7fedbea50435c151b98b9.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow f3c227244d89f121fb0275b6a0484107
    Note over S,U: Source contains call setDrill at line 363 - order unresolved
    Note over S,U: Source contains call setDrill at line 367 - order unresolved
    Note over S,U: Source contains call refresh at line 368 - order unresolved
    Note over S,U: Source contains call setDrill at line 351 - order unresolved
    Note over S,U: Source contains call runHealthBulkOp at line 346 - order unresolved
    Note over S,U: Source contains call refresh at line 347 - order unresolved
    Note over S,U: Source contains call from at line 217 - order unresolved
    Note over S,U: Source contains call runHealthBulkOpSelected at line 219 - order unresolved
    Note over S,U: Source contains call onDone at line 220 - order unresolved
    Note over S,U: Source contains call setChecked at line 195 - order unresolved
    Note over S,U: Source contains call Set at line 195 - order unresolved
    Note over S,U: Source contains call setChecked at line 197 - order unresolved
    Note over S,U: Source contains call Set at line 197 - order unresolved
    Note over S,U: Source contains call map at line 197 - order unresolved
    Note over S,U: Source contains call setChecked at line 202 - order unresolved
    Note over S,U: Source contains call Set at line 203 - order unresolved
    Note over S,U: Source contains call every at line 204 - order unresolved
    Note over S,U: Source contains call has at line 204 - order unresolved
    Note over S,U: Source contains call delete at line 207 - order unresolved
    Note over S,U: Source contains call add at line 209 - order unresolved
    Note over S,U: Source contains call setChecked at line 182 - order unresolved
    Note over S,U: Source contains call Set at line 183 - order unresolved
    Note over S,U: Source contains call has at line 184 - order unresolved
    Note over S,U: Source contains call delete at line 185 - order unresolved
    Note over S,U: Source contains call add at line 187 - order unresolved
```
