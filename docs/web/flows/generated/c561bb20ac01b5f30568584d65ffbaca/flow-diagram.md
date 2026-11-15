# Console live sessions and host graphs — flow-diagram

Source revision: 25a58b2a60b350f2a582cb407acbbbc52274a39b. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:198e3a25e0ede1625afdc13a7bb02504 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Dashboard\.tsx:294; symbol: CHART\_HEIGHT; span SHA256: 795a0efbd16af2c292727707936c42deca150072ae3d45c960aba52fb4eb6337.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow c561bb20ac01b5f30568584d65ffbaca
    Note over S,U: Source contains call useT at line 545 - order unresolved
    Note over S,U: Source contains call useAsync at line 546 - order unresolved
    Note over S,U: Source contains call usePolling at line 547 - order unresolved
    Note over S,U: Source contains call map at line 549 - order unresolved
    Note over S,U: Source contains call parse at line 549 - order unresolved
    Note over S,U: Source contains call map at line 562 - order unresolved
    Note over S,U: Source contains call some at line 563 - order unresolved
    Note over S,U: Source contains call map at line 564 - order unresolved
    Note over S,U: Source contains call map at line 565 - order unresolved
    Note over S,U: Source contains call map at line 567 - order unresolved
    Note over S,U: Source contains call some at line 568 - order unresolved
    Note over S,U: Source contains call max at line 569 - order unresolved
    Note over S,U: Source contains call map at line 569 - order unresolved
    Note over S,U: Source contains call t at line 574 - order unresolved
    Note over S,U: Source contains call t at line 577 - order unresolved
    Note over S,U: Source contains call t at line 584 - order unresolved
    Note over S,U: Source contains call t at line 586 - order unresolved
    Note over S,U: Source contains call t at line 591 - order unresolved
    Note over S,U: Source contains call t at line 594 - order unresolved
    Note over S,U: Source contains call formatBytes at line 595 - order unresolved
    Note over S,U: Source contains call formatBytes at line 596 - order unresolved
    Note over S,U: Source contains call t at line 601 - order unresolved
    Note over S,U: Source contains call t at line 608 - order unresolved
    Note over S,U: Source contains call t at line 610 - order unresolved
    Note over S,U: Source contains call t at line 614 - order unresolved
    Note over S,U: Source contains call t at line 618 - order unresolved
    Note over S,U: Source contains call t at line 620 - order unresolved
    Note over S,U: Source contains call niceMax at line 626 - order unresolved
    Note over S,U: Source contains call useT at line 320 - order unresolved
    Note over S,U: Source contains call useId at line 321 - order unresolved
    Note over S,U: Source contains call useWidth at line 322 - order unresolved
    Note over S,U: Source contains call now at line 325 - order unresolved
    Note over S,U: Source contains call max at line 327 - order unresolved
    Note over S,U: Source contains call useAsync at line 259 - order unresolved
    Note over S,U: Source contains call useEventRefresh at line 260 - order unresolved
    Note over S,U: Source contains call usePolling at line 261 - order unresolved
    Note over S,U: Source contains call useT at line 173 - order unresolved
    Note over S,U: Source contains call useAsync at line 174 - order unresolved
    Note over S,U: Source contains call useEventRefresh at line 177 - order unresolved
    Note over S,U: Source contains call usePolling at line 178 - order unresolved
    Note over S,U: Source contains call useT at line 145 - order unresolved
    Note over S,U: Source contains call min at line 147 - order unresolved
    Note over S,U: Source contains call max at line 147 - order unresolved
    Note over S,U: Source contains call t at line 153 - order unresolved
    Note over S,U: Source contains call round at line 156 - order unresolved
    Note over S,U: Source contains call clock at line 161 - order unresolved
    Note over S,U: Source contains call max at line 93 - order unresolved
    Note over S,U: Source contains call floor at line 93 - order unresolved
    Note over S,U: Source contains call floor at line 94 - order unresolved
    Note over S,U: Source contains call floor at line 95 - order unresolved
    Note over S,U: Source contains call String at line 96 - order unresolved
    Note over S,U: Source contains call padStart at line 96 - order unresolved
    Note over S,U: Source contains call max at line 534 - order unresolved
    Note over S,U: Source contains call map at line 534 - order unresolved
    Note over S,U: Source contains call floor at line 535 - order unresolved
    Note over S,U: Source contains call log10 at line 535 - order unresolved
    Note over S,U: Source contains call push at line 525 - order unresolved
    Note over S,U: Source contains call reverse at line 529 - order unresolved
    Note over S,U: Source contains call Date at line 314 - order unresolved
    Note over S,U: Source contains call toLocaleTimeString at line 314 - order unresolved
    Note over S,U: Source contains call useEffect at line 59 - order unresolved
    Note over S,U: Source contains call useEffect at line 45 - order unresolved
    Note over S,U: Source contains call setInterval at line 46 - order unresolved
    Note over S,U: Source contains call refresh at line 47 - order unresolved
    Note over S,U: Source contains call clearInterval at line 49 - order unresolved
    Note over S,U: Source contains call useState at line 299 - order unresolved
    Note over S,U: Source contains call useLayoutEffect at line 300 - order unresolved
    Note over S,U: Source contains call ResizeObserver at line 303 - order unresolved
    Note over S,U: Source contains call round at line 304 - order unresolved
    Note over S,U: Source contains call setWidth at line 305 - order unresolved
    Note over S,U: Source contains call observe at line 307 - order unresolved
    Note over S,U: Source contains call disconnect at line 308 - order unresolved
    Note over S,U: Source contains call getBoundingClientRect at line 348 - order unresolved
    Note over S,U: Source contains call abs at line 352 - order unresolved
    Note over S,U: Source contains call setHover at line 354 - order unresolved
    Note over S,U: Source contains call forEach at line 335 - order unresolved
    Note over S,U: Source contains call async at line 64 - order unresolved
    Note over S,U: Source contains call refresh at line 72 - order unresolved
    Note over S,U: Source contains call setTimeout at line 77 - order unresolved
    Note over S,U: Source contains call terminateSession at line 181 - order unresolved
    Note over S,U: Source contains call refresh at line 182 - order unresolved
    Note over S,U: Source contains call min at line 330 - order unresolved
```
