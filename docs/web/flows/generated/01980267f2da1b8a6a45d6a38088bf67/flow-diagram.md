# Manage manual and smart collections — flow-diagram

Source revision: 03e521f81ef085ec14c6d7d5d76533692ecba36c. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:53c48a58745ade83b87ae75f77ce4902 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/CollectionsPage\.tsx:365; symbol: CollectionRow; span SHA256: 5ca5177ddfc8b2d4a18487bf9a9fd06dd63e1931236eb8f812e720300a1d9470.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 01980267f2da1b8a6a45d6a38088bf67
    Note over S,U: Source contains call useT at line 386 - order unresolved
    Note over S,U: Source contains call useState at line 387 - order unresolved
    Note over S,U: Source contains call useState at line 388 - order unresolved
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
    Note over S,U: Source contains call useT at line 501 - order unresolved
    Note over S,U: Source contains call useState at line 505 - order unresolved
    Note over S,U: Source contains call useEffect at line 507 - order unresolved
    Note over S,U: Source contains call setMembers at line 508 - order unresolved
    Note over S,U: Source contains call sort at line 508 - order unresolved
    Note over S,U: Source contains call useT at line 788 - order unresolved
    Note over S,U: Source contains call useState at line 789 - order unresolved
    Note over S,U: Source contains call useState at line 790 - order unresolved
    Note over S,U: Source contains call useState at line 791 - order unresolved
    Note over S,U: Source contains call useT at line 661 - order unresolved
    Note over S,U: Source contains call useState at line 663 - order unresolved
    Note over S,U: Source contains call useRef at line 665 - order unresolved
    Note over S,U: Source contains call useCallback at line 667 - order unresolved
    Note over S,U: Source contains call async at line 668 - order unresolved
    Note over S,U: Source contains call setError at line 670 - order unresolved
    Note over S,U: Source contains call setSmart at line 671 - order unresolved
    Note over S,U: Source contains call fetchSmartCollections at line 674 - order unresolved
    Note over S,U: Source contains call setSmart at line 675 - order unresolved
    Note over S,U: Source contains call setError at line 677 - order unresolved
    Note over S,U: Source contains call t at line 677 - order unresolved
    Note over S,U: Source contains call useEffect at line 683 - order unresolved
    Note over S,U: Source contains call load at line 684 - order unresolved
    Note over S,U: Source contains call useMemo at line 690 - order unresolved
    Note over S,U: Source contains call find at line 691 - order unresolved
    Note over S,U: Source contains call trim at line 391 - order unresolved
    Note over S,U: Source contains call setEditing at line 392 - order unresolved
    Note over S,U: Source contains call onRename at line 393 - order unresolved
    Note over S,U: Source contains call async at line 546 - order unresolved
    Note over S,U: Source contains call split at line 548 - order unresolved
    Note over S,U: Source contains call map at line 549 - order unresolved
    Note over S,U: Source contains call trim at line 549 - order unresolved
    Note over S,U: Source contains call filter at line 550 - order unresolved
    Note over S,U: Source contains call addCollectionMedia at line 553 - order unresolved
    Note over S,U: Source contains call setAddIDs at line 554 - order unresolved
    Note over S,U: Source contains call onChanged at line 555 - order unresolved
    Note over S,U: Source contains call onError at line 557 - order unresolved
    Note over S,U: Source contains call async at line 173 - order unresolved
    Note over S,U: Source contains call setError at line 174 - order unresolved
    Note over S,U: Source contains call trim at line 175 - order unresolved
    Note over S,U: Source contains call createCollection at line 178 - order unresolved
    Note over S,U: Source contains call setNewName at line 183 - order unresolved
    Note over S,U: Source contains call setNewParent at line 184 - order unresolved
    Note over S,U: Source contains call reload at line 185 - order unresolved
    Note over S,U: Source contains call setError at line 187 - order unresolved
    Note over S,U: Source contains call t at line 187 - order unresolved
    Note over S,U: Source contains call setMembers at line 533 - order unresolved
    Note over S,U: Source contains call persistOrder at line 534 - order unresolved
    Note over S,U: Source contains call async at line 537 - order unresolved
    Note over S,U: Source contains call removeCollectionMedia at line 539 - order unresolved
    Note over S,U: Source contains call onChanged at line 540 - order unresolved
    Note over S,U: Source contains call onError at line 542 - order unresolved
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
    Note over S,U: Source contains call async at line 793 - order unresolved
    Note over S,U: Source contains call trim at line 794 - order unresolved
    Note over S,U: Source contains call trim at line 795 - order unresolved
    Note over S,U: Source contains call updateSmartCollection at line 799 - order unresolved
    Note over S,U: Source contains call trim at line 803 - order unresolved
    Note over S,U: Source contains call createSmartCollection at line 807 - order unresolved
    Note over S,U: Source contains call trim at line 811 - order unresolved
    Note over S,U: Source contains call onSaved at line 814 - order unresolved
    Note over S,U: Source contains call onError at line 816 - order unresolved
    Note over S,U: Source contains call async at line 514 - order unresolved
    Note over S,U: Source contains call reorderCollectionMedia at line 516 - order unresolved
    Note over S,U: Source contains call map at line 518 - order unresolved
    Note over S,U: Source contains call onChanged at line 520 - order unresolved
    Note over S,U: Source contains call setMembers at line 522 - order unresolved
    Note over S,U: Source contains call sort at line 522 - order unresolved
    Note over S,U: Source contains call onChanged at line 523 - order unresolved
    Note over S,U: Source contains call onError at line 524 - order unresolved
    Note over S,U: Source contains call load at line 171 - order unresolved
    Note over S,U: Source contains call useMemo at line 169 - order unresolved
    Note over S,U: Source contains call find at line 169 - order unresolved
    Note over S,U: Source contains call useMemo at line 168 - order unresolved
    Note over S,U: Source contains call filter at line 168 - order unresolved
```
