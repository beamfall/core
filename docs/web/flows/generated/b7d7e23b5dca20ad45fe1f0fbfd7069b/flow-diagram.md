# Manage markers: segment edit, bulk delete, trashed markers — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:7d953f859a790e0b580890ea7829b735 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/MarkersPage\.tsx:82; symbol: MarkersPage; span SHA256: 3e9ce0a28fcc6badcd8c28d735a9f7bbc501cf39799cbb04f6777bd0adf280b7.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow b7d7e23b5dca20ad45fe1f0fbfd7069b
    Note over S,U: Source contains call useT at line 83 - order unresolved
    Note over S,U: Source contains call useState at line 84 - order unresolved
    Note over S,U: Source contains call useState at line 85 - order unresolved
    Note over S,U: Source contains call Set at line 88 - order unresolved
    Note over S,U: Source contains call useState at line 89 - order unresolved
    Note over S,U: Source contains call useState at line 90 - order unresolved
    Note over S,U: Source contains call useCallback at line 92 - order unresolved
    Note over S,U: Source contains call async at line 93 - order unresolved
    Note over S,U: Source contains call setError at line 94 - order unresolved
    Note over S,U: Source contains call trim at line 95 - order unresolved
    Note over S,U: Source contains call all at line 98 - order unresolved
    Note over S,U: Source contains call fetchMarkers at line 98 - order unresolved
    Note over S,U: Source contains call fetchTrashedMarkers at line 98 - order unresolved
    Note over S,U: Source contains call setMediaID at line 99 - order unresolved
    Note over S,U: Source contains call setMarkers at line 100 - order unresolved
    Note over S,U: Source contains call sort at line 100 - order unresolved
    Note over S,U: Source contains call setTrashed at line 101 - order unresolved
    Note over S,U: Source contains call setSelected at line 102 - order unresolved
    Note over S,U: Source contains call Set at line 102 - order unresolved
    Note over S,U: Source contains call setLoaded at line 103 - order unresolved
    Note over S,U: Source contains call setError at line 105 - order unresolved
    Note over S,U: Source contains call t at line 105 - order unresolved
    Note over S,U: Source contains call localeCompare at line 79 - order unresolved
    Note over S,U: Source contains call async at line 502 - order unresolved
    Note over S,U: Source contains call onError at line 503 - order unresolved
    Note over S,U: Source contains call all at line 506 - order unresolved
    Note over S,U: Source contains call map at line 506 - order unresolved
    Note over S,U: Source contains call deleteMarker at line 506 - order unresolved
    Note over S,U: Source contains call onApplied at line 507 - order unresolved
    Note over S,U: Source contains call onError at line 509 - order unresolved
    Note over S,U: Source contains call t at line 509 - order unresolved
    Note over S,U: Source contains call async at line 114 - order unresolved
    Note over S,U: Source contains call setError at line 116 - order unresolved
    Note over S,U: Source contains call createMarker at line 118 - order unresolved
    Note over S,U: Source contains call blankDraft at line 118 - order unresolved
    Note over S,U: Source contains call load at line 119 - order unresolved
    Note over S,U: Source contains call setError at line 121 - order unresolved
    Note over S,U: Source contains call t at line 121 - order unresolved
    Note over S,U: Source contains call async at line 282 - order unresolved
    Note over S,U: Source contains call onError at line 283 - order unresolved
    Note over S,U: Source contains call deleteMarker at line 285 - order unresolved
    Note over S,U: Source contains call onSaved at line 286 - order unresolved
    Note over S,U: Source contains call onError at line 288 - order unresolved
    Note over S,U: Source contains call t at line 288 - order unresolved
    Note over S,U: Source contains call load at line 111 - order unresolved
    Note over S,U: Source contains call async at line 658 - order unresolved
    Note over S,U: Source contains call onError at line 659 - order unresolved
    Note over S,U: Source contains call restoreMarker at line 661 - order unresolved
    Note over S,U: Source contains call onRestored at line 662 - order unresolved
    Note over S,U: Source contains call onError at line 664 - order unresolved
    Note over S,U: Source contains call t at line 664 - order unresolved
    Note over S,U: Source contains call async at line 484 - order unresolved
    Note over S,U: Source contains call onError at line 485 - order unresolved
    Note over S,U: Source contains call all at line 488 - order unresolved
    Note over S,U: Source contains call map at line 489 - order unresolved
    Note over S,U: Source contains call toDraft at line 490 - order unresolved
    Note over S,U: Source contains call Set at line 491 - order unresolved
    Note over S,U: Source contains call updateMarker at line 492 - order unresolved
    Note over S,U: Source contains call setApplied at line 495 - order unresolved
    Note over S,U: Source contains call onApplied at line 496 - order unresolved
    Note over S,U: Source contains call onError at line 498 - order unresolved
    Note over S,U: Source contains call t at line 498 - order unresolved
    Note over S,U: Source contains call async at line 254 - order unresolved
    Note over S,U: Source contains call onError at line 255 - order unresolved
    Note over S,U: Source contains call updateMarker at line 257 - order unresolved
    Note over S,U: Source contains call onSaved at line 258 - order unresolved
    Note over S,U: Source contains call onError at line 260 - order unresolved
    Note over S,U: Source contains call t at line 260 - order unresolved
    Note over S,U: Source contains call useCallback at line 125 - order unresolved
    Note over S,U: Source contains call load at line 125 - order unresolved
    Note over S,U: Source contains call useCallback at line 112 - order unresolved
    Note over S,U: Source contains call load at line 112 - order unresolved
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
    Note over S,U: Source contains call setSelected at line 128 - order unresolved
    Note over S,U: Source contains call Set at line 129 - order unresolved
    Note over S,U: Source contains call add at line 130 - order unresolved
    Note over S,U: Source contains call delete at line 131 - order unresolved
```
