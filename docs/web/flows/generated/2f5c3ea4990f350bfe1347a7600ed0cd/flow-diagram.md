# Setup: choose sign\-in security mode and create the owner Profile — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:383f60ef74dfd5713690f4a85a5785fe -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/setup/orchestrate\.ts:225; symbol: bootstrapResume; span SHA256: a688d5610f848cc6ce5b1f97c8e93be757d4d0752bc0a1a7adb7f77f261c717a.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 2f5c3ea4990f350bfe1347a7600ed0cd
    Note over S,U: Source contains call fetchSetupStatus at line 226 - order unresolved
    Note over S,U: Source contains call fetchSetupState at line 229 - order unresolved
    Note over S,U: Source contains call fetchLibraries at line 231 - order unresolved
    Note over S,U: Source contains call find at line 232 - order unresolved
    Note over S,U: Source contains call find at line 233 - order unresolved
    Note over S,U: Source contains call SetupTokenError at line 124 - order unresolved
```
