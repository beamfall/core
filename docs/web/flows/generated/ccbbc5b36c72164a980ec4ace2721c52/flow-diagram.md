# Console area shell: route table, section nav, host firewall, session bar — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:fad205a35a982626c5b6b92999240d7d -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/ConfirmButton\.tsx:20; symbol: ConfirmButton; span SHA256: fd1970dc9047b48eb27240271171b21b6270a622a3bb61edbb1acec520d7eb7a.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow ccbbc5b36c72164a980ec4ace2721c52
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/ConfirmButton.tsx at line 41 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/ConfirmButton.tsx at line 42 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/ConfirmButton.tsx at line 43 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/Section.tsx at line 49 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/Section.tsx at line 52 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/Section.tsx at line 24 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/Section.tsx at line 35 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/Section.tsx at line 37 - order unresolved
    Note over S,U: Source contains call any in internal/web/app/src/areas/console/adminEpoch.ts at line 38 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/areas/console/index.tsx at line 347 - order unresolved
    Note over S,U: Source contains call useRegister in internal/web/app/src/areas/console/index.tsx at line 433 - order unresolved
    Note over S,U: Source contains call useSession in internal/web/app/src/areas/console/index.tsx at line 434 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/index.tsx at line 435 - order unresolved
    Note over S,U: Source contains call useLocation in internal/web/app/src/areas/console/index.tsx at line 436 - order unresolved
    Note over S,U: Source contains call groupsForHost in internal/web/app/src/areas/console/index.tsx at line 437 - order unresolved
    Note over S,U: Source contains call isBeamfallAppStoreHost in internal/web/app/src/areas/console/index.tsx at line 437 - order unresolved
    Note over S,U: Source contains call flatMap in internal/web/app/src/areas/console/index.tsx at line 438 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/console/index.tsx at line 439 - order unresolved
    Note over S,U: Source contains call test in internal/web/app/src/areas/console/index.tsx at line 441 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/console/index.tsx at line 445 - order unresolved
    Note over S,U: Source contains call setRegister in internal/web/app/src/areas/console/index.tsx at line 445 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 450 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 467 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 473 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 476 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/index.tsx at line 387 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 389 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 390 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 391 - order unresolved
    Note over S,U: Source contains call cn in internal/web/app/src/areas/console/index.tsx at line 398 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 401 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 408 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 415 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/index.tsx at line 370 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 376 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 380 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 351 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/index.tsx at line 354 - order unresolved
    Note over S,U: Source contains call has in internal/web/app/src/areas/console/index.tsx at line 354 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 355 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/index.tsx at line 363 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/console/ConfirmButton.tsx at line 46 - order unresolved
    Note over S,U: Source contains call onConfirm in internal/web/app/src/areas/console/ConfirmButton.tsx at line 48 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/console/ConfirmButton.tsx at line 50 - order unresolved
    Note over S,U: Source contains call setOpen in internal/web/app/src/areas/console/ConfirmButton.tsx at line 51 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/console/useAsync.ts at line 56 - order unresolved
    Note over S,U: Source contains call abort in internal/web/app/src/areas/console/useAsync.ts at line 57 - order unresolved
    Note over S,U: Source contains call AbortController in internal/web/app/src/areas/console/useAsync.ts at line 58 - order unresolved
    Note over S,U: Source contains call withAdminEpoch in internal/web/app/src/areas/console/useAsync.ts at line 67 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/console/useAsync.ts at line 72 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/useAsync.ts at line 72 - order unresolved
    Note over S,U: Source contains call beginRequest in internal/web/app/src/areas/console/useAsync.ts at line 73 - order unresolved
    Note over S,U: Source contains call setLoading in internal/web/app/src/areas/console/useAsync.ts at line 74 - order unresolved
    Note over S,U: Source contains call setFailed in internal/web/app/src/areas/console/useAsync.ts at line 75 - order unresolved
    Note over S,U: Source contains call current in internal/web/app/src/areas/console/useAsync.ts at line 77 - order unresolved
    Note over S,U: Source contains call current in internal/web/app/src/areas/console/useAsync.ts at line 78 - order unresolved
    Note over S,U: Source contains call setData in internal/web/app/src/areas/console/useAsync.ts at line 79 - order unresolved
    Note over S,U: Source contains call current in internal/web/app/src/areas/console/useAsync.ts at line 81 - order unresolved
    Note over S,U: Source contains call isAuthRejection in internal/web/app/src/areas/console/useAsync.ts at line 85 - order unresolved
    Note over S,U: Source contains call setData in internal/web/app/src/areas/console/useAsync.ts at line 85 - order unresolved
    Note over S,U: Source contains call setFailed in internal/web/app/src/areas/console/useAsync.ts at line 86 - order unresolved
    Note over S,U: Source contains call current in internal/web/app/src/areas/console/useAsync.ts at line 88 - order unresolved
    Note over S,U: Source contains call setLoading in internal/web/app/src/areas/console/useAsync.ts at line 88 - order unresolved
```
