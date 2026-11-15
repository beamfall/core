# Setup: choose sign\-in security mode and create the owner Profile — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:383f60ef74dfd5713690f4a85a5785fe -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/setup/SetupWizard\.tsx:131; symbol: SECURITY\_MODES; span SHA256: 009350b6896c002518fa268b58f8440741d9067009d6d86cabe95062b7ec28f7.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 2f5c3ea4990f350bfe1347a7600ed0cd
    Note over S,U: Source contains call constructor at line 80 - order unresolved
    Note over S,U: Source contains call super at line 81 - order unresolved
    Note over S,U: Source contains call fetchSetupStatus at line 273 - order unresolved
    Note over S,U: Source contains call fetchSetupState at line 278 - order unresolved
    Note over S,U: Source contains call fetchLibraries at line 280 - order unresolved
    Note over S,U: Source contains call find at line 281 - order unresolved
    Note over S,U: Source contains call find at line 282 - order unresolved
    Note over S,U: Source contains call SetupTokenError at line 109 - order unresolved
```
