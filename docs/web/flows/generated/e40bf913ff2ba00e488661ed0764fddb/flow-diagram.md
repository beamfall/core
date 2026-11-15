# Console remote access: certificate, relay, direct connection, watch\-away guide — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:786209b29f4d10a61de26299e4f95511 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/RemoteAccess\.tsx:66; symbol: CERT\_STATE; span SHA256: 34c6e6df39126b1ed055c8538ad882ae2190a0b5ad63d03e6e98e2ef9e0b8cab.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow e40bf913ff2ba00e488661ed0764fddb
    Note over S,U: Source contains call useT at line 186 - order unresolved
    Note over S,U: Source contains call t at line 190 - order unresolved
    Note over S,U: Source contains call useT at line 235 - order unresolved
    Note over S,U: Source contains call t at line 241 - order unresolved
    Note over S,U: Source contains call t at line 243 - order unresolved
    Note over S,U: Source contains call useT at line 365 - order unresolved
    Note over S,U: Source contains call useAsync at line 366 - order unresolved
    Note over S,U: Source contains call useDisplayLocale at line 367 - order unresolved
    Note over S,U: Source contains call connectionClassLabels at line 368 - order unresolved
    Note over S,U: Source contains call refresh at line 375 - order unresolved
    Note over S,U: Source contains call t at line 377 - order unresolved
    Note over S,U: Source contains call t at line 388 - order unresolved
    Note over S,U: Source contains call ListFormat at line 390 - order unresolved
    Note over S,U: Source contains call format at line 390 - order unresolved
    Note over S,U: Source contains call map at line 391 - order unresolved
    Note over S,U: Source contains call t at line 391 - order unresolved
    Note over S,U: Source contains call t at line 398 - order unresolved
    Note over S,U: Source contains call t at line 403 - order unresolved
    Note over S,U: Source contains call useT at line 267 - order unresolved
    Note over S,U: Source contains call find at line 269 - order unresolved
    Note over S,U: Source contains call currentEndpointKind at line 270 - order unresolved
    Note over S,U: Source contains call directState at line 276 - order unresolved
    Note over S,U: Source contains call t at line 282 - order unresolved
    Note over S,U: Source contains call t at line 283 - order unresolved
    Note over S,U: Source contains call t at line 292 - order unresolved
    Note over S,U: Source contains call t at line 295 - order unresolved
    Note over S,U: Source contains call t at line 297 - order unresolved
    Note over S,U: Source contains call t at line 303 - order unresolved
    Note over S,U: Source contains call t at line 308 - order unresolved
    Note over S,U: Source contains call refresh at line 311 - order unresolved
    Note over S,U: Source contains call t at line 313 - order unresolved
    Note over S,U: Source contains call t at line 321 - order unresolved
    Note over S,U: Source contains call t at line 326 - order unresolved
    Note over S,U: Source contains call t at line 328 - order unresolved
    Note over S,U: Source contains call Boolean at line 335 - order unresolved
    Note over S,U: Source contains call t at line 337 - order unresolved
    Note over S,U: Source contains call t at line 339 - order unresolved
    Note over S,U: Source contains call t at line 345 - order unresolved
    Note over S,U: Source contains call t at line 347 - order unresolved
    Note over S,U: Source contains call t at line 353 - order unresolved
    Note over S,U: Source contains call from at line 197 - order unresolved
    Note over S,U: Source contains call Set at line 198 - order unresolved
    Note over S,U: Source contains call map at line 200 - order unresolved
    Note over S,U: Source contains call filter at line 201 - order unresolved
    Note over S,U: Source contains call Boolean at line 201 - order unresolved
    Note over S,U: Source contains call URL at line 208 - order unresolved
    Note over S,U: Source contains call toLowerCase at line 208 - order unresolved
```
