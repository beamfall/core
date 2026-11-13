# Setup: activate plugins and enter provider keys — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:72288c2765450ca0a119a9453a4e8ec3 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/setup/orchestrate\.ts:159; symbol: activatePlugins; span SHA256: 026ffcbbcccbe3553646fa05bca51707878dcf9f1e0cf43e3cc3ae3202e11b87.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6c861cacb507b604f2986e0397a2b389
    Note over S,U: Source contains call setPluginActive in internal/web/app/src/areas/setup/orchestrate.ts at line 161 - order unresolved
    Note over S,U: Source contains call verifyAdminSession in internal/web/app/src/areas/setup/orchestrate.ts at line 171 - order unresolved
    Note over S,U: Source contains call fetchConsoleConfig in internal/web/app/src/areas/setup/orchestrate.ts at line 172 - order unresolved
    Note over S,U: Source contains call groupProviderKeys in internal/web/app/src/areas/setup/orchestrate.ts at line 173 - order unresolved
    Note over S,U: Source contains call flatMap in internal/web/app/src/areas/setup/orchestrate.ts at line 173 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/auth.ts at line 28 - order unresolved
```
