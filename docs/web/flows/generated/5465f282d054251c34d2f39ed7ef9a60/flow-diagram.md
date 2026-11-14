# Setup: configure the update\-check channel — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:90dc49e6a5b16314d1d9c6d9da74ea5e -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/setup/orchestrate\.ts:38; symbol: UpdateCheckChannel; span SHA256: bd5955f7abfd6b60f80d302824d8ee419c7c19aa76ad7abc062bdee76afbfb44.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 5465f282d054251c34d2f39ed7ef9a60
    Note over S,U: Source contains call verifyAdminSession in internal/web/app/src/areas/setup/orchestrate.ts at line 188 - order unresolved
    Note over S,U: Source contains call setConsoleConfig in internal/web/app/src/areas/setup/orchestrate.ts at line 189 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/auth.ts at line 28 - order unresolved
```
