# Create a Media from an unmatched record and upload artwork — flow-diagram

Source revision: d442b4a6a38de86be0670669b3a3f6aa47e80b22. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:807561dd4a10701ef7f48396cf60ca6c -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/pages/unmatched/CreateMediaForm\.tsx:23; symbol: uploadArtwork; span SHA256: ab684484ef0e920778940bc7d20da087174859abf997a4670127ae7a91bffe39.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6c5316a98e80fcee0fad200be1034235
    Note over S,U: Source contains call getIdentityEpochSignal at line 111 - order unresolved
    Note over S,U: Source contains call async at line 118 - order unresolved
    Note over S,U: Source contains call captureGeneration at line 119 - order unresolved
    Note over S,U: Source contains call trim at line 120 - order unresolved
    Note over S,U: Source contains call validateYear at line 121 - order unresolved
    Note over S,U: Source contains call setTitleError at line 122 - order unresolved
    Note over S,U: Source contains call t at line 122 - order unresolved
    Note over S,U: Source contains call setYearError at line 123 - order unresolved
    Note over S,U: Source contains call t at line 123 - order unresolved
    Note over S,U: Source contains call setBusy at line 128 - order unresolved
    Note over S,U: Source contains call setSaveError at line 129 - order unresolved
    Note over S,U: Source contains call createMediaFromUnmatched at line 131 - order unresolved
    Note over S,U: Source contains call uploadArtwork at line 139 - order unresolved
    Note over S,U: Source contains call current at line 143 - order unresolved
    Note over S,U: Source contains call setArtworkError at line 147 - order unresolved
    Note over S,U: Source contains call current at line 155 - order unresolved
    Note over S,U: Source contains call onCreated at line 156 - order unresolved
    Note over S,U: Source contains call current at line 158 - order unresolved
    Note over S,U: Source contains call setSaveError at line 161 - order unresolved
    Note over S,U: Source contains call t at line 161 - order unresolved
    Note over S,U: Source contains call current at line 163 - order unresolved
    Note over S,U: Source contains call setBusy at line 163 - order unresolved
    Note over S,U: Source contains call async at line 167 - order unresolved
    Note over S,U: Source contains call captureGeneration at line 172 - order unresolved
    Note over S,U: Source contains call setBusy at line 173 - order unresolved
    Note over S,U: Source contains call uploadArtwork at line 175 - order unresolved
    Note over S,U: Source contains call current at line 176 - order unresolved
    Note over S,U: Source contains call onCreated at line 177 - order unresolved
    Note over S,U: Source contains call current at line 179 - order unresolved
    Note over S,U: Source contains call setArtworkError at line 180 - order unresolved
    Note over S,U: Source contains call current at line 186 - order unresolved
    Note over S,U: Source contains call setBusy at line 186 - order unresolved
```
