# Setup: activate plugins and enter provider keys — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:72288c2765450ca0a119a9453a4e8ec3 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/setup/SetupWizard\.tsx:109; symbol: PLUGIN\_ICON; span SHA256: 05333a405746aecee5aaad7e9a91ad91fcdda7e6f1e4a8700ec73ec1f5962de8.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6c861cacb507b604f2986e0397a2b389
    Note over S,U: Source contains call setPluginActive in internal/web/app/src/areas/setup/orchestrate.ts at line 163 - order unresolved
    Note over S,U: Source contains call verifyAdminSession in internal/web/app/src/areas/setup/orchestrate.ts at line 173 - order unresolved
    Note over S,U: Source contains call fetchConsoleConfig in internal/web/app/src/areas/setup/orchestrate.ts at line 174 - order unresolved
    Note over S,U: Source contains call groupProviderKeys in internal/web/app/src/areas/setup/orchestrate.ts at line 175 - order unresolved
    Note over S,U: Source contains call flatMap in internal/web/app/src/areas/setup/orchestrate.ts at line 175 - order unresolved
    Note over S,U: Source contains call rebindCompatibility in internal/web/app/src/lib/graphql/auth.ts at line 28 - order unresolved
```
