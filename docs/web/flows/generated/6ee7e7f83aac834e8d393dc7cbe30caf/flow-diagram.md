# Server event stream \(/api/events\) with reconnect backoff — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:668267b9c9893804cca270345daa77af -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/lib/eventStream\.ts:12; symbol: EventStreamHandlers; span SHA256: c8e518ab943975eb1d0e2f58f99e04cd4b60d99eeb877fc9e1484ff655608d7a.

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
    Note over S,U: Source contains call fetch at line 75 - order unresolved
    Note over S,U: Source contains call get at line 81 - order unresolved
    Note over S,U: Source contains call includes at line 82 - order unresolved
    Note over S,U: Source contains call Error at line 83 - order unresolved
    Note over S,U: Source contains call createEventStreamParser at line 87 - order unresolved
    Note over S,U: Source contains call onEvent at line 92 - order unresolved
    Note over S,U: Source contains call MessageEvent at line 93 - order unresolved
    Note over S,U: Source contains call getReader at line 96 - order unresolved
    Note over S,U: Source contains call TextDecoder at line 97 - order unresolved
    Note over S,U: Source contains call read at line 99 - order unresolved
    Note over S,U: Source contains call feed at line 101 - order unresolved
    Note over S,U: Source contains call decode at line 101 - order unresolved
    Note over S,U: Source contains call clearTimeout at line 117 - order unresolved
    Note over S,U: Source contains call removeEventListener at line 118 - order unresolved
    Note over S,U: Source contains call resolve at line 119 - order unresolved
```
