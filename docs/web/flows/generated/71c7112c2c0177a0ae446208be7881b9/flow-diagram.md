# Console migration: importer wizard, credentials, mapping, match\-rate report — flow-diagram

Source revision: b952880cf4519beaa9738e25e349aabf600c9a41. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:665c1c7877bbd983c71af51301fe54c5 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/console/sections/RemoteAccess\.tsx:177; symbol: CATEGORY\_LABELS; span SHA256: 4aafe40cebd58b74f2169fb9edfec61aa0bc3d3a342b3a83030aca6dc535ee7e.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 71c7112c2c0177a0ae446208be7881b9
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 739 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 740 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 741 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 742 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 922 - order unresolved
    Note over S,U: Source contains call initialWizardConfig in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 927 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 929 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 930 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 931 - order unresolved
    Note over S,U: Source contains call useRef in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 940 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 810 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 816 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 817 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 821 - order unresolved
    Note over S,U: Source contains call onChange in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 824 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 826 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 827 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 830 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 831 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 840 - order unresolved
    Note over S,U: Source contains call onChange in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 843 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 845 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 846 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 849 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 850 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 868 - order unresolved
    Note over S,U: Source contains call parseImportReport in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 869 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 873 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 876 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 880 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 884 - order unresolved
    Note over S,U: Source contains call matchPercent in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 885 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 897 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 900 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 904 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1134 - order unresolved
    Note over S,U: Source contains call useDisplayLocale in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1135 - order unresolved
    Note over S,U: Source contains call useAsync in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1136 - order unresolved
    Note over S,U: Source contains call setActive in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1143 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1157 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1158 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1159 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1163 - order unresolved
    Note over S,U: Source contains call displayString in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1166 - order unresolved
    Note over S,U: Source contains call importerInputLabel in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1168 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1170 - order unresolved
    Note over S,U: Source contains call setActive in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1173 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1174 - order unresolved
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
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 942 - order unresolved
    Note over S,U: Source contains call clearInterval in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 944 - order unresolved
    Note over S,U: Source contains call setConfig in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 957 - order unresolved
    Note over S,U: Source contains call clearPoll in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1004 - order unresolved
    Note over S,U: Source contains call setPhase in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1005 - order unresolved
    Note over S,U: Source contains call setConfig in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1006 - order unresolved
    Note over S,U: Source contains call initialWizardConfig in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1006 - order unresolved
    Note over S,U: Source contains call setJob in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1007 - order unresolved
    Note over S,U: Source contains call setErrorMsg in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 1008 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 744 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 745 - order unresolved
    Note over S,U: Source contains call setSaving in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 746 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 747 - order unresolved
    Note over S,U: Source contains call setScraperCredential in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 749 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 749 - order unresolved
    Note over S,U: Source contains call setSecret in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 750 - order unresolved
    Note over S,U: Source contains call onConfigured in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 751 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 753 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 753 - order unresolved
    Note over S,U: Source contains call setSaving in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 755 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 961 - order unresolved
    Note over S,U: Source contains call setErrorMsg in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 962 - order unresolved
    Note over S,U: Source contains call setPhase in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 963 - order unresolved
    Note over S,U: Source contains call setJob in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 964 - order unresolved
    Note over S,U: Source contains call runImport in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 966 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 969 - order unresolved
    Note over S,U: Source contains call categoriesArgument in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 970 - order unresolved
    Note over S,U: Source contains call setInterval in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 974 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 975 - order unresolved
    Note over S,U: Source contains call getAdminEpochSignal in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 976 - order unresolved
    Note over S,U: Source contains call clearPoll in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 978 - order unresolved
    Note over S,U: Source contains call fetchImportStatus in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 982 - order unresolved
    Note over S,U: Source contains call setJob in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 984 - order unresolved
    Note over S,U: Source contains call isTerminalStatus in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 985 - order unresolved
    Note over S,U: Source contains call clearPoll in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 986 - order unresolved
    Note over S,U: Source contains call setPhase in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 987 - order unresolved
    Note over S,U: Source contains call setPhase in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 996 - order unresolved
    Note over S,U: Source contains call setErrorMsg in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 997 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/console/sections/RemoteAccess.tsx at line 997 - order unresolved
```
