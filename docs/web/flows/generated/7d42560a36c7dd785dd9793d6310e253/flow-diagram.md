# Service worker registration and reload\-on\-controller\-change — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:56d463b3d3c6cd0c370f2cb95e0573dc -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/lib/pwa\.ts:32; symbol: registerServiceWorker; span SHA256: 55a7cccd9279f66b2ba0e5458bc0af4b632b9ae52b3bbc338a2a2dc5a7f9ee03.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 7d42560a36c7dd785dd9793d6310e253
    Note over S,U: Source contains call reloadOnceOnControllerChange at line 40 - order unresolved
    Note over S,U: Source contains call reload at line 40 - order unresolved
    Note over S,U: Source contains call addEventListener at line 41 - order unresolved
    Note over S,U: Source contains call register at line 42 - order unresolved
    Note over S,U: Source contains call catch at line 42 - order unresolved
    Note over S,U: Source contains call addEventListener at line 25 - order unresolved
    Note over S,U: Source contains call reload at line 28 - order unresolved
```
