# Viewer My Data / privacy: summary, event log, sessions, stream tokens, export — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:acc362561c516ccec31539cb78cf4958 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/pages/MyDataPage\.tsx:46; symbol: ALL\_LIBRARIES; span SHA256: eb3f07ba74a0fc603288f92ab1ebcf39cb6f259d1a28387ee36024bb03f48ee7.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow f8d45946aa49284f1b86ebef0c980b98
    Note over S,U: Source contains call useT at line 521 - order unresolved
    Note over S,U: Source contains call t at line 524 - order unresolved
    Note over S,U: Source contains call t at line 526 - order unresolved
    Note over S,U: Source contains call t at line 527 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 537 - order unresolved
    Note over S,U: Source contains call useT at line 428 - order unresolved
    Note over S,U: Source contains call t at line 436 - order unresolved
    Note over S,U: Source contains call t at line 439 - order unresolved
    Note over S,U: Source contains call t at line 445 - order unresolved
    Note over S,U: Source contains call map at line 446 - order unresolved
    Note over S,U: Source contains call t at line 455 - order unresolved
    Note over S,U: Source contains call onFromChange at line 460 - order unresolved
    Note over S,U: Source contains call t at line 465 - order unresolved
    Note over S,U: Source contains call onToChange at line 470 - order unresolved
    Note over S,U: Source contains call t at line 477 - order unresolved
    Note over S,U: Source contains call t at line 480 - order unresolved
    Note over S,U: Source contains call t at line 488 - order unresolved
    Note over S,U: Source contains call map at line 491 - order unresolved
    Note over S,U: Source contains call t at line 499 - order unresolved
    Note over S,U: Source contains call t at line 508 - order unresolved
    Note over S,U: Source contains call t at line 511 - order unresolved
    Note over S,U: Source contains call useT at line 56 - order unresolved
    Note over S,U: Source contains call useState at line 82 - order unresolved
    Note over S,U: Source contains call useT at line 544 - order unresolved
    Note over S,U: Source contains call push at line 546 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 547 - order unresolved
    Note over S,U: Source contains call push at line 548 - order unresolved
    Note over S,U: Source contains call join at line 556 - order unresolved
    Note over S,U: Source contains call t at line 559 - order unresolved
    Note over S,U: Source contains call useT at line 335 - order unresolved
    Note over S,U: Source contains call t at line 338 - order unresolved
    Note over S,U: Source contains call t at line 339 - order unresolved
    Note over S,U: Source contains call t at line 340 - order unresolved
    Note over S,U: Source contains call t at line 341 - order unresolved
    Note over S,U: Source contains call t at line 342 - order unresolved
    Note over S,U: Source contains call max at line 345 - order unresolved
    Note over S,U: Source contains call map at line 345 - order unresolved
    Note over S,U: Source contains call t at line 347 - order unresolved
    Note over S,U: Source contains call map at line 352 - order unresolved
    Note over S,U: Source contains call toLocaleString at line 354 - order unresolved
    Note over S,U: Source contains call t at line 361 - order unresolved
    Note over S,U: Source contains call formatDate at line 362 - order unresolved
    Note over S,U: Source contains call formatDate at line 363 - order unresolved
    Note over S,U: Source contains call t at line 365 - order unresolved
    Note over S,U: Source contains call t at line 369 - order unresolved
    Note over S,U: Source contains call map at line 371 - order unresolved
    Note over S,U: Source contains call useT at line 565 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 566 - order unresolved
    Note over S,U: Source contains call t at line 574 - order unresolved
    Note over S,U: Source contains call stringify at line 584 - order unresolved
    Note over S,U: Source contains call Blob at line 585 - order unresolved
    Note over S,U: Source contains call createObjectURL at line 586 - order unresolved
    Note over S,U: Source contains call createElement at line 587 - order unresolved
    Note over S,U: Source contains call click at line 590 - order unresolved
    Note over S,U: Source contains call revokeObjectURL at line 591 - order unresolved
    Note over S,U: Source contains call Date at line 606 - order unresolved
    Note over S,U: Source contains call isNaN at line 607 - order unresolved
    Note over S,U: Source contains call getTime at line 607 - order unresolved
    Note over S,U: Source contains call toLocaleDateString at line 608 - order unresolved
    Note over S,U: Source contains call Date at line 598 - order unresolved
    Note over S,U: Source contains call isNaN at line 599 - order unresolved
    Note over S,U: Source contains call getTime at line 599 - order unresolved
    Note over S,U: Source contains call toLocaleString at line 600 - order unresolved
    Note over S,U: Source contains call setApplied at line 134 - order unresolved
    Note over S,U: Source contains call setOffset at line 135 - order unresolved
    Note over S,U: Source contains call setDraftLibrary at line 139 - order unresolved
    Note over S,U: Source contains call setDraftFrom at line 140 - order unresolved
    Note over S,U: Source contains call setDraftTo at line 141 - order unresolved
    Note over S,U: Source contains call setApplied at line 142 - order unresolved
    Note over S,U: Source contains call setOffset at line 143 - order unresolved
    Note over S,U: Source contains call setDeleteError at line 159 - order unresolved
    Note over S,U: Source contains call setDeleteMsg at line 160 - order unresolved
    Note over S,U: Source contains call confirm at line 163 - order unresolved
    Note over S,U: Source contains call t at line 163 - order unresolved
    Note over S,U: Source contains call deleteMyEngagement at line 167 - order unresolved
    Note over S,U: Source contains call setDeleteMsg at line 168 - order unresolved
    Note over S,U: Source contains call t at line 168 - order unresolved
    Note over S,U: Source contains call all at line 169 - order unresolved
    Note over S,U: Source contains call refresh at line 169 - order unresolved
    Note over S,U: Source contains call refreshSummary at line 169 - order unresolved
    Note over S,U: Source contains call refreshLog at line 169 - order unresolved
    Note over S,U: Source contains call toLowerCase at line 173 - order unresolved
    Note over S,U: Source contains call includes at line 173 - order unresolved
    Note over S,U: Source contains call prompt at line 175 - order unresolved
    Note over S,U: Source contains call t at line 175 - order unresolved
    Note over S,U: Source contains call setDeleteError at line 177 - order unresolved
    Note over S,U: Source contains call t at line 177 - order unresolved
    Note over S,U: Source contains call deleteMyEngagement at line 181 - order unresolved
    Note over S,U: Source contains call setDeleteMsg at line 182 - order unresolved
    Note over S,U: Source contains call t at line 182 - order unresolved
    Note over S,U: Source contains call all at line 183 - order unresolved
    Note over S,U: Source contains call refresh at line 183 - order unresolved
    Note over S,U: Source contains call refreshSummary at line 183 - order unresolved
    Note over S,U: Source contains call refreshLog at line 183 - order unresolved
    Note over S,U: Source contains call setDeleteError at line 185 - order unresolved
    Note over S,U: Source contains call t at line 185 - order unresolved
    Note over S,U: Source contains call setExportError at line 147 - order unresolved
    Note over S,U: Source contains call setExportMsg at line 148 - order unresolved
    Note over S,U: Source contains call fetchMyEngagement at line 150 - order unresolved
    Note over S,U: Source contains call downloadJSON at line 151 - order unresolved
    Note over S,U: Source contains call setExportMsg at line 152 - order unresolved
    Note over S,U: Source contains call t at line 152 - order unresolved
    Note over S,U: Source contains call setExportError at line 154 - order unresolved
    Note over S,U: Source contains call t at line 154 - order unresolved
    Note over S,U: Source contains call useCallback at line 84 - order unresolved
    Note over S,U: Source contains call async at line 84 - order unresolved
    Note over S,U: Source contains call setSessionsError at line 85 - order unresolved
    Note over S,U: Source contains call setInfo at line 87 - order unresolved
    Note over S,U: Source contains call fetchMySessions at line 87 - order unresolved
    Note over S,U: Source contains call setSessionsError at line 89 - order unresolved
    Note over S,U: Source contains call t at line 89 - order unresolved
    Note over S,U: Source contains call useCallback at line 102 - order unresolved
    Note over S,U: Source contains call async at line 102 - order unresolved
    Note over S,U: Source contains call setLogError at line 103 - order unresolved
    Note over S,U: Source contains call setLogPage at line 105 - order unresolved
    Note over S,U: Source contains call fetchMyEngagementLog at line 106 - order unresolved
    Note over S,U: Source contains call setLogError at line 113 - order unresolved
    Note over S,U: Source contains call t at line 113 - order unresolved
    Note over S,U: Source contains call useCallback at line 93 - order unresolved
    Note over S,U: Source contains call async at line 93 - order unresolved
    Note over S,U: Source contains call setSummaryError at line 94 - order unresolved
    Note over S,U: Source contains call setSummary at line 96 - order unresolved
    Note over S,U: Source contains call fetchMyDataSummary at line 96 - order unresolved
    Note over S,U: Source contains call setSummaryError at line 98 - order unresolved
    Note over S,U: Source contains call t at line 98 - order unresolved
```
