# Manage artwork slots and candidates — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:162629d2a5437184156d26a846172605 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/ArtworkPage\.tsx:35; symbol: ArtworkPage; span SHA256: 4de7fcc23356a35273de7275a550023fc96b76a9ce3b9990683a7e6a851f080d.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 2ee85e517ea59a2a44c6f1b91291d7fd
    Note over S,U: Source contains call useT at line 36 - order unresolved
    Note over S,U: Source contains call useState at line 37 - order unresolved
    Note over S,U: Source contains call useState at line 38 - order unresolved
    Note over S,U: Source contains call useState at line 41 - order unresolved
    Note over S,U: Source contains call useT at line 118 - order unresolved
    Note over S,U: Source contains call t at line 123 - order unresolved
    Note over S,U: Source contains call t at line 125 - order unresolved
    Note over S,U: Source contains call t at line 127 - order unresolved
    Note over S,U: Source contains call map at line 130 - order unresolved
    Note over S,U: Source contains call isRelativePreviewURL at line 136 - order unresolved
    Note over S,U: Source contains call t at line 140 - order unresolved
    Note over S,U: Source contains call provenance at line 146 - order unresolved
    Note over S,U: Source contains call provenance at line 148 - order unresolved
    Note over S,U: Source contains call t at line 148 - order unresolved
    Note over S,U: Source contains call t at line 151 - order unresolved
    Note over S,U: Source contains call onPin at line 158 - order unresolved
    Note over S,U: Source contains call t at line 160 - order unresolved
    Note over S,U: Source contains call startsWith at line 23 - order unresolved
    Note over S,U: Source contains call filter at line 27 - order unresolved
    Note over S,U: Source contains call join at line 27 - order unresolved
    Note over S,U: Source contains call async at line 43 - order unresolved
    Note over S,U: Source contains call fetchArtworkCandidates at line 45 - order unresolved
    Note over S,U: Source contains call setSlots at line 46 - order unresolved
    Note over S,U: Source contains call setSlots at line 48 - order unresolved
    Note over S,U: Source contains call setError at line 49 - order unresolved
    Note over S,U: Source contains call t at line 49 - order unresolved
    Note over S,U: Source contains call async at line 53 - order unresolved
    Note over S,U: Source contains call setError at line 54 - order unresolved
    Note over S,U: Source contains call trim at line 55 - order unresolved
    Note over S,U: Source contains call setLoadedID at line 57 - order unresolved
    Note over S,U: Source contains call setSlots at line 58 - order unresolved
    Note over S,U: Source contains call all at line 59 - order unresolved
    Note over S,U: Source contains call map at line 59 - order unresolved
    Note over S,U: Source contains call loadSlot at line 59 - order unresolved
    Note over S,U: Source contains call async at line 62 - order unresolved
    Note over S,U: Source contains call setError at line 63 - order unresolved
    Note over S,U: Source contains call pinArtworkCandidate at line 65 - order unresolved
    Note over S,U: Source contains call loadSlot at line 67 - order unresolved
    Note over S,U: Source contains call setError at line 69 - order unresolved
    Note over S,U: Source contains call t at line 69 - order unresolved
```
