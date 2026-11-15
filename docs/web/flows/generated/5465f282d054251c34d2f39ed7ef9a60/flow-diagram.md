# Setup: configure the update\-check channel — flow-diagram

Source revision: a75e0f8437987a575a00a20b8d515be33b584f10. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:90dc49e6a5b16314d1d9c6d9da74ea5e -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/setup/orchestrate\.ts:33; symbol: UpdateCheckChannel; span SHA256: bd5955f7abfd6b60f80d302824d8ee419c7c19aa76ad7abc062bdee76afbfb44.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 5465f282d054251c34d2f39ed7ef9a60
    Note over S,U: Source contains call verifyAdminSession in internal/web/app/src/areas/setup/orchestrate.ts at line 231 - order unresolved
    Note over S,U: Source contains call setConsoleConfig in internal/web/app/src/areas/setup/orchestrate.ts at line 232 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/auth.ts at line 28 - order unresolved
```
