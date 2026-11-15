# Rescan a library or all libraries and follow scan state — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

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
    Note over S,U: Source contains call useT at line 222 - order unresolved
    Note over S,U: Source contains call useAnyRescanActive at line 223 - order unresolved
    Note over S,U: Source contains call useEffect at line 224 - order unresolved
    Note over S,U: Source contains call ensurePolling at line 225 - order unresolved
    Note over S,U: Source contains call filter at line 227 - order unresolved
    Note over S,U: Source contains call startRescan at line 235 - order unresolved
    Note over S,U: Source contains call t at line 239 - order unresolved
    Note over S,U: Source contains call useT at line 197 - order unresolved
    Note over S,U: Source contains call useRescanState at line 198 - order unresolved
    Note over S,U: Source contains call has at line 199 - order unresolved
    Note over S,U: Source contains call t at line 205 - order unresolved
    Note over S,U: Source contains call startRescan at line 206 - order unresolved
    Note over S,U: Source contains call t at line 209 - order unresolved
    Note over S,U: Source contains call useT at line 246 - order unresolved
    Note over S,U: Source contains call useRescanState at line 247 - order unresolved
    Note over S,U: Source contains call useEffect at line 248 - order unresolved
    Note over S,U: Source contains call ensurePolling at line 249 - order unresolved
    Note over S,U: Source contains call t at line 253 - order unresolved
    Note over S,U: Source contains call find at line 256 - order unresolved
    Note over S,U: Source contains call jobProgressDetail at line 258 - order unresolved
    Note over S,U: Source contains call doneDetail at line 260 - order unresolved
    Note over S,U: Source contains call t at line 262 - order unresolved
    Note over S,U: Source contains call t at line 267 - order unresolved
    Note over S,U: Source contains call t at line 269 - order unresolved
    Note over S,U: Source contains call t at line 271 - order unresolved
    Note over S,U: Source contains call t at line 273 - order unresolved
    Note over S,U: Source contains call listener at line 43 - order unresolved
    Note over S,U: Source contains call setInterval at line 113 - order unresolved
    Note over S,U: Source contains call poll at line 113 - order unresolved
    Note over S,U: Source contains call emit at line 117 - order unresolved
    Note over S,U: Source contains call poll at line 118 - order unresolved
    Note over S,U: Source contains call phaseFor at line 70 - order unresolved
    Note over S,U: Source contains call some at line 61 - order unresolved
    Note over S,U: Source contains call has at line 61 - order unresolved
    Note over S,U: Source contains call find at line 62 - order unresolved
    Note over S,U: Source contains call some at line 64 - order unresolved
    Note over S,U: Source contains call entries at line 80 - order unresolved
    Note over S,U: Source contains call filter at line 80 - order unresolved
    Note over S,U: Source contains call clearInterval at line 82 - order unresolved
    Note over S,U: Source contains call fetchJobs at line 88 - order unresolved
    Note over S,U: Source contains call isAuthRejection at line 90 - order unresolved
    Note over S,U: Source contains call clearInterval at line 91 - order unresolved
    Note over S,U: Source contains call emit at line 94 - order unresolved
    Note over S,U: Source contains call Map at line 98 - order unresolved
    Note over S,U: Source contains call map at line 98 - order unresolved
    Note over S,U: Source contains call get at line 101 - order unresolved
    Note over S,U: Source contains call map at line 104 - order unresolved
    Note over S,U: Source contains call get at line 106 - order unresolved
    Note over S,U: Source contains call has at line 106 - order unresolved
    Note over S,U: Source contains call setState at line 108 - order unresolved
    Note over S,U: Source contains call foldRescan at line 108 - order unresolved
    Note over S,U: Source contains call get at line 52 - order unresolved
    Note over S,U: Source contains call clear at line 181 - order unresolved
    Note over S,U: Source contains call clearInterval at line 182 - order unresolved
    Note over S,U: Source contains call emit at line 185 - order unresolved
    Note over S,U: Source contains call set at line 47 - order unresolved
    Note over S,U: Source contains call emit at line 48 - order unresolved
    Note over S,U: Source contains call add at line 163 - order unresolved
    Note over S,U: Source contains call delete at line 164 - order unresolved
    Note over S,U: Source contains call useSyncExternalStore at line 173 - order unresolved
    Note over S,U: Source contains call values at line 175 - order unresolved
    Note over S,U: Source contains call some at line 175 - order unresolved
    Note over S,U: Source contains call has at line 175 - order unresolved
    Note over S,U: Source contains call useSyncExternalStore at line 168 - order unresolved
    Note over S,U: Source contains call rescanState at line 168 - order unresolved
```
