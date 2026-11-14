# Manage tags: tag tree, assign, bulk, trashed tags — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:a3e7b63e72224666c05bc986c9dadf3b -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:358; symbol: AssignSection; span SHA256: ff76541b48b3a2f9b4e7cb4baf150f18989432602ae1aa6b427a1a99ae9c95a4.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 7904a72ccfa91625534cebea607e6a03
    Note over S,U: Source contains call useT at line 359 - order unresolved
    Note over S,U: Source contains call useState at line 361 - order unresolved
    Note over S,U: Source contains call useState at line 363 - order unresolved
    Note over S,U: Source contains call useT at line 501 - order unresolved
    Note over S,U: Source contains call useState at line 504 - order unresolved
    Note over S,U: Source contains call Set at line 505 - order unresolved
    Note over S,U: Source contains call useT at line 165 - order unresolved
    Note over S,U: Source contains call useState at line 166 - order unresolved
    Note over S,U: Source contains call useState at line 167 - order unresolved
    Note over S,U: Source contains call useState at line 169 - order unresolved
    Note over S,U: Source contains call useState at line 170 - order unresolved
    Note over S,U: Source contains call useT at line 84 - order unresolved
    Note over S,U: Source contains call useState at line 86 - order unresolved
    Note over S,U: Source contains call useState at line 87 - order unresolved
    Note over S,U: Source contains call useState at line 88 - order unresolved
    Note over S,U: Source contains call useT at line 653 - order unresolved
    Note over S,U: Source contains call t at line 353 - order unresolved
    Note over S,U: Source contains call t at line 354 - order unresolved
    Note over S,U: Source contains call t at line 355 - order unresolved
    Note over S,U: Source contains call filter at line 55 - order unresolved
    Note over S,U: Source contains call get at line 59 - order unresolved
    Note over S,U: Source contains call push at line 60 - order unresolved
    Note over S,U: Source contains call set at line 61 - order unresolved
    Note over S,U: Source contains call Set at line 63 - order unresolved
    Note over S,U: Source contains call map at line 63 - order unresolved
    Note over S,U: Source contains call setEditing at line 192 - order unresolved
    Note over S,U: Source contains call setEditName at line 193 - order unresolved
    Note over S,U: Source contains call setEditParent at line 194 - order unresolved
    Note over S,U: Source contains call setEditFocus at line 195 - order unresolved
    Note over S,U: Source contains call useMemo at line 108 - order unresolved
    Note over S,U: Source contains call filter at line 108 - order unresolved
    Note over S,U: Source contains call useCallback at line 90 - order unresolved
    Note over S,U: Source contains call async at line 90 - order unresolved
    Note over S,U: Source contains call setError at line 91 - order unresolved
    Note over S,U: Source contains call setLoading at line 92 - order unresolved
    Note over S,U: Source contains call setTags at line 96 - order unresolved
    Note over S,U: Source contains call fetchTags at line 96 - order unresolved
    Note over S,U: Source contains call setError at line 98 - order unresolved
    Note over S,U: Source contains call t at line 98 - order unresolved
    Note over S,U: Source contains call setLoading at line 100 - order unresolved
    Note over S,U: Source contains call async at line 530 - order unresolved
    Note over S,U: Source contains call onError at line 531 - order unresolved
    Note over S,U: Source contains call idList at line 532 - order unresolved
    Note over S,U: Source contains call bulkAssignTags at line 536 - order unresolved
    Note over S,U: Source contains call setAffected at line 537 - order unresolved
    Note over S,U: Source contains call onError at line 539 - order unresolved
    Note over S,U: Source contains call t at line 539 - order unresolved
    Note over S,U: Source contains call async at line 376 - order unresolved
    Note over S,U: Source contains call onError at line 377 - order unresolved
    Note over S,U: Source contains call trim at line 378 - order unresolved
    Note over S,U: Source contains call setAssignment at line 381 - order unresolved
    Note over S,U: Source contains call assignTag at line 381 - order unresolved
    Note over S,U: Source contains call setAddTagID at line 382 - order unresolved
    Note over S,U: Source contains call onError at line 384 - order unresolved
    Note over S,U: Source contains call t at line 384 - order unresolved
    Note over S,U: Source contains call async at line 177 - order unresolved
    Note over S,U: Source contains call onError at line 178 - order unresolved
    Note over S,U: Source contains call trim at line 179 - order unresolved
    Note over S,U: Source contains call createTag at line 182 - order unresolved
    Note over S,U: Source contains call setName at line 183 - order unresolved
    Note over S,U: Source contains call setParentID at line 184 - order unresolved
    Note over S,U: Source contains call onChanged at line 185 - order unresolved
    Note over S,U: Source contains call onError at line 187 - order unresolved
    Note over S,U: Source contains call t at line 187 - order unresolved
    Note over S,U: Source contains call async at line 211 - order unresolved
    Note over S,U: Source contains call onError at line 212 - order unresolved
    Note over S,U: Source contains call deleteTag at line 214 - order unresolved
    Note over S,U: Source contains call onChanged at line 215 - order unresolved
    Note over S,U: Source contains call onError at line 217 - order unresolved
    Note over S,U: Source contains call t at line 217 - order unresolved
    Note over S,U: Source contains call async at line 365 - order unresolved
    Note over S,U: Source contains call onError at line 366 - order unresolved
    Note over S,U: Source contains call trim at line 367 - order unresolved
    Note over S,U: Source contains call setAssignment at line 370 - order unresolved
    Note over S,U: Source contains call fetchTagAssignments at line 370 - order unresolved
    Note over S,U: Source contains call onError at line 372 - order unresolved
    Note over S,U: Source contains call t at line 372 - order unresolved
    Note over S,U: Source contains call async at line 655 - order unresolved
    Note over S,U: Source contains call onError at line 656 - order unresolved
    Note over S,U: Source contains call restoreTag at line 658 - order unresolved
    Note over S,U: Source contains call onChanged at line 659 - order unresolved
    Note over S,U: Source contains call onError at line 661 - order unresolved
    Note over S,U: Source contains call t at line 661 - order unresolved
    Note over S,U: Source contains call async at line 198 - order unresolved
    Note over S,U: Source contains call onError at line 199 - order unresolved
    Note over S,U: Source contains call trim at line 200 - order unresolved
    Note over S,U: Source contains call updateTag at line 203 - order unresolved
    Note over S,U: Source contains call setEditing at line 204 - order unresolved
    Note over S,U: Source contains call onChanged at line 205 - order unresolved
    Note over S,U: Source contains call onError at line 207 - order unresolved
    Note over S,U: Source contains call t at line 207 - order unresolved
    Note over S,U: Source contains call async at line 388 - order unresolved
    Note over S,U: Source contains call onError at line 389 - order unresolved
    Note over S,U: Source contains call trim at line 390 - order unresolved
    Note over S,U: Source contains call setAssignment at line 393 - order unresolved
    Note over S,U: Source contains call unassignTag at line 393 - order unresolved
    Note over S,U: Source contains call onError at line 395 - order unresolved
    Note over S,U: Source contains call t at line 395 - order unresolved
    Note over S,U: Source contains call filter at line 223 - order unresolved
    Note over S,U: Source contains call useMemo at line 175 - order unresolved
    Note over S,U: Source contains call treeOrder at line 175 - order unresolved
    Note over S,U: Source contains call setSelected at line 515 - order unresolved
    Note over S,U: Source contains call Set at line 516 - order unresolved
    Note over S,U: Source contains call has at line 517 - order unresolved
    Note over S,U: Source contains call delete at line 517 - order unresolved
    Note over S,U: Source contains call add at line 518 - order unresolved
    Note over S,U: Source contains call useMemo at line 109 - order unresolved
    Note over S,U: Source contains call filter at line 109 - order unresolved
    Note over S,U: Source contains call get at line 66 - order unresolved
    Note over S,U: Source contains call sort at line 66 - order unresolved
    Note over S,U: Source contains call localeCompare at line 66 - order unresolved
    Note over S,U: Source contains call push at line 68 - order unresolved
    Note over S,U: Source contains call visit at line 69 - order unresolved
    Note over S,U: Source contains call min at line 69 - order unresolved
```
