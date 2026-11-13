# Display helpers: activity, job, provider\-key and string formatting — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:2be4f055260fe96658c7617ba9dda609 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/lib/display\.ts:7; symbol: displayString; span SHA256: b2197f7f32797be6ef2343140e6cf0e53b4fd035c84c60a1188d6bd2b77f5f31.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow a814bcc8011e07198fcd174f30acbcdc
    Note over S,U: Source contains call split in internal/web/app/src/lib/display.ts at line 12 - order unresolved
    Note over S,U: Source contains call values in internal/web/app/src/lib/display.ts at line 15 - order unresolved
    Note over S,U: Source contains call URL in internal/web/app/src/lib/providerKeys.ts at line 40 - order unresolved
    Note over S,U: Source contains call get in internal/web/app/src/lib/providerKeys.ts at line 51 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/lib/providerKeys.ts at line 52 - order unresolved
    Note over S,U: Source contains call set in internal/web/app/src/lib/providerKeys.ts at line 53 - order unresolved
    Note over S,U: Source contains call entries in internal/web/app/src/lib/providerKeys.ts at line 55 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/lib/providerKeys.ts at line 55 - order unresolved
    Note over S,U: Source contains call providerKeyLabel in internal/web/app/src/lib/providerKeys.ts at line 59 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/lib/providerKeys.ts at line 60 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/lib/providerKeys.ts at line 63 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/lib/providerKeys.ts at line 63 - order unresolved
    Note over S,U: Source contains call endpointHost in internal/web/app/src/lib/providerKeys.ts at line 63 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/lib/providerKeys.ts at line 63 - order unresolved
    Note over S,U: Source contains call some in internal/web/app/src/lib/providerKeys.ts at line 64 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/lib/providerKeys.ts at line 66 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/lib/providerKeys.ts at line 67 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/lib/providerKeys.ts at line 67 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/lib/providerKeys.ts at line 27 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/lib/providerKeys.ts at line 27 - order unresolved
    Note over S,U: Source contains call every in internal/web/app/src/lib/providerKeys.ts at line 29 - order unresolved
    Note over S,U: Source contains call slice in internal/web/app/src/lib/providerKeys.ts at line 31 - order unresolved
    Note over S,U: Source contains call startsWith in internal/web/app/src/lib/providerKeys.ts at line 32 - order unresolved
    Note over S,U: Source contains call slice in internal/web/app/src/lib/providerKeys.ts at line 32 - order unresolved
    Note over S,U: Source contains call replace in internal/web/app/src/lib/providerKeys.ts at line 34 - order unresolved
    Note over S,U: Source contains call replace in internal/web/app/src/lib/providerKeys.ts at line 18 - order unresolved
    Note over S,U: Source contains call twMerge in internal/web/app/src/lib/utils.ts at line 6 - order unresolved
    Note over S,U: Source contains call clsx in internal/web/app/src/lib/utils.ts at line 6 - order unresolved
```
