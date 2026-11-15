# Manage manual and smart collections — flow-diagram

Source revision: b952880cf4519beaa9738e25e349aabf600c9a41. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:53c48a58745ade83b87ae75f77ce4902 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/CollectionsPage\.tsx:364; symbol: CollectionRow; span SHA256: 5ca5177ddfc8b2d4a18487bf9a9fd06dd63e1931236eb8f812e720300a1d9470.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 01980267f2da1b8a6a45d6a38088bf67
    Note over S,U: Source contains call useT at line 385 - order unresolved
    Note over S,U: Source contains call useState at line 386 - order unresolved
    Note over S,U: Source contains call useState at line 387 - order unresolved
    Note over S,U: Source contains call useT at line 68 - order unresolved
    Note over S,U: Source contains call useSession at line 69 - order unresolved
    Note over S,U: Source contains call useState at line 72 - order unresolved
    Note over S,U: Source contains call useEffect at line 76 - order unresolved
    Note over S,U: Source contains call setLibraryID at line 77 - order unresolved
    Note over S,U: Source contains call setTab at line 81 - order unresolved
    Note over S,U: Source contains call t at line 85 - order unresolved
    Note over S,U: Source contains call t at line 87 - order unresolved
    Note over S,U: Source contains call t at line 90 - order unresolved
    Note over S,U: Source contains call t at line 94 - order unresolved
    Note over S,U: Source contains call t at line 98 - order unresolved
    Note over S,U: Source contains call map at line 101 - order unresolved
    Note over S,U: Source contains call t at line 109 - order unresolved
    Note over S,U: Source contains call useT at line 124 - order unresolved
    Note over S,U: Source contains call useState at line 126 - order unresolved
    Note over S,U: Source contains call useState at line 127 - order unresolved
    Note over S,U: Source contains call useState at line 128 - order unresolved
    Note over S,U: Source contains call useState at line 131 - order unresolved
    Note over S,U: Source contains call useState at line 132 - order unresolved
    Note over S,U: Source contains call useRef at line 135 - order unresolved
    Note over S,U: Source contains call useCallback at line 137 - order unresolved
    Note over S,U: Source contains call async at line 138 - order unresolved
    Note over S,U: Source contains call setError at line 140 - order unresolved
    Note over S,U: Source contains call setCollections at line 141 - order unresolved
    Note over S,U: Source contains call fetchCollections at line 144 - order unresolved
    Note over S,U: Source contains call setCollections at line 145 - order unresolved
    Note over S,U: Source contains call setError at line 147 - order unresolved
    Note over S,U: Source contains call t at line 147 - order unresolved
    Note over S,U: Source contains call useEffect at line 153 - order unresolved
    Note over S,U: Source contains call load at line 154 - order unresolved
    Note over S,U: Source contains call useMemo at line 161 - order unresolved
    Note over S,U: Source contains call filter at line 164 - order unresolved
    Note over S,U: Source contains call sort at line 165 - order unresolved
    Note over S,U: Source contains call localeCompare at line 165 - order unresolved
    Note over S,U: Source contains call useT at line 500 - order unresolved
    Note over S,U: Source contains call useState at line 504 - order unresolved
    Note over S,U: Source contains call useEffect at line 506 - order unresolved
    Note over S,U: Source contains call setMembers at line 507 - order unresolved
    Note over S,U: Source contains call sort at line 507 - order unresolved
    Note over S,U: Source contains call useT at line 787 - order unresolved
    Note over S,U: Source contains call useState at line 788 - order unresolved
    Note over S,U: Source contains call useState at line 789 - order unresolved
    Note over S,U: Source contains call useState at line 790 - order unresolved
    Note over S,U: Source contains call useT at line 660 - order unresolved
    Note over S,U: Source contains call useState at line 662 - order unresolved
    Note over S,U: Source contains call useRef at line 664 - order unresolved
    Note over S,U: Source contains call useCallback at line 666 - order unresolved
    Note over S,U: Source contains call async at line 667 - order unresolved
    Note over S,U: Source contains call setError at line 669 - order unresolved
    Note over S,U: Source contains call setSmart at line 670 - order unresolved
    Note over S,U: Source contains call fetchSmartCollections at line 673 - order unresolved
    Note over S,U: Source contains call setSmart at line 674 - order unresolved
    Note over S,U: Source contains call setError at line 676 - order unresolved
    Note over S,U: Source contains call t at line 676 - order unresolved
    Note over S,U: Source contains call useEffect at line 682 - order unresolved
    Note over S,U: Source contains call load at line 683 - order unresolved
    Note over S,U: Source contains call useMemo at line 689 - order unresolved
    Note over S,U: Source contains call find at line 690 - order unresolved
    Note over S,U: Source contains call trim at line 390 - order unresolved
    Note over S,U: Source contains call setEditing at line 391 - order unresolved
    Note over S,U: Source contains call onRename at line 392 - order unresolved
    Note over S,U: Source contains call async at line 545 - order unresolved
    Note over S,U: Source contains call split at line 547 - order unresolved
    Note over S,U: Source contains call map at line 548 - order unresolved
    Note over S,U: Source contains call trim at line 548 - order unresolved
    Note over S,U: Source contains call filter at line 549 - order unresolved
    Note over S,U: Source contains call addCollectionMedia at line 552 - order unresolved
    Note over S,U: Source contains call setAddIDs at line 553 - order unresolved
    Note over S,U: Source contains call onChanged at line 554 - order unresolved
    Note over S,U: Source contains call onError at line 556 - order unresolved
    Note over S,U: Source contains call async at line 173 - order unresolved
    Note over S,U: Source contains call setError at line 174 - order unresolved
    Note over S,U: Source contains call trim at line 175 - order unresolved
    Note over S,U: Source contains call createCollection at line 178 - order unresolved
    Note over S,U: Source contains call setNewName at line 183 - order unresolved
    Note over S,U: Source contains call setNewParent at line 184 - order unresolved
    Note over S,U: Source contains call reload at line 185 - order unresolved
    Note over S,U: Source contains call setError at line 187 - order unresolved
    Note over S,U: Source contains call t at line 187 - order unresolved
    Note over S,U: Source contains call setMembers at line 532 - order unresolved
    Note over S,U: Source contains call persistOrder at line 533 - order unresolved
    Note over S,U: Source contains call async at line 536 - order unresolved
    Note over S,U: Source contains call removeCollectionMedia at line 538 - order unresolved
    Note over S,U: Source contains call onChanged at line 539 - order unresolved
    Note over S,U: Source contains call onError at line 541 - order unresolved
    Note over S,U: Source contains call async at line 191 - order unresolved
    Note over S,U: Source contains call setError at line 192 - order unresolved
    Note over S,U: Source contains call updateCollection at line 194 - order unresolved
    Note over S,U: Source contains call reload at line 195 - order unresolved
    Note over S,U: Source contains call setError at line 197 - order unresolved
    Note over S,U: Source contains call t at line 197 - order unresolved
    Note over S,U: Source contains call async at line 205 - order unresolved
    Note over S,U: Source contains call setError at line 208 - order unresolved
    Note over S,U: Source contains call updateCollection at line 212 - order unresolved
    Note over S,U: Source contains call setError at line 214 - order unresolved
    Note over S,U: Source contains call t at line 214 - order unresolved
    Note over S,U: Source contains call updateCollection at line 219 - order unresolved
    Note over S,U: Source contains call updateCollection at line 222 - order unresolved
    Note over S,U: Source contains call catch at line 222 - order unresolved
    Note over S,U: Source contains call reload at line 224 - order unresolved
    Note over S,U: Source contains call setError at line 225 - order unresolved
    Note over S,U: Source contains call t at line 225 - order unresolved
    Note over S,U: Source contains call async at line 239 - order unresolved
    Note over S,U: Source contains call setError at line 240 - order unresolved
    Note over S,U: Source contains call restoreCollection at line 242 - order unresolved
    Note over S,U: Source contains call reload at line 243 - order unresolved
    Note over S,U: Source contains call setError at line 245 - order unresolved
    Note over S,U: Source contains call t at line 245 - order unresolved
    Note over S,U: Source contains call async at line 792 - order unresolved
    Note over S,U: Source contains call trim at line 793 - order unresolved
    Note over S,U: Source contains call trim at line 794 - order unresolved
    Note over S,U: Source contains call updateSmartCollection at line 798 - order unresolved
    Note over S,U: Source contains call trim at line 802 - order unresolved
    Note over S,U: Source contains call createSmartCollection at line 806 - order unresolved
    Note over S,U: Source contains call trim at line 810 - order unresolved
    Note over S,U: Source contains call onSaved at line 813 - order unresolved
    Note over S,U: Source contains call onError at line 815 - order unresolved
    Note over S,U: Source contains call async at line 513 - order unresolved
    Note over S,U: Source contains call reorderCollectionMedia at line 515 - order unresolved
    Note over S,U: Source contains call map at line 517 - order unresolved
    Note over S,U: Source contains call onChanged at line 519 - order unresolved
    Note over S,U: Source contains call setMembers at line 521 - order unresolved
    Note over S,U: Source contains call sort at line 521 - order unresolved
    Note over S,U: Source contains call onChanged at line 522 - order unresolved
    Note over S,U: Source contains call onError at line 523 - order unresolved
    Note over S,U: Source contains call load at line 171 - order unresolved
    Note over S,U: Source contains call useMemo at line 169 - order unresolved
    Note over S,U: Source contains call find at line 169 - order unresolved
    Note over S,U: Source contains call useMemo at line 168 - order unresolved
    Note over S,U: Source contains call filter at line 168 - order unresolved
```
