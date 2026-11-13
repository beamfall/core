# Manage restrictions — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:81591007b037041f06a2388546b4035c -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/RestrictionsPage\.tsx:16; symbol: RestrictionsPage; span SHA256: 417dbc6d2a1bbd8599adf1cdd5447cc8a1ddd58b2d00abca38ee259921f56a2a.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 06804cd2e2106358c566b86268baca0f
    Note over S,U: Source contains call useT at line 17 - order unresolved
    Note over S,U: Source contains call useSession at line 18 - order unresolved
    Note over S,U: Source contains call useState at line 19 - order unresolved
    Note over S,U: Source contains call useState at line 20 - order unresolved
    Note over S,U: Source contains call useState at line 21 - order unresolved
    Note over S,U: Source contains call async at line 44 - order unresolved
    Note over S,U: Source contains call setBusy at line 45 - order unresolved
    Note over S,U: Source contains call setError at line 46 - order unresolved
    Note over S,U: Source contains call relock at line 48 - order unresolved
    Note over S,U: Source contains call setBusy at line 50 - order unresolved
    Note over S,U: Source contains call async at line 23 - order unresolved
    Note over S,U: Source contains call preventDefault at line 24 - order unresolved
    Note over S,U: Source contains call setError at line 25 - order unresolved
    Note over S,U: Source contains call setPin at line 27 - order unresolved
    Note over S,U: Source contains call setBusy at line 29 - order unresolved
    Note over S,U: Source contains call reveal at line 31 - order unresolved
    Note over S,U: Source contains call setError at line 32 - order unresolved
    Note over S,U: Source contains call t at line 32 - order unresolved
    Note over S,U: Source contains call setError at line 34 - order unresolved
    Note over S,U: Source contains call t at line 36 - order unresolved
    Note over S,U: Source contains call t at line 37 - order unresolved
    Note over S,U: Source contains call setBusy at line 40 - order unresolved
```
