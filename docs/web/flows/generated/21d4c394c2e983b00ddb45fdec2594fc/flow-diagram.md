# Rescan a library or all libraries and follow scan state — flow-diagram

Source revision: 03e521f81ef085ec14c6d7d5d76533692ecba36c. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:b04e658d96258cd9a9a4a89055b5618b -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/components/beamfall/LibraryRescan\.tsx:28; symbol: ACTIVE; span SHA256: e102279ed89eede4d9a82b5ea48e0c3014f5e434dc686556347bc33a19bc7aeb.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 21d4c394c2e983b00ddb45fdec2594fc
    Note over S,U: Source contains call Set at line 28 - order unresolved
    Note over S,U: Source contains call useT at line 214 - order unresolved
    Note over S,U: Source contains call useAnyRescanActive at line 215 - order unresolved
    Note over S,U: Source contains call useEffect at line 216 - order unresolved
    Note over S,U: Source contains call ensurePolling at line 217 - order unresolved
    Note over S,U: Source contains call filter at line 219 - order unresolved
    Note over S,U: Source contains call startRescan at line 227 - order unresolved
    Note over S,U: Source contains call t at line 231 - order unresolved
    Note over S,U: Source contains call useT at line 189 - order unresolved
    Note over S,U: Source contains call useRescanState at line 190 - order unresolved
    Note over S,U: Source contains call has at line 191 - order unresolved
    Note over S,U: Source contains call t at line 197 - order unresolved
    Note over S,U: Source contains call startRescan at line 198 - order unresolved
    Note over S,U: Source contains call t at line 201 - order unresolved
    Note over S,U: Source contains call useT at line 238 - order unresolved
    Note over S,U: Source contains call useRescanState at line 239 - order unresolved
    Note over S,U: Source contains call useEffect at line 240 - order unresolved
    Note over S,U: Source contains call ensurePolling at line 241 - order unresolved
    Note over S,U: Source contains call t at line 245 - order unresolved
    Note over S,U: Source contains call find at line 248 - order unresolved
    Note over S,U: Source contains call jobProgressDetail at line 249 - order unresolved
    Note over S,U: Source contains call t at line 253 - order unresolved
    Note over S,U: Source contains call t at line 255 - order unresolved
    Note over S,U: Source contains call t at line 257 - order unresolved
    Note over S,U: Source contains call listener at line 41 - order unresolved
    Note over S,U: Source contains call setInterval at line 105 - order unresolved
    Note over S,U: Source contains call poll at line 105 - order unresolved
    Note over S,U: Source contains call emit at line 109 - order unresolved
    Note over S,U: Source contains call poll at line 110 - order unresolved
    Note over S,U: Source contains call phaseFor at line 63 - order unresolved
    Note over S,U: Source contains call some at line 55 - order unresolved
    Note over S,U: Source contains call has at line 55 - order unresolved
    Note over S,U: Source contains call find at line 56 - order unresolved
    Note over S,U: Source contains call entries at line 73 - order unresolved
    Note over S,U: Source contains call filter at line 73 - order unresolved
    Note over S,U: Source contains call clearInterval at line 75 - order unresolved
    Note over S,U: Source contains call fetchJobs at line 81 - order unresolved
    Note over S,U: Source contains call isAuthRejection at line 83 - order unresolved
    Note over S,U: Source contains call clearInterval at line 84 - order unresolved
    Note over S,U: Source contains call emit at line 87 - order unresolved
    Note over S,U: Source contains call Map at line 91 - order unresolved
    Note over S,U: Source contains call map at line 91 - order unresolved
    Note over S,U: Source contains call get at line 94 - order unresolved
    Note over S,U: Source contains call map at line 96 - order unresolved
    Note over S,U: Source contains call get at line 98 - order unresolved
    Note over S,U: Source contains call has at line 98 - order unresolved
    Note over S,U: Source contains call setState at line 100 - order unresolved
    Note over S,U: Source contains call foldRescan at line 100 - order unresolved
    Note over S,U: Source contains call get at line 50 - order unresolved
    Note over S,U: Source contains call clear at line 173 - order unresolved
    Note over S,U: Source contains call clearInterval at line 174 - order unresolved
    Note over S,U: Source contains call emit at line 177 - order unresolved
    Note over S,U: Source contains call set at line 45 - order unresolved
    Note over S,U: Source contains call emit at line 46 - order unresolved
    Note over S,U: Source contains call add at line 155 - order unresolved
    Note over S,U: Source contains call delete at line 156 - order unresolved
    Note over S,U: Source contains call useSyncExternalStore at line 165 - order unresolved
    Note over S,U: Source contains call values at line 167 - order unresolved
    Note over S,U: Source contains call some at line 167 - order unresolved
    Note over S,U: Source contains call has at line 167 - order unresolved
    Note over S,U: Source contains call useSyncExternalStore at line 160 - order unresolved
    Note over S,U: Source contains call rescanState at line 160 - order unresolved
```
