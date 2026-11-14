# Console live sessions: update awareness, jobs, live sessions and host graphs — flow-diagram

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:198e3a25e0ede1625afdc13a7bb02504 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/Dashboard\.tsx:289; symbol: CHART\_HEIGHT; span SHA256: 795a0efbd16af2c292727707936c42deca150072ae3d45c960aba52fb4eb6337.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow c561bb20ac01b5f30568584d65ffbaca
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 540 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 541 - order unresolved
    Note over S,U: Source contains call usePolling in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 542 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 544 - order unresolved
    Note over S,U: Source contains call parse in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 544 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 557 - order unresolved
    Note over S,U: Source contains call some in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 558 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 559 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 560 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 562 - order unresolved
    Note over S,U: Source contains call some in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 563 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 564 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 564 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 569 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 572 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 579 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 581 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 586 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 589 - order unresolved
    Note over S,U: Source contains call formatBytes in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 590 - order unresolved
    Note over S,U: Source contains call formatBytes in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 591 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 596 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 603 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 605 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 609 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 613 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 615 - order unresolved
    Note over S,U: Source contains call niceMax in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 621 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 315 - order unresolved
    Note over S,U: Source contains call useId in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 316 - order unresolved
    Note over S,U: Source contains call useWidth in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 317 - order unresolved
    Note over S,U: Source contains call now in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 320 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 322 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 257 - order unresolved
    Note over S,U: Source contains call useEventRefresh in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 258 - order unresolved
    Note over S,U: Source contains call usePolling in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 259 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 170 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 171 - order unresolved
    Note over S,U: Source contains call useEventRefresh in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 174 - order unresolved
    Note over S,U: Source contains call usePolling in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 175 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 142 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 144 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 144 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 150 - order unresolved
    Note over S,U: Source contains call round in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 153 - order unresolved
    Note over S,U: Source contains call clock in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 158 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 93 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 93 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 94 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 95 - order unresolved
    Note over S,U: Source contains call String in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 96 - order unresolved
    Note over S,U: Source contains call padStart in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 96 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 529 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 529 - order unresolved
    Note over S,U: Source contains call floor in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 530 - order unresolved
    Note over S,U: Source contains call log10 in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 530 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 520 - order unresolved
    Note over S,U: Source contains call reverse in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 524 - order unresolved
    Note over S,U: Source contains call Date in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 309 - order unresolved
    Note over S,U: Source contains call toLocaleTimeString in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 309 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 59 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 45 - order unresolved
    Note over S,U: Source contains call setInterval in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 46 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 47 - order unresolved
    Note over S,U: Source contains call clearInterval in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 49 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 294 - order unresolved
    Note over S,U: Source contains call useLayoutEffect in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 295 - order unresolved
    Note over S,U: Source contains call ResizeObserver in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 298 - order unresolved
    Note over S,U: Source contains call round in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 299 - order unresolved
    Note over S,U: Source contains call setWidth in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 300 - order unresolved
    Note over S,U: Source contains call observe in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 302 - order unresolved
    Note over S,U: Source contains call disconnect in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 303 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/Operations.tsx at line 351 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/Operations.tsx at line 352 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/console/sections/Operations.tsx at line 355 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/console/sections/Operations.tsx at line 357 - order unresolved
    Note over S,U: Source contains call subscribeEvents in internal/web/app/src/areas/console/sections/Operations.tsx at line 358 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/Operations.tsx at line 359 - order unresolved
    Note over S,U: Source contains call setStream in internal/web/app/src/areas/console/sections/Operations.tsx at line 364 - order unresolved
    Note over S,U: Source contains call setStream in internal/web/app/src/areas/console/sections/Operations.tsx at line 367 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/Operations.tsx at line 377 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/Operations.tsx at line 387 - order unresolved
    Note over S,U: Source contains call round in internal/web/app/src/areas/console/sections/Operations.tsx at line 389 - order unresolved
    Note over S,U: Source contains call statusTone in internal/web/app/src/areas/console/sections/Operations.tsx at line 390 - order unresolved
    Note over S,U: Source contains call jobKindLabel in internal/web/app/src/areas/console/sections/Operations.tsx at line 393 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 400 - order unresolved
    Note over S,U: Source contains call jobProgressDetail in internal/web/app/src/areas/console/sections/Operations.tsx at line 407 - order unresolved
    Note over S,U: Source contains call jobStatusLabel in internal/web/app/src/areas/console/sections/Operations.tsx at line 408 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/Operations.tsx at line 321 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 331 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 340 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/Operations.tsx at line 64 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 69 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 74 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 75 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 80 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 86 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 87 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 93 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 94 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 99 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 100 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 101 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 102 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 103 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 106 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 107 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 110 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 111 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 114 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 115 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 118 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 119 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 126 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 129 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 132 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 144 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 147 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 148 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 150 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 152 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 155 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 158 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/Operations.tsx at line 163 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/Operations.tsx at line 171 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/Operations.tsx at line 172 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/Operations.tsx at line 173 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Operations.tsx at line 174 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/Operations.tsx at line 175 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/console/sections/Operations.tsx at line 177 - order unresolved
    Note over S,U: Source contains call setEnabled in internal/web/app/src/areas/console/sections/Operations.tsx at line 179 - order unresolved
    Note over S,U: Source contains call setChannel in internal/web/app/src/areas/console/sections/Operations.tsx at line 180 - order unresolved
    Note over S,U: Source contains call jobStatusState in internal/web/app/src/areas/console/sections/Operations.tsx at line 30 - order unresolved
    Note over S,U: Source contains call getBoundingClientRect in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 343 - order unresolved
    Note over S,U: Source contains call abs in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 347 - order unresolved
    Note over S,U: Source contains call setHover in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 349 - order unresolved
    Note over S,U: Source contains call forEach in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 330 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 64 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 72 - order unresolved
    Note over S,U: Source contains call setTimeout in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 77 - order unresolved
    Note over S,U: Source contains call terminateSession in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 178 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 179 - order unresolved
    Note over S,U: Source contains call min in internal/web/app/src/areas/console/sections/Dashboard.tsx at line 325 - order unresolved
    Note over S,U: Source contains call preventDefault in internal/web/app/src/areas/console/sections/Operations.tsx at line 184 - order unresolved
    Note over S,U: Source contains call setConsoleConfig in internal/web/app/src/areas/console/sections/Operations.tsx at line 185 - order unresolved
    Note over S,U: Source contains call setData in internal/web/app/src/areas/console/sections/Operations.tsx at line 189 - order unresolved
    Note over S,U: Source contains call refresh in internal/web/app/src/areas/console/sections/Operations.tsx at line 190 - order unresolved
```
