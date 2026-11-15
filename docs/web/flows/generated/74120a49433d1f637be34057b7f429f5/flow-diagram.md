# Backups: history, verify manifest, restore and export — flow-diagram

Source revision: 25a58b2a60b350f2a582cb407acbbbc52274a39b. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:b42224772cc1a8bd498b67110696ffb1 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/BackupExportPage\.tsx:106; symbol: BackupExportPage; span SHA256: 9eae9d9d39c2b3071703d4e5f17a9f203efd54f5aefaebeb878dab0d3c1ed978.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 74120a49433d1f637be34057b7f429f5
    Note over S,U: Source contains call useState at line 109 - order unresolved
    Note over S,U: Source contains call useT at line 145 - order unresolved
    Note over S,U: Source contains call useState at line 146 - order unresolved
    Note over S,U: Source contains call useState at line 147 - order unresolved
    Note over S,U: Source contains call useState at line 148 - order unresolved
    Note over S,U: Source contains call useState at line 149 - order unresolved
    Note over S,U: Source contains call useEpochRequest at line 159 - order unresolved
    Note over S,U: Source contains call useT at line 710 - order unresolved
    Note over S,U: Source contains call useState at line 711 - order unresolved
    Note over S,U: Source contains call useState at line 713 - order unresolved
    Note over S,U: Source contains call useEpochRequest at line 715 - order unresolved
    Note over S,U: Source contains call useT at line 373 - order unresolved
    Note over S,U: Source contains call t at line 377 - order unresolved
    Note over S,U: Source contains call t at line 378 - order unresolved
    Note over S,U: Source contains call t at line 382 - order unresolved
    Note over S,U: Source contains call t at line 388 - order unresolved
    Note over S,U: Source contains call t at line 392 - order unresolved
    Note over S,U: Source contains call manifestPath at line 394 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 397 - order unresolved
    Note over S,U: Source contains call formatBytes at line 397 - order unresolved
    Note over S,U: Source contains call t at line 408 - order unresolved
    Note over S,U: Source contains call t at line 410 - order unresolved
    Note over S,U: Source contains call useT at line 423 - order unresolved
    Note over S,U: Source contains call t at line 426 - order unresolved
    Note over S,U: Source contains call t at line 430 - order unresolved
    Note over S,U: Source contains call t at line 432 - order unresolved
    Note over S,U: Source contains call t at line 433 - order unresolved
    Note over S,U: Source contains call t at line 434 - order unresolved
    Note over S,U: Source contains call t at line 435 - order unresolved
    Note over S,U: Source contains call t at line 440 - order unresolved
    Note over S,U: Source contains call t at line 442 - order unresolved
    Note over S,U: Source contains call t at line 444 - order unresolved
    Note over S,U: Source contains call t at line 449 - order unresolved
    Note over S,U: Source contains call t at line 451 - order unresolved
    Note over S,U: Source contains call useT at line 358 - order unresolved
    Note over S,U: Source contains call t at line 360 - order unresolved
    Note over S,U: Source contains call t at line 363 - order unresolved
    Note over S,U: Source contains call t at line 365 - order unresolved
    Note over S,U: Source contains call min at line 100 - order unresolved
    Note over S,U: Source contains call floor at line 100 - order unresolved
    Note over S,U: Source contains call log at line 100 - order unresolved
    Note over S,U: Source contains call String at line 102 - order unresolved
    Note over S,U: Source contains call toFixed at line 102 - order unresolved
    Note over S,U: Source contains call split at line 351 - order unresolved
    Note over S,U: Source contains call filter at line 351 - order unresolved
    Note over S,U: Source contains call useRef at line 71 - order unresolved
    Note over S,U: Source contains call useEffect at line 73 - order unresolved
    Note over S,U: Source contains call abort at line 77 - order unresolved
    Note over S,U: Source contains call useCallback at line 82 - order unresolved
    Note over S,U: Source contains call abort at line 83 - order unresolved
    Note over S,U: Source contains call AbortController at line 84 - order unresolved
    Note over S,U: Source contains call withAdminEpoch at line 87 - order unresolved
    Note over S,U: Source contains call useCallback at line 161 - order unresolved
    Note over S,U: Source contains call async at line 161 - order unresolved
    Note over S,U: Source contains call trim at line 162 - order unresolved
    Note over S,U: Source contains call setError at line 164 - order unresolved
    Note over S,U: Source contains call t at line 164 - order unresolved
    Note over S,U: Source contains call beginRequest at line 167 - order unresolved
    Note over S,U: Source contains call setError at line 168 - order unresolved
    Note over S,U: Source contains call setLoading at line 169 - order unresolved
    Note over S,U: Source contains call fetchBackupHistory at line 171 - order unresolved
    Note over S,U: Source contains call current at line 172 - order unresolved
    Note over S,U: Source contains call setEntries at line 173 - order unresolved
    Note over S,U: Source contains call setLoaded at line 174 - order unresolved
    Note over S,U: Source contains call some at line 176 - order unresolved
    Note over S,U: Source contains call setSelected at line 177 - order unresolved
    Note over S,U: Source contains call current at line 180 - order unresolved
    Note over S,U: Source contains call setError at line 181 - order unresolved
    Note over S,U: Source contains call t at line 181 - order unresolved
    Note over S,U: Source contains call current at line 183 - order unresolved
    Note over S,U: Source contains call setLoading at line 183 - order unresolved
    Note over S,U: Source contains call Blob at line 737 - order unresolved
    Note over S,U: Source contains call stringify at line 737 - order unresolved
    Note over S,U: Source contains call createObjectURL at line 740 - order unresolved
    Note over S,U: Source contains call createElement at line 741 - order unresolved
    Note over S,U: Source contains call click at line 744 - order unresolved
    Note over S,U: Source contains call revokeObjectURL at line 745 - order unresolved
    Note over S,U: Source contains call async at line 717 - order unresolved
    Note over S,U: Source contains call beginRequest at line 718 - order unresolved
    Note over S,U: Source contains call setError at line 719 - order unresolved
    Note over S,U: Source contains call setBusy at line 720 - order unresolved
    Note over S,U: Source contains call fetchMetadataExport at line 722 - order unresolved
    Note over S,U: Source contains call current at line 723 - order unresolved
    Note over S,U: Source contains call setExportData at line 724 - order unresolved
    Note over S,U: Source contains call current at line 726 - order unresolved
    Note over S,U: Source contains call setError at line 727 - order unresolved
    Note over S,U: Source contains call t at line 727 - order unresolved
    Note over S,U: Source contains call current at line 729 - order unresolved
    Note over S,U: Source contains call setBusy at line 729 - order unresolved
    Note over S,U: Source contains call async at line 187 - order unresolved
    Note over S,U: Source contains call trim at line 188 - order unresolved
    Note over S,U: Source contains call setError at line 190 - order unresolved
    Note over S,U: Source contains call t at line 190 - order unresolved
    Note over S,U: Source contains call setError at line 193 - order unresolved
    Note over S,U: Source contains call setRunning at line 194 - order unresolved
    Note over S,U: Source contains call runBackup at line 196 - order unresolved
    Note over S,U: Source contains call loadHistory at line 198 - order unresolved
    Note over S,U: Source contains call setError at line 200 - order unresolved
    Note over S,U: Source contains call t at line 200 - order unresolved
    Note over S,U: Source contains call setRunning at line 202 - order unresolved
    Note over S,U: Source contains call async at line 206 - order unresolved
    Note over S,U: Source contains call setError at line 207 - order unresolved
    Note over S,U: Source contains call setVerifying at line 208 - order unresolved
    Note over S,U: Source contains call setVerifyResult at line 209 - order unresolved
    Note over S,U: Source contains call verifyBackup at line 211 - order unresolved
    Note over S,U: Source contains call manifestPath at line 211 - order unresolved
    Note over S,U: Source contains call setVerifyResult at line 212 - order unresolved
    Note over S,U: Source contains call setEntries at line 218 - order unresolved
    Note over S,U: Source contains call map at line 219 - order unresolved
    Note over S,U: Source contains call setError at line 224 - order unresolved
    Note over S,U: Source contains call t at line 224 - order unresolved
    Note over S,U: Source contains call setVerifying at line 226 - order unresolved
```
