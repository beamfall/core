# Setup: choose sign\-in security mode and create the owner Profile — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:383f60ef74dfd5713690f4a85a5785fe -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/setup/SetupWizard\.tsx:94; symbol: SECURITY\_MODES; span SHA256: 009350b6896c002518fa268b58f8440741d9067009d6d86cabe95062b7ec28f7.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 2f5c3ea4990f350bfe1347a7600ed0cd
    Note over S,U: Source contains call constructor at line 95 - order unresolved
    Note over S,U: Source contains call super at line 96 - order unresolved
    Note over S,U: Source contains call fetchSetupStatus at line 226 - order unresolved
    Note over S,U: Source contains call fetchSetupState at line 229 - order unresolved
    Note over S,U: Source contains call fetchLibraries at line 231 - order unresolved
    Note over S,U: Source contains call find at line 232 - order unresolved
    Note over S,U: Source contains call find at line 233 - order unresolved
    Note over S,U: Source contains call SetupTokenError at line 124 - order unresolved
```
