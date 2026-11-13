# Manage curation: fields, relations and files of a Media — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:7f169a212dc9701d4b5063f9b7ce8da6 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/CurationPage\.tsx:64; symbol: CurationPage; span SHA256: 787a5d250ba7be37e9ed701cd3b7ee65a118836277f33067c79d3c7d8fe41370.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 3d8432e8a48564693b1c5b210720b534
    Note over S,U: Source contains call useT at line 65 - order unresolved
    Note over S,U: Source contains call useState at line 67 - order unresolved
    Note over S,U: Source contains call useState at line 69 - order unresolved
    Note over S,U: Source contains call useState at line 70 - order unresolved
    Note over S,U: Source contains call useT at line 184 - order unresolved
    Note over S,U: Source contains call t at line 187 - order unresolved
    Note over S,U: Source contains call t at line 192 - order unresolved
    Note over S,U: Source contains call t at line 198 - order unresolved
    Note over S,U: Source contains call t at line 203 - order unresolved
    Note over S,U: Source contains call t at line 204 - order unresolved
    Note over S,U: Source contains call t at line 205 - order unresolved
    Note over S,U: Source contains call map at line 209 - order unresolved
    Note over S,U: Source contains call useT at line 424 - order unresolved
    Note over S,U: Source contains call useState at line 427 - order unresolved
    Note over S,U: Source contains call t at line 431 - order unresolved
    Note over S,U: Source contains call t at line 432 - order unresolved
    Note over S,U: Source contains call useT at line 236 - order unresolved
    Note over S,U: Source contains call useState at line 237 - order unresolved
    Note over S,U: Source contains call useState at line 238 - order unresolved
    Note over S,U: Source contains call useState at line 239 - order unresolved
    Note over S,U: Source contains call t at line 243 - order unresolved
    Note over S,U: Source contains call t at line 244 - order unresolved
    Note over S,U: Source contains call async at line 72 - order unresolved
    Note over S,U: Source contains call setError at line 73 - order unresolved
    Note over S,U: Source contains call fetchEntity at line 75 - order unresolved
    Note over S,U: Source contains call setSubject at line 77 - order unresolved
    Note over S,U: Source contains call setLoaded at line 78 - order unresolved
    Note over S,U: Source contains call fetchEntityRelations at line 81 - order unresolved
    Note over S,U: Source contains call setSubject at line 82 - order unresolved
    Note over S,U: Source contains call setLoaded at line 92 - order unresolved
    Note over S,U: Source contains call all at line 96 - order unresolved
    Note over S,U: Source contains call fetchMediaFields at line 97 - order unresolved
    Note over S,U: Source contains call fetchMediaFiles at line 98 - order unresolved
    Note over S,U: Source contains call fetchMediaRelations at line 99 - order unresolved
    Note over S,U: Source contains call fetchMediaRelations at line 100 - order unresolved
    Note over S,U: Source contains call find at line 102 - order unresolved
    Note over S,U: Source contains call setSubject at line 103 - order unresolved
    Note over S,U: Source contains call setLoaded at line 113 - order unresolved
    Note over S,U: Source contains call async at line 251 - order unresolved
    Note over S,U: Source contains call onError at line 252 - order unresolved
    Note over S,U: Source contains call upsertEntityRelation at line 260 - order unresolved
    Note over S,U: Source contains call upsertEntityRelation at line 267 - order unresolved
    Note over S,U: Source contains call onChange at line 274 - order unresolved
    Note over S,U: Source contains call onError at line 276 - order unresolved
    Note over S,U: Source contains call t at line 276 - order unresolved
    Note over S,U: Source contains call async at line 280 - order unresolved
    Note over S,U: Source contains call onError at line 281 - order unresolved
    Note over S,U: Source contains call trim at line 282 - order unresolved
    Note over S,U: Source contains call trim at line 283 - order unresolved
    Note over S,U: Source contains call trim at line 285 - order unresolved
    Note over S,U: Source contains call Number at line 285 - order unresolved
    Note over S,U: Source contains call upsertEntityRelation at line 287 - order unresolved
    Note over S,U: Source contains call setTargetID at line 293 - order unresolved
    Note over S,U: Source contains call setOrder at line 294 - order unresolved
    Note over S,U: Source contains call onChange at line 295 - order unresolved
    Note over S,U: Source contains call onError at line 297 - order unresolved
    Note over S,U: Source contains call t at line 297 - order unresolved
    Note over S,U: Source contains call async at line 437 - order unresolved
    Note over S,U: Source contains call onError at line 438 - order unresolved
    Note over S,U: Source contains call trim at line 439 - order unresolved
    Note over S,U: Source contains call linkMediaRelation at line 442 - order unresolved
    Note over S,U: Source contains call setExtraID at line 443 - order unresolved
    Note over S,U: Source contains call onChange at line 444 - order unresolved
    Note over S,U: Source contains call onError at line 446 - order unresolved
    Note over S,U: Source contains call t at line 446 - order unresolved
    Note over S,U: Source contains call async at line 116 - order unresolved
    Note over S,U: Source contains call trim at line 117 - order unresolved
    Note over S,U: Source contains call load at line 120 - order unresolved
    Note over S,U: Source contains call setSubject at line 122 - order unresolved
    Note over S,U: Source contains call setLoaded at line 123 - order unresolved
    Note over S,U: Source contains call setError at line 124 - order unresolved
    Note over S,U: Source contains call t at line 124 - order unresolved
    Note over S,U: Source contains call load at line 130 - order unresolved
    Note over S,U: Source contains call catch at line 130 - order unresolved
    Note over S,U: Source contains call setError at line 130 - order unresolved
    Note over S,U: Source contains call t at line 130 - order unresolved
    Note over S,U: Source contains call t at line 303 - order unresolved
    Note over S,U: Source contains call t at line 305 - order unresolved
```
