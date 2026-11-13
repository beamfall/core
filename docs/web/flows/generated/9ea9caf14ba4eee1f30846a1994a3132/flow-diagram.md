# Manage unmatched records: inbox, fix match, suppression — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:d3397ef2a6658591a75180b326e3647d -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/UnmatchedPage\.tsx:40; symbol: UnmatchedPage; span SHA256: 291cd556681df34fe7de2db62444f5e8829fef17ccee656c8b5c8514585cd6ce.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 9ea9caf14ba4eee1f30846a1994a3132
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 41 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 44 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 45 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 46 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 47 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 48 - order unresolved
    Note over S,U: Source contains call useMemo in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 50 - order unresolved
    Note over S,U: Source contains call find in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 51 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 29 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 30 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 32 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 33 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 35 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 20 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 23 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 24 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 25 - order unresolved
    Note over S,U: Source contains call useT in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 70 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 72 - order unresolved
    Note over S,U: Source contains call useState in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 73 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 75 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 76 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 77 - order unresolved
    Note over S,U: Source contains call action in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 79 - order unresolved
    Note over S,U: Source contains call onChanged in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 80 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 84 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 84 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 86 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 92 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 95 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 97 - order unresolved
    Note over S,U: Source contains call setScope in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 105 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 108 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 109 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 112 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 128 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 132 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 134 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 137 - order unresolved
    Note over S,U: Source contains call run in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 146 - order unresolved
    Note over S,U: Source contains call resetUnmatchedSuppression in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 147 - order unresolved
    Note over S,U: Source contains call suppressionTarget in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 148 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 153 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 157 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/SuppressionPanel.tsx at line 158 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 55 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 55 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 56 - order unresolved
    Note over S,U: Source contains call setLoading in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 57 - order unresolved
    Note over S,U: Source contains call all in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 59 - order unresolved
    Note over S,U: Source contains call fetchUnmatchedQueue in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 59 - order unresolved
    Note over S,U: Source contains call fetchLibraries in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 59 - order unresolved
    Note over S,U: Source contains call setItems in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 60 - order unresolved
    Note over S,U: Source contains call setLibraries in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 61 - order unresolved
    Note over S,U: Source contains call setActiveID in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 62 - order unresolved
    Note over S,U: Source contains call setLibraryID in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 63 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 65 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 65 - order unresolved
    Note over S,U: Source contains call setLoading in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 67 - order unresolved
    Note over S,U: Source contains call setItems in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 88 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 88 - order unresolved
    Note over S,U: Source contains call setActiveID in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 89 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 95 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 96 - order unresolved
    Note over S,U: Source contains call allSettled in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 97 - order unresolved
    Note over S,U: Source contains call map in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 97 - order unresolved
    Note over S,U: Source contains call dismissUnmatched in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 97 - order unresolved
    Note over S,U: Source contains call setActiveID in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 98 - order unresolved
    Note over S,U: Source contains call load in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 99 - order unresolved
    Note over S,U: Source contains call some in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 100 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 100 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 100 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 76 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 77 - order unresolved
    Note over S,U: Source contains call dismissUnmatched in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 79 - order unresolved
    Note over S,U: Source contains call setItems in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 80 - order unresolved
    Note over S,U: Source contains call filter in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 80 - order unresolved
    Note over S,U: Source contains call setActiveID in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 81 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 83 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 83 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 103 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 105 - order unresolved
    Note over S,U: Source contains call setRescanMsg in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 106 - order unresolved
    Note over S,U: Source contains call runHealthBulkOp in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 108 - order unresolved
    Note over S,U: Source contains call setRescanMsg in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 109 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 111 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/UnmatchedPage.tsx at line 111 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 101 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 103 - order unresolved
    Note over S,U: Source contains call fixMatch in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 105 - order unresolved
    Note over S,U: Source contains call onApplied in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 106 - order unresolved
    Note over S,U: Source contains call onError in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 108 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 108 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 110 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 65 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 66 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 68 - order unresolved
    Note over S,U: Source contains call identifyLookup in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 70 - order unresolved
    Note over S,U: Source contains call setCandidates in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 71 - order unresolved
    Note over S,U: Source contains call select in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 81 - order unresolved
    Note over S,U: Source contains call onError in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 83 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 83 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 85 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 89 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 91 - order unresolved
    Note over S,U: Source contains call setDiff in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 93 - order unresolved
    Note over S,U: Source contains call fixMatchPreview in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 93 - order unresolved
    Note over S,U: Source contains call onError in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 95 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 95 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 97 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 47 - order unresolved
    Note over S,U: Source contains call trim in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 48 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 50 - order unresolved
    Note over S,U: Source contains call fixMatchCandidates in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 52 - order unresolved
    Note over S,U: Source contains call setCandidates in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 53 - order unresolved
    Note over S,U: Source contains call select in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 54 - order unresolved
    Note over S,U: Source contains call onError in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 56 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 56 - order unresolved
    Note over S,U: Source contains call setBusy in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 58 - order unresolved
    Note over S,U: Source contains call setSelectedID in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 43 - order unresolved
    Note over S,U: Source contains call setDiff in internal/web/app/src/areas/manage/pages/unmatched/FixMatchPanel.tsx at line 44 - order unresolved
    Note over S,U: Source contains call useCallback in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 27 - order unresolved
    Note over S,U: Source contains call async in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 27 - order unresolved
    Note over S,U: Source contains call all in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 29 - order unresolved
    Note over S,U: Source contains call fetchUnmatchedRecords in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 30 - order unresolved
    Note over S,U: Source contains call fetchUnmatchedSuppressions in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 31 - order unresolved
    Note over S,U: Source contains call setRecords in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 33 - order unresolved
    Note over S,U: Source contains call setSuppressions in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 34 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 35 - order unresolved
    Note over S,U: Source contains call setError in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 37 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 37 - order unresolved
    Note over S,U: Source contains call setNotice in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 48 - order unresolved
    Note over S,U: Source contains call t in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 48 - order unresolved
    Note over S,U: Source contains call setActiveID in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 49 - order unresolved
    Note over S,U: Source contains call load in internal/web/app/src/areas/manage/pages/unmatched/RecordInbox.tsx at line 50 - order unresolved
```
