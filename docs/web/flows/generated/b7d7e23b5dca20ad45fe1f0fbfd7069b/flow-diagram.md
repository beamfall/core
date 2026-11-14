# Manage markers: segment edit, bulk delete, trashed markers — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:7d953f859a790e0b580890ea7829b735 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:597; symbol: BulkDeleteConfirm; span SHA256: 06875e3212b9669e3dc36e7164f0f7193644d51ad12ab79d317d837f8da0d044.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow b7d7e23b5dca20ad45fe1f0fbfd7069b
    Note over S,U: Source contains call useT at line 606 - order unresolved
    Note over S,U: Source contains call t at line 616 - order unresolved
    Note over S,U: Source contains call t at line 621 - order unresolved
    Note over S,U: Source contains call t at line 622 - order unresolved
    Note over S,U: Source contains call t at line 625 - order unresolved
    Note over S,U: Source contains call t at line 630 - order unresolved
    Note over S,U: Source contains call useT at line 471 - order unresolved
    Note over S,U: Source contains call useState at line 474 - order unresolved
    Note over S,U: Source contains call useState at line 475 - order unresolved
    Note over S,U: Source contains call useT at line 432 - order unresolved
    Note over S,U: Source contains call t at line 437 - order unresolved
    Note over S,U: Source contains call t at line 442 - order unresolved
    Note over S,U: Source contains call t at line 443 - order unresolved
    Note over S,U: Source contains call t at line 446 - order unresolved
    Note over S,U: Source contains call t at line 451 - order unresolved
    Note over S,U: Source contains call useT at line 232 - order unresolved
    Note over S,U: Source contains call toDraft at line 233 - order unresolved
    Note over S,U: Source contains call useState at line 234 - order unresolved
    Note over S,U: Source contains call useEffect at line 238 - order unresolved
    Note over S,U: Source contains call setDraft at line 239 - order unresolved
    Note over S,U: Source contains call toDraft at line 239 - order unresolved
    Note over S,U: Source contains call useT at line 84 - order unresolved
    Note over S,U: Source contains call useState at line 85 - order unresolved
    Note over S,U: Source contains call useState at line 86 - order unresolved
    Note over S,U: Source contains call Set at line 89 - order unresolved
    Note over S,U: Source contains call useState at line 90 - order unresolved
    Note over S,U: Source contains call useState at line 91 - order unresolved
    Note over S,U: Source contains call useCallback at line 93 - order unresolved
    Note over S,U: Source contains call async at line 94 - order unresolved
    Note over S,U: Source contains call setError at line 95 - order unresolved
    Note over S,U: Source contains call trim at line 96 - order unresolved
    Note over S,U: Source contains call all at line 99 - order unresolved
    Note over S,U: Source contains call fetchMarkers at line 99 - order unresolved
    Note over S,U: Source contains call fetchTrashedMarkers at line 99 - order unresolved
    Note over S,U: Source contains call setMediaID at line 100 - order unresolved
    Note over S,U: Source contains call setMarkers at line 101 - order unresolved
    Note over S,U: Source contains call sort at line 101 - order unresolved
    Note over S,U: Source contains call setTrashed at line 102 - order unresolved
    Note over S,U: Source contains call setSelected at line 103 - order unresolved
    Note over S,U: Source contains call Set at line 103 - order unresolved
    Note over S,U: Source contains call setLoaded at line 104 - order unresolved
    Note over S,U: Source contains call setError at line 106 - order unresolved
    Note over S,U: Source contains call t at line 106 - order unresolved
    Note over S,U: Source contains call useT at line 650 - order unresolved
    Note over S,U: Source contains call localeCompare at line 80 - order unresolved
    Note over S,U: Source contains call async at line 504 - order unresolved
    Note over S,U: Source contains call onError at line 505 - order unresolved
    Note over S,U: Source contains call all at line 508 - order unresolved
    Note over S,U: Source contains call map at line 508 - order unresolved
    Note over S,U: Source contains call deleteMarker at line 508 - order unresolved
    Note over S,U: Source contains call onApplied at line 509 - order unresolved
    Note over S,U: Source contains call onError at line 511 - order unresolved
    Note over S,U: Source contains call t at line 511 - order unresolved
    Note over S,U: Source contains call async at line 115 - order unresolved
    Note over S,U: Source contains call setError at line 117 - order unresolved
    Note over S,U: Source contains call createMarker at line 119 - order unresolved
    Note over S,U: Source contains call blankDraft at line 119 - order unresolved
    Note over S,U: Source contains call load at line 120 - order unresolved
    Note over S,U: Source contains call setError at line 122 - order unresolved
    Note over S,U: Source contains call t at line 122 - order unresolved
    Note over S,U: Source contains call async at line 282 - order unresolved
    Note over S,U: Source contains call onError at line 283 - order unresolved
    Note over S,U: Source contains call deleteMarker at line 285 - order unresolved
    Note over S,U: Source contains call onSaved at line 286 - order unresolved
    Note over S,U: Source contains call onError at line 288 - order unresolved
    Note over S,U: Source contains call t at line 288 - order unresolved
    Note over S,U: Source contains call load at line 112 - order unresolved
    Note over S,U: Source contains call async at line 652 - order unresolved
    Note over S,U: Source contains call onError at line 653 - order unresolved
    Note over S,U: Source contains call restoreMarker at line 655 - order unresolved
    Note over S,U: Source contains call onRestored at line 656 - order unresolved
    Note over S,U: Source contains call onError at line 658 - order unresolved
    Note over S,U: Source contains call t at line 658 - order unresolved
    Note over S,U: Source contains call async at line 486 - order unresolved
    Note over S,U: Source contains call onError at line 487 - order unresolved
    Note over S,U: Source contains call all at line 490 - order unresolved
    Note over S,U: Source contains call map at line 491 - order unresolved
    Note over S,U: Source contains call toDraft at line 492 - order unresolved
    Note over S,U: Source contains call Set at line 493 - order unresolved
    Note over S,U: Source contains call updateMarker at line 494 - order unresolved
    Note over S,U: Source contains call setApplied at line 497 - order unresolved
    Note over S,U: Source contains call onApplied at line 498 - order unresolved
    Note over S,U: Source contains call onError at line 500 - order unresolved
    Note over S,U: Source contains call t at line 500 - order unresolved
    Note over S,U: Source contains call async at line 254 - order unresolved
    Note over S,U: Source contains call onError at line 255 - order unresolved
    Note over S,U: Source contains call updateMarker at line 257 - order unresolved
    Note over S,U: Source contains call onSaved at line 258 - order unresolved
    Note over S,U: Source contains call onError at line 260 - order unresolved
    Note over S,U: Source contains call t at line 260 - order unresolved
    Note over S,U: Source contains call useCallback at line 126 - order unresolved
    Note over S,U: Source contains call load at line 126 - order unresolved
    Note over S,U: Source contains call useCallback at line 113 - order unresolved
    Note over S,U: Source contains call load at line 113 - order unresolved
    Note over S,U: Source contains call set at line 252 - order unresolved
    Note over S,U: Source contains call filter at line 252 - order unresolved
    Note over S,U: Source contains call setDraft at line 242 - order unresolved
    Note over S,U: Source contains call async at line 266 - order unresolved
    Note over S,U: Source contains call onError at line 267 - order unresolved
    Note over S,U: Source contains call all at line 269 - order unresolved
    Note over S,U: Source contains call updateMarker at line 270 - order unresolved
    Note over S,U: Source contains call updateMarker at line 271 - order unresolved
    Note over S,U: Source contains call toDraft at line 272 - order unresolved
    Note over S,U: Source contains call onSaved at line 276 - order unresolved
    Note over S,U: Source contains call onError at line 278 - order unresolved
    Note over S,U: Source contains call t at line 278 - order unresolved
    Note over S,U: Source contains call setSelected at line 129 - order unresolved
    Note over S,U: Source contains call Set at line 130 - order unresolved
    Note over S,U: Source contains call add at line 131 - order unresolved
    Note over S,U: Source contains call delete at line 132 - order unresolved
```
