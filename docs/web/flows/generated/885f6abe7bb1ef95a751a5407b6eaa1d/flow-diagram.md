# Console operations: resource governor, job queue and job log — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:f1ce1ed6efb84e59e8c457a01cb5ac19 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/OperationsControls\.tsx:159; symbol: GovernorSettings; span SHA256: abc1dc8faca9781de3fb18b37fb9703e88f622dd772866977a585ed5dcf4a906.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 885f6abe7bb1ef95a751a5407b6eaa1d
    Note over S,U: Source contains call useT at line 160 - order unresolved
    Note over S,U: Source contains call useAsync at line 161 - order unresolved
    Note over S,U: Source contains call useState at line 162 - order unresolved
    Note over S,U: Source contains call useState at line 163 - order unresolved
    Note over S,U: Source contains call useState at line 164 - order unresolved
    Note over S,U: Source contains call useState at line 165 - order unresolved
    Note over S,U: Source contains call useEffect at line 167 - order unresolved
    Note over S,U: Source contains call setWorkers at line 169 - order unresolved
    Note over S,U: Source contains call max at line 169 - order unresolved
    Note over S,U: Source contains call setRetentionDays at line 170 - order unresolved
    Note over S,U: Source contains call max at line 170 - order unresolved
    Note over S,U: Source contains call useT at line 95 - order unresolved
    Note over S,U: Source contains call useAsync at line 96 - order unresolved
    Note over S,U: Source contains call useEffect at line 98 - order unresolved
    Note over S,U: Source contains call subscribeEvents at line 98 - order unresolved
    Note over S,U: Source contains call refresh at line 98 - order unresolved
    Note over S,U: Source contains call refresh at line 105 - order unresolved
    Note over S,U: Source contains call t at line 107 - order unresolved
    Note over S,U: Source contains call t at line 119 - order unresolved
    Note over S,U: Source contains call t at line 125 - order unresolved
    Note over S,U: Source contains call t at line 141 - order unresolved
    Note over S,U: Source contains call t at line 144 - order unresolved
    Note over S,U: Source contains call t at line 146 - order unresolved
    Note over S,U: Source contains call useT at line 309 - order unresolved
    Note over S,U: Source contains call useAsync at line 310 - order unresolved
    Note over S,U: Source contains call fetchJobEvents at line 310 - order unresolved
    Note over S,U: Source contains call map at line 319 - order unresolved
    Note over S,U: Source contains call jobEventDetail at line 325 - order unresolved
    Note over S,U: Source contains call redactJobLogPayload at line 325 - order unresolved
    Note over S,U: Source contains call t at line 326 - order unresolved
    Note over S,U: Source contains call useT at line 355 - order unresolved
    Note over S,U: Source contains call useAsync at line 356 - order unresolved
    Note over S,U: Source contains call useAsync at line 357 - order unresolved
    Note over S,U: Source contains call useState at line 360 - order unresolved
    Note over S,U: Source contains call useEffect at line 362 - order unresolved
    Note over S,U: Source contains call subscribeEvents at line 364 - order unresolved
    Note over S,U: Source contains call refresh at line 365 - order unresolved
    Note over S,U: Source contains call setLogRefresh at line 366 - order unresolved
    Note over S,U: Source contains call useT at line 76 - order unresolved
    Note over S,U: Source contains call t at line 79 - order unresolved
    Note over S,U: Source contains call t at line 82 - order unresolved
    Note over S,U: Source contains call reduce at line 61 - order unresolved
    Note over S,U: Source contains call replace at line 62 - order unresolved
    Note over S,U: Source contains call jobStatusState at line 46 - order unresolved
```
