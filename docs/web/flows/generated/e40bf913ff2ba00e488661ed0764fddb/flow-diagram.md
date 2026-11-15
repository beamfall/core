# Console remote access: certificate, relay, direct connection, watch\-away guide — flow-diagram

Source revision: 25a58b2a60b350f2a582cb407acbbbc52274a39b. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:786209b29f4d10a61de26299e4f95511 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/RemoteAccess\.tsx:62; symbol: CERT\_STATE; span SHA256: 34c6e6df39126b1ed055c8538ad882ae2190a0b5ad63d03e6e98e2ef9e0b8cab.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow e40bf913ff2ba00e488661ed0764fddb
    Note over S,U: Source contains call useT at line 143 - order unresolved
    Note over S,U: Source contains call t at line 147 - order unresolved
    Note over S,U: Source contains call useT at line 192 - order unresolved
    Note over S,U: Source contains call t at line 198 - order unresolved
    Note over S,U: Source contains call t at line 200 - order unresolved
    Note over S,U: Source contains call useT at line 322 - order unresolved
    Note over S,U: Source contains call useAsync at line 323 - order unresolved
    Note over S,U: Source contains call useDisplayLocale at line 324 - order unresolved
    Note over S,U: Source contains call connectionClassLabels at line 325 - order unresolved
    Note over S,U: Source contains call refresh at line 332 - order unresolved
    Note over S,U: Source contains call t at line 334 - order unresolved
    Note over S,U: Source contains call t at line 345 - order unresolved
    Note over S,U: Source contains call ListFormat at line 347 - order unresolved
    Note over S,U: Source contains call format at line 347 - order unresolved
    Note over S,U: Source contains call map at line 348 - order unresolved
    Note over S,U: Source contains call t at line 348 - order unresolved
    Note over S,U: Source contains call t at line 355 - order unresolved
    Note over S,U: Source contains call t at line 360 - order unresolved
    Note over S,U: Source contains call useT at line 224 - order unresolved
    Note over S,U: Source contains call find at line 226 - order unresolved
    Note over S,U: Source contains call currentEndpointKind at line 227 - order unresolved
    Note over S,U: Source contains call directState at line 233 - order unresolved
    Note over S,U: Source contains call t at line 239 - order unresolved
    Note over S,U: Source contains call t at line 240 - order unresolved
    Note over S,U: Source contains call t at line 249 - order unresolved
    Note over S,U: Source contains call t at line 252 - order unresolved
    Note over S,U: Source contains call t at line 254 - order unresolved
    Note over S,U: Source contains call t at line 260 - order unresolved
    Note over S,U: Source contains call t at line 265 - order unresolved
    Note over S,U: Source contains call refresh at line 268 - order unresolved
    Note over S,U: Source contains call t at line 270 - order unresolved
    Note over S,U: Source contains call t at line 278 - order unresolved
    Note over S,U: Source contains call t at line 283 - order unresolved
    Note over S,U: Source contains call t at line 285 - order unresolved
    Note over S,U: Source contains call Boolean at line 292 - order unresolved
    Note over S,U: Source contains call t at line 294 - order unresolved
    Note over S,U: Source contains call t at line 296 - order unresolved
    Note over S,U: Source contains call t at line 302 - order unresolved
    Note over S,U: Source contains call t at line 304 - order unresolved
    Note over S,U: Source contains call t at line 310 - order unresolved
    Note over S,U: Source contains call from at line 154 - order unresolved
    Note over S,U: Source contains call Set at line 155 - order unresolved
    Note over S,U: Source contains call map at line 157 - order unresolved
    Note over S,U: Source contains call filter at line 158 - order unresolved
    Note over S,U: Source contains call Boolean at line 158 - order unresolved
    Note over S,U: Source contains call URL at line 165 - order unresolved
    Note over S,U: Source contains call toLowerCase at line 165 - order unresolved
```
