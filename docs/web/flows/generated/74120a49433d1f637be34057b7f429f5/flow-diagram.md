# Backups: history, verify manifest, restore and export — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:b42224772cc1a8bd498b67110696ffb1 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/BackupExportPage\.tsx:87; symbol: BackupExportPage; span SHA256: 9eae9d9d39c2b3071703d4e5f17a9f203efd54f5aefaebeb878dab0d3c1ed978.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 74120a49433d1f637be34057b7f429f5
    Note over S,U: Source contains call useState at line 90 - order unresolved
    Note over S,U: Source contains call useT at line 126 - order unresolved
    Note over S,U: Source contains call useState at line 127 - order unresolved
    Note over S,U: Source contains call useState at line 128 - order unresolved
    Note over S,U: Source contains call useState at line 129 - order unresolved
    Note over S,U: Source contains call useState at line 130 - order unresolved
    Note over S,U: Source contains call useEpochRequest at line 140 - order unresolved
    Note over S,U: Source contains call useT at line 438 - order unresolved
    Note over S,U: Source contains call useState at line 439 - order unresolved
    Note over S,U: Source contains call useState at line 441 - order unresolved
    Note over S,U: Source contains call useEpochRequest at line 443 - order unresolved
    Note over S,U: Source contains call useT at line 354 - order unresolved
    Note over S,U: Source contains call t at line 358 - order unresolved
    Note over S,U: Source contains call t at line 359 - order unresolved
    Note over S,U: Source contains call t at line 363 - order unresolved
    Note over S,U: Source contains call t at line 369 - order unresolved
    Note over S,U: Source contains call t at line 373 - order unresolved
    Note over S,U: Source contains call manifestPath at line 375 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 378 - order unresolved
    Note over S,U: Source contains call formatBytes at line 378 - order unresolved
    Note over S,U: Source contains call t at line 389 - order unresolved
    Note over S,U: Source contains call t at line 391 - order unresolved
    Note over S,U: Source contains call useT at line 405 - order unresolved
    Note over S,U: Source contains call t at line 408 - order unresolved
    Note over S,U: Source contains call t at line 411 - order unresolved
    Note over S,U: Source contains call t at line 412 - order unresolved
    Note over S,U: Source contains call t at line 413 - order unresolved
    Note over S,U: Source contains call t at line 414 - order unresolved
    Note over S,U: Source contains call t at line 419 - order unresolved
    Note over S,U: Source contains call t at line 421 - order unresolved
    Note over S,U: Source contains call t at line 423 - order unresolved
    Note over S,U: Source contains call t at line 428 - order unresolved
    Note over S,U: Source contains call t at line 430 - order unresolved
    Note over S,U: Source contains call useT at line 339 - order unresolved
    Note over S,U: Source contains call t at line 341 - order unresolved
    Note over S,U: Source contains call t at line 344 - order unresolved
    Note over S,U: Source contains call t at line 346 - order unresolved
    Note over S,U: Source contains call min at line 81 - order unresolved
    Note over S,U: Source contains call floor at line 81 - order unresolved
    Note over S,U: Source contains call log at line 81 - order unresolved
    Note over S,U: Source contains call String at line 83 - order unresolved
    Note over S,U: Source contains call toFixed at line 83 - order unresolved
    Note over S,U: Source contains call split at line 332 - order unresolved
    Note over S,U: Source contains call filter at line 332 - order unresolved
    Note over S,U: Source contains call useRef at line 52 - order unresolved
    Note over S,U: Source contains call useEffect at line 54 - order unresolved
    Note over S,U: Source contains call abort at line 58 - order unresolved
    Note over S,U: Source contains call useCallback at line 63 - order unresolved
    Note over S,U: Source contains call abort at line 64 - order unresolved
    Note over S,U: Source contains call AbortController at line 65 - order unresolved
    Note over S,U: Source contains call withAdminEpoch at line 68 - order unresolved
    Note over S,U: Source contains call useCallback at line 142 - order unresolved
    Note over S,U: Source contains call async at line 142 - order unresolved
    Note over S,U: Source contains call trim at line 143 - order unresolved
    Note over S,U: Source contains call setError at line 145 - order unresolved
    Note over S,U: Source contains call t at line 145 - order unresolved
    Note over S,U: Source contains call beginRequest at line 148 - order unresolved
    Note over S,U: Source contains call setError at line 149 - order unresolved
    Note over S,U: Source contains call setLoading at line 150 - order unresolved
    Note over S,U: Source contains call fetchBackupHistory at line 152 - order unresolved
    Note over S,U: Source contains call current at line 153 - order unresolved
    Note over S,U: Source contains call setEntries at line 154 - order unresolved
    Note over S,U: Source contains call setLoaded at line 155 - order unresolved
    Note over S,U: Source contains call some at line 157 - order unresolved
    Note over S,U: Source contains call setSelected at line 158 - order unresolved
    Note over S,U: Source contains call current at line 161 - order unresolved
    Note over S,U: Source contains call setError at line 162 - order unresolved
    Note over S,U: Source contains call t at line 162 - order unresolved
    Note over S,U: Source contains call current at line 164 - order unresolved
    Note over S,U: Source contains call setLoading at line 164 - order unresolved
    Note over S,U: Source contains call Blob at line 465 - order unresolved
    Note over S,U: Source contains call stringify at line 465 - order unresolved
    Note over S,U: Source contains call createObjectURL at line 468 - order unresolved
    Note over S,U: Source contains call createElement at line 469 - order unresolved
    Note over S,U: Source contains call click at line 472 - order unresolved
    Note over S,U: Source contains call revokeObjectURL at line 473 - order unresolved
    Note over S,U: Source contains call async at line 445 - order unresolved
    Note over S,U: Source contains call beginRequest at line 446 - order unresolved
    Note over S,U: Source contains call setError at line 447 - order unresolved
    Note over S,U: Source contains call setBusy at line 448 - order unresolved
    Note over S,U: Source contains call fetchMetadataExport at line 450 - order unresolved
    Note over S,U: Source contains call current at line 451 - order unresolved
    Note over S,U: Source contains call setExportData at line 452 - order unresolved
    Note over S,U: Source contains call current at line 454 - order unresolved
    Note over S,U: Source contains call setError at line 455 - order unresolved
    Note over S,U: Source contains call t at line 455 - order unresolved
    Note over S,U: Source contains call current at line 457 - order unresolved
    Note over S,U: Source contains call setBusy at line 457 - order unresolved
    Note over S,U: Source contains call async at line 168 - order unresolved
    Note over S,U: Source contains call trim at line 169 - order unresolved
    Note over S,U: Source contains call setError at line 171 - order unresolved
    Note over S,U: Source contains call t at line 171 - order unresolved
    Note over S,U: Source contains call setError at line 174 - order unresolved
    Note over S,U: Source contains call setRunning at line 175 - order unresolved
    Note over S,U: Source contains call runBackup at line 177 - order unresolved
    Note over S,U: Source contains call loadHistory at line 179 - order unresolved
    Note over S,U: Source contains call setError at line 181 - order unresolved
    Note over S,U: Source contains call t at line 181 - order unresolved
    Note over S,U: Source contains call setRunning at line 183 - order unresolved
    Note over S,U: Source contains call async at line 187 - order unresolved
    Note over S,U: Source contains call setError at line 188 - order unresolved
    Note over S,U: Source contains call setVerifying at line 189 - order unresolved
    Note over S,U: Source contains call setVerifyResult at line 190 - order unresolved
    Note over S,U: Source contains call verifyBackup at line 192 - order unresolved
    Note over S,U: Source contains call manifestPath at line 192 - order unresolved
    Note over S,U: Source contains call setVerifyResult at line 193 - order unresolved
    Note over S,U: Source contains call setEntries at line 199 - order unresolved
    Note over S,U: Source contains call map at line 200 - order unresolved
    Note over S,U: Source contains call setError at line 205 - order unresolved
    Note over S,U: Source contains call t at line 205 - order unresolved
    Note over S,U: Source contains call setVerifying at line 207 - order unresolved
```
