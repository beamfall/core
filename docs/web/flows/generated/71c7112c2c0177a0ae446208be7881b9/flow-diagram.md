# Console migration: importer wizard, credentials, mapping, match\-rate report — flow-diagram

Source revision: 25a58b2a60b350f2a582cb407acbbbc52274a39b. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:665c1c7877bbd983c71af51301fe54c5 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/RemoteAccess\.tsx:134; symbol: CATEGORY\_LABELS; span SHA256: 4aafe40cebd58b74f2169fb9edfec61aa0bc3d3a342b3a83030aca6dc535ee7e.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 71c7112c2c0177a0ae446208be7881b9
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 526 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 527 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 528 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 529 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 709 - order unresolved
    Note over S,U: Source contains call initialWizardConfig in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 714 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 716 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 717 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 718 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 727 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 597 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 603 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 604 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 608 - order unresolved
    Note over S,U: Source contains call onChange in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 611 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 613 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 614 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 617 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 618 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 627 - order unresolved
    Note over S,U: Source contains call onChange in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 630 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 632 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 633 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 636 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 637 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 655 - order unresolved
    Note over S,U: Source contains call parseImportReport in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 656 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 660 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 663 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 667 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 671 - order unresolved
    Note over S,U: Source contains call matchPercent in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 672 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 684 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 687 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 691 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 921 - order unresolved
    Note over S,U: Source contains call useDisplayLocale in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 922 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 923 - order unresolved
    Note over S,U: Source contains call setActive in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 930 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 944 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 945 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 946 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 950 - order unresolved
    Note over S,U: Source contains call displayString in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 953 - order unresolved
    Note over S,U: Source contains call importerInputLabel in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 955 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 957 - order unresolved
    Note over S,U: Source contains call setActive in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 960 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 961 - order unresolved
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
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 729 - order unresolved
    Note over S,U: Source contains call clearInterval in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 731 - order unresolved
    Note over S,U: Source contains call setConfig in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 744 - order unresolved
    Note over S,U: Source contains call clearPoll in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 791 - order unresolved
    Note over S,U: Source contains call setPhase in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 792 - order unresolved
    Note over S,U: Source contains call setConfig in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 793 - order unresolved
    Note over S,U: Source contains call initialWizardConfig in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 793 - order unresolved
    Note over S,U: Source contains call setJob in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 794 - order unresolved
    Note over S,U: Source contains call setErrorMsg in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 795 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 531 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 532 - order unresolved
    Note over S,U: Source contains call setSaving in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 533 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 534 - order unresolved
    Note over S,U: Source contains call setScraperCredential in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 536 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 536 - order unresolved
    Note over S,U: Source contains call setSecret in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 537 - order unresolved
    Note over S,U: Source contains call onConfigured in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 538 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 540 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 540 - order unresolved
    Note over S,U: Source contains call setSaving in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 542 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 748 - order unresolved
    Note over S,U: Source contains call setErrorMsg in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 749 - order unresolved
    Note over S,U: Source contains call setPhase in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 750 - order unresolved
    Note over S,U: Source contains call setJob in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 751 - order unresolved
    Note over S,U: Source contains call runImport in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 753 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 756 - order unresolved
    Note over S,U: Source contains call categoriesArgument in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 757 - order unresolved
    Note over S,U: Source contains call setInterval in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 761 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 762 - order unresolved
    Note over S,U: Source contains call getAdminEpochSignal in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 763 - order unresolved
    Note over S,U: Source contains call clearPoll in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 765 - order unresolved
    Note over S,U: Source contains call fetchImportStatus in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 769 - order unresolved
    Note over S,U: Source contains call setJob in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 771 - order unresolved
    Note over S,U: Source contains call isTerminalStatus in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 772 - order unresolved
    Note over S,U: Source contains call clearPoll in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 773 - order unresolved
    Note over S,U: Source contains call setPhase in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 774 - order unresolved
    Note over S,U: Source contains call setPhase in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 783 - order unresolved
    Note over S,U: Source contains call setErrorMsg in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 784 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 784 - order unresolved
```
