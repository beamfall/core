# Console migration: importer wizard, credentials, mapping, match\-rate report — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:665c1c7877bbd983c71af51301fe54c5 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/RemoteAccess\.tsx:132; symbol: CATEGORY\_LABELS; span SHA256: 4aafe40cebd58b74f2169fb9edfec61aa0bc3d3a342b3a83030aca6dc535ee7e.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 71c7112c2c0177a0ae446208be7881b9
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 453 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 454 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 455 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 456 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 636 - order unresolved
    Note over S,U: Source contains call initialWizardConfig in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 641 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 643 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 644 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 645 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 654 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 524 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 530 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 531 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 535 - order unresolved
    Note over S,U: Source contains call onChange in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 538 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 540 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 541 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 544 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 545 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 554 - order unresolved
    Note over S,U: Source contains call onChange in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 557 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 559 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 560 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 563 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 564 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 582 - order unresolved
    Note over S,U: Source contains call parseImportReport in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 583 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 587 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 590 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 594 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 598 - order unresolved
    Note over S,U: Source contains call matchPercent in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 599 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 611 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 614 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 618 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 848 - order unresolved
    Note over S,U: Source contains call useDisplayLocale in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 849 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 850 - order unresolved
    Note over S,U: Source contains call setActive in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 857 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 871 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 872 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 873 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 877 - order unresolved
    Note over S,U: Source contains call displayString in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 880 - order unresolved
    Note over S,U: Source contains call importerInputLabel in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 882 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 884 - order unresolved
    Note over S,U: Source contains call setActive in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 887 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 888 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/lib/migrationReport.ts at line 48 - order unresolved
    Note over S,U: Source contains call round in internal/web/app/src/lib/migrationReport.ts at line 132 - order unresolved
    Note over S,U: Source contains call lastIndexOf in internal/web/app/src/lib/migrationReport.ts at line 112 - order unresolved
    Note over S,U: Source contains call lastIndexOf in internal/web/app/src/lib/migrationReport.ts at line 113 - order unresolved
    Note over S,U: Source contains call slice in internal/web/app/src/lib/migrationReport.ts at line 115 - order unresolved
    Note over S,U: Source contains call split in internal/web/app/src/lib/migrationReport.ts at line 117 - order unresolved
    Note over S,U: Source contains call split in internal/web/app/src/lib/migrationReport.ts at line 118 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/lib/migrationReport.ts at line 120 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/lib/migrationReport.ts at line 121 - order unresolved
    Note over S,U: Source contains call Number in internal/web/app/src/lib/migrationReport.ts at line 123 - order unresolved
    Note over S,U: Source contains call isFinite in internal/web/app/src/lib/migrationReport.ts at line 124 - order unresolved
    Note over S,U: Source contains call push in internal/web/app/src/lib/migrationReport.ts at line 125 - order unresolved
    Note over S,U: Source contains call max in internal/web/app/src/lib/migrationReport.ts at line 95 - order unresolved
    Note over S,U: Source contains call parseCategoryBreakdown in internal/web/app/src/lib/migrationReport.ts at line 102 - order unresolved
    Note over S,U: Source contains call includes in internal/web/app/src/lib/migrationReport.ts at line 36 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/lib/migrationReport.ts at line 37 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 656 - order unresolved
    Note over S,U: Source contains call clearInterval in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 658 - order unresolved
    Note over S,U: Source contains call setConfig in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 671 - order unresolved
    Note over S,U: Source contains call clearPoll in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 718 - order unresolved
    Note over S,U: Source contains call setPhase in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 719 - order unresolved
    Note over S,U: Source contains call setConfig in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 720 - order unresolved
    Note over S,U: Source contains call initialWizardConfig in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 720 - order unresolved
    Note over S,U: Source contains call setJob in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 721 - order unresolved
    Note over S,U: Source contains call setErrorMsg in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 722 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 458 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 459 - order unresolved
    Note over S,U: Source contains call setSaving in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 460 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 461 - order unresolved
    Note over S,U: Source contains call setScraperCredential in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 463 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 463 - order unresolved
    Note over S,U: Source contains call setSecret in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 464 - order unresolved
    Note over S,U: Source contains call onConfigured in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 465 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 467 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 467 - order unresolved
    Note over S,U: Source contains call setSaving in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 469 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 675 - order unresolved
    Note over S,U: Source contains call setErrorMsg in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 676 - order unresolved
    Note over S,U: Source contains call setPhase in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 677 - order unresolved
    Note over S,U: Source contains call setJob in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 678 - order unresolved
    Note over S,U: Source contains call runImport in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 680 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 683 - order unresolved
    Note over S,U: Source contains call categoriesArgument in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 684 - order unresolved
    Note over S,U: Source contains call setInterval in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 688 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 689 - order unresolved
    Note over S,U: Source contains call getAdminEpochSignal in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 690 - order unresolved
    Note over S,U: Source contains call clearPoll in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 692 - order unresolved
    Note over S,U: Source contains call fetchImportStatus in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 696 - order unresolved
    Note over S,U: Source contains call setJob in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 698 - order unresolved
    Note over S,U: Source contains call isTerminalStatus in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 699 - order unresolved
    Note over S,U: Source contains call clearPoll in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 700 - order unresolved
    Note over S,U: Source contains call setPhase in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 701 - order unresolved
    Note over S,U: Source contains call setPhase in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 710 - order unresolved
    Note over S,U: Source contains call setErrorMsg in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 711 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 711 - order unresolved
```
