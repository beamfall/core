# Setup: choose sign\-in security mode and create the owner Profile — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:383f60ef74dfd5713690f4a85a5785fe -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/setup/SetupWizard\.tsx:88; symbol: SECURITY\_MODES; span SHA256: 009350b6896c002518fa268b58f8440741d9067009d6d86cabe95062b7ec28f7.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 2f5c3ea4990f350bfe1347a7600ed0cd
    Note over S,U: Source contains call constructor at line 97 - order unresolved
    Note over S,U: Source contains call super at line 98 - order unresolved
    Note over S,U: Source contains call fetchSetupStatus at line 228 - order unresolved
    Note over S,U: Source contains call fetchSetupState at line 231 - order unresolved
    Note over S,U: Source contains call fetchLibraries at line 233 - order unresolved
    Note over S,U: Source contains call find at line 234 - order unresolved
    Note over S,U: Source contains call find at line 235 - order unresolved
    Note over S,U: Source contains call SetupTokenError at line 126 - order unresolved
```
