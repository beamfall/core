# Manage tags: tag tree, assign, bulk, trashed tags — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:a3e7b63e72224666c05bc986c9dadf3b -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/TagsPage\.tsx:356; symbol: AssignSection; span SHA256: ff76541b48b3a2f9b4e7cb4baf150f18989432602ae1aa6b427a1a99ae9c95a4.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 7904a72ccfa91625534cebea607e6a03
    Note over S,U: Source contains call useT at line 357 - order unresolved
    Note over S,U: Source contains call useState at line 359 - order unresolved
    Note over S,U: Source contains call useState at line 361 - order unresolved
    Note over S,U: Source contains call useT at line 499 - order unresolved
    Note over S,U: Source contains call useState at line 502 - order unresolved
    Note over S,U: Source contains call Set at line 503 - order unresolved
    Note over S,U: Source contains call useT at line 163 - order unresolved
    Note over S,U: Source contains call useState at line 164 - order unresolved
    Note over S,U: Source contains call useState at line 165 - order unresolved
    Note over S,U: Source contains call useState at line 167 - order unresolved
    Note over S,U: Source contains call useState at line 168 - order unresolved
    Note over S,U: Source contains call useT at line 84 - order unresolved
    Note over S,U: Source contains call useState at line 86 - order unresolved
    Note over S,U: Source contains call useState at line 87 - order unresolved
    Note over S,U: Source contains call useState at line 88 - order unresolved
    Note over S,U: Source contains call useT at line 651 - order unresolved
    Note over S,U: Source contains call t at line 351 - order unresolved
    Note over S,U: Source contains call t at line 352 - order unresolved
    Note over S,U: Source contains call t at line 353 - order unresolved
    Note over S,U: Source contains call filter at line 55 - order unresolved
    Note over S,U: Source contains call get at line 59 - order unresolved
    Note over S,U: Source contains call push at line 60 - order unresolved
    Note over S,U: Source contains call set at line 61 - order unresolved
    Note over S,U: Source contains call Set at line 63 - order unresolved
    Note over S,U: Source contains call map at line 63 - order unresolved
    Note over S,U: Source contains call setEditing at line 190 - order unresolved
    Note over S,U: Source contains call setEditName at line 191 - order unresolved
    Note over S,U: Source contains call setEditParent at line 192 - order unresolved
    Note over S,U: Source contains call setEditFocus at line 193 - order unresolved
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
    Note over S,U: Source contains call async at line 528 - order unresolved
    Note over S,U: Source contains call onError at line 529 - order unresolved
    Note over S,U: Source contains call idList at line 530 - order unresolved
    Note over S,U: Source contains call bulkAssignTags at line 534 - order unresolved
    Note over S,U: Source contains call setAffected at line 535 - order unresolved
    Note over S,U: Source contains call onError at line 537 - order unresolved
    Note over S,U: Source contains call t at line 537 - order unresolved
    Note over S,U: Source contains call async at line 374 - order unresolved
    Note over S,U: Source contains call onError at line 375 - order unresolved
    Note over S,U: Source contains call trim at line 376 - order unresolved
    Note over S,U: Source contains call setAssignment at line 379 - order unresolved
    Note over S,U: Source contains call assignTag at line 379 - order unresolved
    Note over S,U: Source contains call setAddTagID at line 380 - order unresolved
    Note over S,U: Source contains call onError at line 382 - order unresolved
    Note over S,U: Source contains call t at line 382 - order unresolved
    Note over S,U: Source contains call async at line 175 - order unresolved
    Note over S,U: Source contains call onError at line 176 - order unresolved
    Note over S,U: Source contains call trim at line 177 - order unresolved
    Note over S,U: Source contains call createTag at line 180 - order unresolved
    Note over S,U: Source contains call setName at line 181 - order unresolved
    Note over S,U: Source contains call setParentID at line 182 - order unresolved
    Note over S,U: Source contains call onChanged at line 183 - order unresolved
    Note over S,U: Source contains call onError at line 185 - order unresolved
    Note over S,U: Source contains call t at line 185 - order unresolved
    Note over S,U: Source contains call async at line 209 - order unresolved
    Note over S,U: Source contains call onError at line 210 - order unresolved
    Note over S,U: Source contains call deleteTag at line 212 - order unresolved
    Note over S,U: Source contains call onChanged at line 213 - order unresolved
    Note over S,U: Source contains call onError at line 215 - order unresolved
    Note over S,U: Source contains call t at line 215 - order unresolved
    Note over S,U: Source contains call async at line 363 - order unresolved
    Note over S,U: Source contains call onError at line 364 - order unresolved
    Note over S,U: Source contains call trim at line 365 - order unresolved
    Note over S,U: Source contains call setAssignment at line 368 - order unresolved
    Note over S,U: Source contains call fetchTagAssignments at line 368 - order unresolved
    Note over S,U: Source contains call onError at line 370 - order unresolved
    Note over S,U: Source contains call t at line 370 - order unresolved
    Note over S,U: Source contains call async at line 653 - order unresolved
    Note over S,U: Source contains call onError at line 654 - order unresolved
    Note over S,U: Source contains call restoreTag at line 656 - order unresolved
    Note over S,U: Source contains call onChanged at line 657 - order unresolved
    Note over S,U: Source contains call onError at line 659 - order unresolved
    Note over S,U: Source contains call t at line 659 - order unresolved
    Note over S,U: Source contains call async at line 196 - order unresolved
    Note over S,U: Source contains call onError at line 197 - order unresolved
    Note over S,U: Source contains call trim at line 198 - order unresolved
    Note over S,U: Source contains call updateTag at line 201 - order unresolved
    Note over S,U: Source contains call setEditing at line 202 - order unresolved
    Note over S,U: Source contains call onChanged at line 203 - order unresolved
    Note over S,U: Source contains call onError at line 205 - order unresolved
    Note over S,U: Source contains call t at line 205 - order unresolved
    Note over S,U: Source contains call async at line 386 - order unresolved
    Note over S,U: Source contains call onError at line 387 - order unresolved
    Note over S,U: Source contains call trim at line 388 - order unresolved
    Note over S,U: Source contains call setAssignment at line 391 - order unresolved
    Note over S,U: Source contains call unassignTag at line 391 - order unresolved
    Note over S,U: Source contains call onError at line 393 - order unresolved
    Note over S,U: Source contains call t at line 393 - order unresolved
    Note over S,U: Source contains call filter at line 221 - order unresolved
    Note over S,U: Source contains call useMemo at line 173 - order unresolved
    Note over S,U: Source contains call treeOrder at line 173 - order unresolved
    Note over S,U: Source contains call setSelected at line 513 - order unresolved
    Note over S,U: Source contains call Set at line 514 - order unresolved
    Note over S,U: Source contains call has at line 515 - order unresolved
    Note over S,U: Source contains call delete at line 515 - order unresolved
    Note over S,U: Source contains call add at line 516 - order unresolved
    Note over S,U: Source contains call useMemo at line 109 - order unresolved
    Note over S,U: Source contains call filter at line 109 - order unresolved
    Note over S,U: Source contains call get at line 66 - order unresolved
    Note over S,U: Source contains call sort at line 66 - order unresolved
    Note over S,U: Source contains call localeCompare at line 66 - order unresolved
    Note over S,U: Source contains call push at line 68 - order unresolved
    Note over S,U: Source contains call visit at line 69 - order unresolved
    Note over S,U: Source contains call min at line 69 - order unresolved
```
