# Setup: configure the update\-check channel — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:90dc49e6a5b16314d1d9c6d9da74ea5e -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/setup/orchestrate\.ts:181; symbol: configureUpdateCheck; span SHA256: eb088f1d1e70a55690701dc5051f24349cd9a6c6d01ef24fa405600c0a9e023a.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 5465f282d054251c34d2f39ed7ef9a60
    Note over S,U: Source contains call verifyAdminSession in internal/web/app/src/areas/setup/orchestrate.ts at line 186 - order unresolved
    Note over S,U: Source contains call setConsoleConfig in internal/web/app/src/areas/setup/orchestrate.ts at line 187 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/auth.ts at line 28 - order unresolved
```
