# Manage navigation overrides: nodes, theme, sections, history — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:cfec355f39d019cbb2990d69b192dbf3 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/NavOverridePage\.tsx:54; symbol: DEFAULT\_ACCENT; span SHA256: f8d513443d51f97f7d43dbc891aa1b8152ff430a1220277997181e47c564f541.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 48df4df13e249971d7603fc21fe8ed4b
    Note over S,U: Source contains call useT at line 574 - order unresolved
    Note over S,U: Source contains call t at line 577 - order unresolved
    Note over S,U: Source contains call t at line 578 - order unresolved
    Note over S,U: Source contains call onReset at line 585 - order unresolved
    Note over S,U: Source contains call t at line 587 - order unresolved
    Note over S,U: Source contains call t at line 592 - order unresolved
    Note over S,U: Source contains call t at line 597 - order unresolved
    Note over S,U: Source contains call t at line 598 - order unresolved
    Note over S,U: Source contains call t at line 599 - order unresolved
    Note over S,U: Source contains call map at line 603 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 605 - order unresolved
    Note over S,U: Source contains call summarize at line 606 - order unresolved
    Note over S,U: Source contains call onRestore at line 612 - order unresolved
    Note over S,U: Source contains call t at line 614 - order unresolved
    Note over S,U: Source contains call useT at line 94 - order unresolved
    Note over S,U: Source contains call useState at line 98 - order unresolved
    Note over S,U: Source contains call useState at line 99 - order unresolved
    Note over S,U: Source contains call useT at line 228 - order unresolved
    Note over S,U: Source contains call useState at line 230 - order unresolved
    Note over S,U: Source contains call useT at line 418 - order unresolved
    Note over S,U: Source contains call useState at line 419 - order unresolved
    Note over S,U: Source contains call useEffect at line 422 - order unresolved
    Note over S,U: Source contains call setLayoutID at line 423 - order unresolved
    Note over S,U: Source contains call find at line 426 - order unresolved
    Note over S,U: Source contains call Set at line 431 - order unresolved
    Note over S,U: Source contains call useT at line 351 - order unresolved
    Note over S,U: Source contains call useState at line 352 - order unresolved
    Note over S,U: Source contains call useEffect at line 355 - order unresolved
    Note over S,U: Source contains call setAccent at line 355 - order unresolved
    Note over S,U: Source contains call test at line 358 - order unresolved
    Note over S,U: Source contains call trim at line 358 - order unresolved
    Note over S,U: Source contains call t at line 362 - order unresolved
    Note over S,U: Source contains call t at line 363 - order unresolved
    Note over S,U: Source contains call t at line 366 - order unresolved
    Note over S,U: Source contains call t at line 377 - order unresolved
    Note over S,U: Source contains call setAccent at line 384 - order unresolved
    Note over S,U: Source contains call trim at line 390 - order unresolved
    Note over S,U: Source contains call onApply at line 391 - order unresolved
    Note over S,U: Source contains call trim at line 391 - order unresolved
    Note over S,U: Source contains call t at line 393 - order unresolved
    Note over S,U: Source contains call onApply at line 400 - order unresolved
    Note over S,U: Source contains call t at line 402 - order unresolved
    Note over S,U: Source contains call splice at line 88 - order unresolved
    Note over S,U: Source contains call splice at line 89 - order unresolved
    Note over S,U: Source contains call Map at line 67 - order unresolved
    Note over S,U: Source contains call map at line 67 - order unresolved
    Note over S,U: Source contains call get at line 71 - order unresolved
    Note over S,U: Source contains call has at line 72 - order unresolved
    Note over S,U: Source contains call push at line 73 - order unresolved
    Note over S,U: Source contains call add at line 74 - order unresolved
    Note over S,U: Source contains call has at line 78 - order unresolved
    Note over S,U: Source contains call push at line 78 - order unresolved
    Note over S,U: Source contains call push at line 553 - order unresolved
    Note over S,U: Source contains call push at line 554 - order unresolved
    Note over S,U: Source contains call t at line 554 - order unresolved
    Note over S,U: Source contains call keys at line 555 - order unresolved
    Note over S,U: Source contains call push at line 556 - order unresolved
    Note over S,U: Source contains call push at line 557 - order unresolved
    Note over S,U: Source contains call t at line 557 - order unresolved
    Note over S,U: Source contains call keys at line 558 - order unresolved
    Note over S,U: Source contains call push at line 559 - order unresolved
    Note over S,U: Source contains call join at line 560 - order unresolved
    Note over S,U: Source contains call t at line 560 - order unresolved
    Note over S,U: Source contains call setEditing at line 251 - order unresolved
    Note over S,U: Source contains call setEditLabel at line 252 - order unresolved
    Note over S,U: Source contains call useMemo at line 232 - order unresolved
    Note over S,U: Source contains call Set at line 232 - order unresolved
    Note over S,U: Source contains call useCallback at line 101 - order unresolved
    Note over S,U: Source contains call async at line 101 - order unresolved
    Note over S,U: Source contains call setError at line 102 - order unresolved
    Note over S,U: Source contains call setLoading at line 103 - order unresolved
    Note over S,U: Source contains call all at line 105 - order unresolved
    Note over S,U: Source contains call fetchManifest at line 106 - order unresolved
    Note over S,U: Source contains call fetchNavOverride at line 107 - order unresolved
    Note over S,U: Source contains call fetchNavOverrideHistory at line 108 - order unresolved
    Note over S,U: Source contains call setManifest at line 110 - order unresolved
    Note over S,U: Source contains call setOverride at line 111 - order unresolved
    Note over S,U: Source contains call setHistory at line 112 - order unresolved
    Note over S,U: Source contains call setError at line 114 - order unresolved
    Note over S,U: Source contains call t at line 114 - order unresolved
    Note over S,U: Source contains call setLoading at line 116 - order unresolved
    Note over S,U: Source contains call useMemo at line 434 - order unresolved
    Note over S,U: Source contains call Map at line 438 - order unresolved
    Note over S,U: Source contains call map at line 438 - order unresolved
    Note over S,U: Source contains call get at line 442 - order unresolved
    Note over S,U: Source contains call has at line 443 - order unresolved
    Note over S,U: Source contains call push at line 444 - order unresolved
    Note over S,U: Source contains call add at line 445 - order unresolved
    Note over S,U: Source contains call has at line 448 - order unresolved
    Note over S,U: Source contains call push at line 448 - order unresolved
    Note over S,U: Source contains call onApply at line 459 - order unresolved
    Note over S,U: Source contains call trim at line 256 - order unresolved
    Note over S,U: Source contains call setEditing at line 260 - order unresolved
    Note over S,U: Source contains call onApply at line 261 - order unresolved
```
