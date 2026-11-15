# Viewer My Data / privacy: summary, event log, sessions, stream tokens, export — flow-diagram

Source revision: b952880cf4519beaa9738e25e349aabf600c9a41. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:acc362561c516ccec31539cb78cf4958 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/pages/MyDataPage\.tsx:48; symbol: ALL\_LIBRARIES; span SHA256: eb3f07ba74a0fc603288f92ab1ebcf39cb6f259d1a28387ee36024bb03f48ee7.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow f8d45946aa49284f1b86ebef0c980b98
    Note over S,U: Source contains call useT at line 491 - order unresolved
    Note over S,U: Source contains call t at line 494 - order unresolved
    Note over S,U: Source contains call t at line 496 - order unresolved
    Note over S,U: Source contains call t at line 497 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 507 - order unresolved
    Note over S,U: Source contains call useT at line 398 - order unresolved
    Note over S,U: Source contains call t at line 406 - order unresolved
    Note over S,U: Source contains call t at line 409 - order unresolved
    Note over S,U: Source contains call t at line 415 - order unresolved
    Note over S,U: Source contains call map at line 416 - order unresolved
    Note over S,U: Source contains call t at line 425 - order unresolved
    Note over S,U: Source contains call onFromChange at line 430 - order unresolved
    Note over S,U: Source contains call t at line 435 - order unresolved
    Note over S,U: Source contains call onToChange at line 440 - order unresolved
    Note over S,U: Source contains call t at line 447 - order unresolved
    Note over S,U: Source contains call t at line 450 - order unresolved
    Note over S,U: Source contains call t at line 458 - order unresolved
    Note over S,U: Source contains call map at line 461 - order unresolved
    Note over S,U: Source contains call t at line 469 - order unresolved
    Note over S,U: Source contains call t at line 478 - order unresolved
    Note over S,U: Source contains call t at line 481 - order unresolved
    Note over S,U: Source contains call useT at line 58 - order unresolved
    Note over S,U: Source contains call useState at line 84 - order unresolved
    Note over S,U: Source contains call useT at line 514 - order unresolved
    Note over S,U: Source contains call push at line 516 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 517 - order unresolved
    Note over S,U: Source contains call push at line 518 - order unresolved
    Note over S,U: Source contains call join at line 526 - order unresolved
    Note over S,U: Source contains call t at line 529 - order unresolved
    Note over S,U: Source contains call useT at line 305 - order unresolved
    Note over S,U: Source contains call t at line 308 - order unresolved
    Note over S,U: Source contains call t at line 309 - order unresolved
    Note over S,U: Source contains call t at line 310 - order unresolved
    Note over S,U: Source contains call t at line 311 - order unresolved
    Note over S,U: Source contains call t at line 312 - order unresolved
    Note over S,U: Source contains call max at line 315 - order unresolved
    Note over S,U: Source contains call map at line 315 - order unresolved
    Note over S,U: Source contains call t at line 317 - order unresolved
    Note over S,U: Source contains call map at line 322 - order unresolved
    Note over S,U: Source contains call toLocaleString at line 324 - order unresolved
    Note over S,U: Source contains call t at line 331 - order unresolved
    Note over S,U: Source contains call formatDate at line 332 - order unresolved
    Note over S,U: Source contains call formatDate at line 333 - order unresolved
    Note over S,U: Source contains call t at line 335 - order unresolved
    Note over S,U: Source contains call t at line 339 - order unresolved
    Note over S,U: Source contains call map at line 341 - order unresolved
    Note over S,U: Source contains call useT at line 535 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 536 - order unresolved
    Note over S,U: Source contains call t at line 544 - order unresolved
    Note over S,U: Source contains call stringify at line 554 - order unresolved
    Note over S,U: Source contains call Blob at line 555 - order unresolved
    Note over S,U: Source contains call createObjectURL at line 556 - order unresolved
    Note over S,U: Source contains call createElement at line 557 - order unresolved
    Note over S,U: Source contains call click at line 560 - order unresolved
    Note over S,U: Source contains call revokeObjectURL at line 561 - order unresolved
    Note over S,U: Source contains call Date at line 576 - order unresolved
    Note over S,U: Source contains call isNaN at line 577 - order unresolved
    Note over S,U: Source contains call getTime at line 577 - order unresolved
    Note over S,U: Source contains call toLocaleDateString at line 578 - order unresolved
    Note over S,U: Source contains call Date at line 568 - order unresolved
    Note over S,U: Source contains call isNaN at line 569 - order unresolved
    Note over S,U: Source contains call getTime at line 569 - order unresolved
    Note over S,U: Source contains call toLocaleString at line 570 - order unresolved
    Note over S,U: Source contains call setApplied at line 136 - order unresolved
    Note over S,U: Source contains call setOffset at line 137 - order unresolved
    Note over S,U: Source contains call setDraftLibrary at line 141 - order unresolved
    Note over S,U: Source contains call setDraftFrom at line 142 - order unresolved
    Note over S,U: Source contains call setDraftTo at line 143 - order unresolved
    Note over S,U: Source contains call setApplied at line 144 - order unresolved
    Note over S,U: Source contains call setOffset at line 145 - order unresolved
    Note over S,U: Source contains call setDeleteError at line 161 - order unresolved
    Note over S,U: Source contains call setDeleteMsg at line 162 - order unresolved
    Note over S,U: Source contains call confirm at line 165 - order unresolved
    Note over S,U: Source contains call t at line 165 - order unresolved
    Note over S,U: Source contains call deleteMyEngagement at line 169 - order unresolved
    Note over S,U: Source contains call setDeleteMsg at line 170 - order unresolved
    Note over S,U: Source contains call t at line 170 - order unresolved
    Note over S,U: Source contains call all at line 171 - order unresolved
    Note over S,U: Source contains call refresh at line 171 - order unresolved
    Note over S,U: Source contains call refreshSummary at line 171 - order unresolved
    Note over S,U: Source contains call refreshLog at line 171 - order unresolved
    Note over S,U: Source contains call toLowerCase at line 175 - order unresolved
    Note over S,U: Source contains call includes at line 175 - order unresolved
    Note over S,U: Source contains call prompt at line 177 - order unresolved
    Note over S,U: Source contains call t at line 177 - order unresolved
    Note over S,U: Source contains call setDeleteError at line 179 - order unresolved
    Note over S,U: Source contains call t at line 179 - order unresolved
    Note over S,U: Source contains call deleteMyEngagement at line 183 - order unresolved
    Note over S,U: Source contains call setDeleteMsg at line 184 - order unresolved
    Note over S,U: Source contains call t at line 184 - order unresolved
    Note over S,U: Source contains call all at line 185 - order unresolved
    Note over S,U: Source contains call refresh at line 185 - order unresolved
    Note over S,U: Source contains call refreshSummary at line 185 - order unresolved
    Note over S,U: Source contains call refreshLog at line 185 - order unresolved
    Note over S,U: Source contains call setDeleteError at line 187 - order unresolved
    Note over S,U: Source contains call t at line 187 - order unresolved
    Note over S,U: Source contains call setExportError at line 149 - order unresolved
    Note over S,U: Source contains call setExportMsg at line 150 - order unresolved
    Note over S,U: Source contains call fetchMyEngagement at line 152 - order unresolved
    Note over S,U: Source contains call downloadJSON at line 153 - order unresolved
    Note over S,U: Source contains call setExportMsg at line 154 - order unresolved
    Note over S,U: Source contains call t at line 154 - order unresolved
    Note over S,U: Source contains call setExportError at line 156 - order unresolved
    Note over S,U: Source contains call t at line 156 - order unresolved
    Note over S,U: Source contains call useCallback at line 86 - order unresolved
    Note over S,U: Source contains call async at line 86 - order unresolved
    Note over S,U: Source contains call setSessionsError at line 87 - order unresolved
    Note over S,U: Source contains call setInfo at line 89 - order unresolved
    Note over S,U: Source contains call fetchMySessions at line 89 - order unresolved
    Note over S,U: Source contains call setSessionsError at line 91 - order unresolved
    Note over S,U: Source contains call t at line 91 - order unresolved
    Note over S,U: Source contains call useCallback at line 104 - order unresolved
    Note over S,U: Source contains call async at line 104 - order unresolved
    Note over S,U: Source contains call setLogError at line 105 - order unresolved
    Note over S,U: Source contains call setLogPage at line 107 - order unresolved
    Note over S,U: Source contains call fetchMyEngagementLog at line 108 - order unresolved
    Note over S,U: Source contains call setLogError at line 115 - order unresolved
    Note over S,U: Source contains call t at line 115 - order unresolved
    Note over S,U: Source contains call useCallback at line 95 - order unresolved
    Note over S,U: Source contains call async at line 95 - order unresolved
    Note over S,U: Source contains call setSummaryError at line 96 - order unresolved
    Note over S,U: Source contains call setSummary at line 98 - order unresolved
    Note over S,U: Source contains call fetchMyDataSummary at line 98 - order unresolved
    Note over S,U: Source contains call setSummaryError at line 100 - order unresolved
    Note over S,U: Source contains call t at line 100 - order unresolved
```
