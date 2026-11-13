# Localization: locale resolution, ICU formatting and string catalogs — flow-diagram

Source revision: 5ffe7939da2318f5153b1a20d0ef6ef94deef162. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:70687676a287479a4e9d27b342b665f8 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/lib/i18n\.tsx:231; symbol: I18nProvider; span SHA256: bc8286cce6e090a1c7b6445fc9e39dff33a84f01f997f60efdea408ac65ea981.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 8ff34043846006c5450fec0ee31d31d5
    Note over S,U: Source contains call resolveBrowserLocalePreference at line 247 - order unresolved
    Note over S,U: Source contains call useI18n at line 282 - order unresolved
    Note over S,U: Source contains call useContext at line 271 - order unresolved
    Note over S,U: Source contains call Error at line 272 - order unresolved
    Note over S,U: Source contains call useI18n at line 278 - order unresolved
    Note over S,U: Source contains call setPreference at line 266 - order unresolved
    Note over S,U: Source contains call String at line 257 - order unresolved
    Note over S,U: Source contains call formatMessage at line 258 - order unresolved
```
