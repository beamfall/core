# Console area shell: route table, section nav, host firewall, session bar — flow-diagram

Source revision: 314d15477e23e7491b4680a9142d17833b11db50. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:fad205a35a982626c5b6b92999240d7d -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/ConfirmButton\.tsx:23; symbol: ConfirmButton; span SHA256: fd1970dc9047b48eb27240271171b21b6270a622a3bb61edbb1acec520d7eb7a.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow ccbbc5b36c72164a980ec4ace2721c52
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/ConfirmButton.tsx at line 44 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/ConfirmButton.tsx at line 45 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/ConfirmButton.tsx at line 46 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/console/ConfirmButton.tsx at line 49 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/Section.tsx at line 20 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/Section.tsx at line 25 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/Section.tsx at line 26 - order unresolved
    Note over S,U: Source contains call any in internal/web/app/src/areas/console/adminEpoch.ts at line 38 - order unresolved
    Note over S,U: Source contains call Set in internal/web/app/src/areas/console/index.tsx at line 405 - order unresolved
    Note over S,U: Source contains call useRegister in internal/web/app/src/areas/console/index.tsx at line 495 - order unresolved
    Note over S,U: Source contains call useSession in internal/web/app/src/areas/console/index.tsx at line 496 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/index.tsx at line 497 - order unresolved
    Note over S,U: Source contains call useLocation in internal/web/app/src/areas/console/index.tsx at line 498 - order unresolved
    Note over S,U: Source contains call groupsForHost in internal/web/app/src/areas/console/index.tsx at line 499 - order unresolved
    Note over S,U: Source contains call isBeamfallAppStoreHost in internal/web/app/src/areas/console/index.tsx at line 499 - order unresolved
    Note over S,U: Source contains call flatMap in internal/web/app/src/areas/console/index.tsx at line 500 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/console/index.tsx at line 501 - order unresolved
    Note over S,U: Source contains call test in internal/web/app/src/areas/console/index.tsx at line 503 - order unresolved
    Note over S,U: Source contains call useEffect in internal/web/app/src/areas/console/index.tsx at line 507 - order unresolved
    Note over S,U: Source contains call setRegister in internal/web/app/src/areas/console/index.tsx at line 507 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 512 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 529 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 534 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 536 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 543 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 546 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/index.tsx at line 458 - order unresolved
    Note over S,U: Source contains call useLocation in internal/web/app/src/areas/console/index.tsx at line 459 - order unresolved
    Note over S,U: Source contains call useAdvancedSettings in internal/web/app/src/areas/console/index.tsx at line 460 - order unresolved
    Note over S,U: Source contains call visibleNavGroups in internal/web/app/src/areas/console/index.tsx at line 463 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 463 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 465 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 466 - order unresolved
    Note over S,U: Source contains call cn in internal/web/app/src/areas/console/index.tsx at line 473 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 476 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/index.tsx at line 429 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 435 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/index.tsx at line 439 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 409 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/index.tsx at line 412 - order unresolved
    Note over S,U: Source contains call has in internal/web/app/src/areas/console/index.tsx at line 412 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/index.tsx at line 413 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/console/index.tsx at line 421 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/console/ConfirmButton.tsx at line 61 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/console/ConfirmButton.tsx at line 62 - order unresolved
    Note over S,U: Source contains call onConfirm in internal/web/app/src/areas/console/ConfirmButton.tsx at line 64 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/console/ConfirmButton.tsx at line 66 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/console/ConfirmButton.tsx at line 70 - order unresolved
    Note over S,U: Source contains call setOpen in internal/web/app/src/areas/console/ConfirmButton.tsx at line 72 - order unresolved
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
