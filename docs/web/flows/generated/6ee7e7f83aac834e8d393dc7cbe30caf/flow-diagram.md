# Server event stream \(/api/events\) with reconnect backoff — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:668267b9c9893804cca270345daa77af -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/lib/eventStream\.ts:22; symbol: createEventStreamParser; span SHA256: baffc7202ec733f3e62791b18cf0f30368f40d0e8b62e1f5d36d3535af30056b.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6ee7e7f83aac834e8d393dc7cbe30caf
    Note over S,U: Source contains call AbortController at line 67 - order unresolved
    Note over S,U: Source contains call onFrame at line 32 - order unresolved
    Note over S,U: Source contains call join at line 32 - order unresolved
    Note over S,U: Source contains call startsWith at line 37 - order unresolved
    Note over S,U: Source contains call indexOf at line 38 - order unresolved
    Note over S,U: Source contains call slice at line 39 - order unresolved
    Note over S,U: Source contains call slice at line 40 - order unresolved
    Note over S,U: Source contains call startsWith at line 41 - order unresolved
    Note over S,U: Source contains call slice at line 41 - order unresolved
    Note over S,U: Source contains call push at line 43 - order unresolved
    Note over S,U: Source contains call includes at line 44 - order unresolved
    Note over S,U: Source contains call async at line 72 - order unresolved
    Note over S,U: Source contains call Error at line 83 - order unresolved
    Note over S,U: Source contains call onEvent at line 92 - order unresolved
    Note over S,U: Source contains call MessageEvent at line 93 - order unresolved
    Note over S,U: Source contains call read at line 99 - order unresolved
    Note over S,U: Source contains call feed at line 101 - order unresolved
    Note over S,U: Source contains call decode at line 101 - order unresolved
    Note over S,U: Source contains call readOnce at line 108 - order unresolved
    Note over S,U: Source contains call Event at line 113 - order unresolved
    Note over S,U: Source contains call clearTimeout at line 117 - order unresolved
    Note over S,U: Source contains call removeEventListener at line 118 - order unresolved
    Note over S,U: Source contains call resolve at line 119 - order unresolved
```
