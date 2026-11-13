# Localization: locale resolution, ICU formatting and string catalogs — flow-diagram

Source revision: e417fb394873a04fbd27fb8d5bcbca5484c8747f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

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
    Note over S,U: Source contains call splitTopLevel at line 155 - order unresolved
    Note over S,U: Source contains call trim at line 156 - order unresolved
    Note over S,U: Source contains call trim at line 158 - order unresolved
    Note over S,U: Source contains call formatNumberFor at line 159 - order unresolved
    Note over S,U: Source contains call Number at line 159 - order unresolved
    Note over S,U: Source contains call Number at line 161 - order unresolved
    Note over S,U: Source contains call parsePluralOptions at line 162 - order unresolved
    Note over S,U: Source contains call slice at line 162 - order unresolved
    Note over S,U: Source contains call join at line 162 - order unresolved
    Note over S,U: Source contains call selectPluralCategory at line 164 - order unresolved
    Note over S,U: Source contains call formatMessageICU at line 165 - order unresolved
    Note over S,U: Source contains call String at line 167 - order unresolved
    Note over S,U: Source contains call NumberFormat at line 69 - order unresolved
    Note over S,U: Source contains call format at line 69 - order unresolved
    Note over S,U: Source contains call NumberFormat at line 71 - order unresolved
    Note over S,U: Source contains call format at line 71 - order unresolved
    Note over S,U: Source contains call includes at line 24 - order unresolved
    Note over S,U: Source contains call readGroup at line 147 - order unresolved
    Note over S,U: Source contains call trim at line 148 - order unresolved
    Note over S,U: Source contains call slice at line 101 - order unresolved
    Note over S,U: Source contains call slice at line 104 - order unresolved
    Note over S,U: Source contains call resolveLocalePreference at line 50 - order unresolved
    Note over S,U: Source contains call PluralRules at line 77 - order unresolved
    Note over S,U: Source contains call select at line 77 - order unresolved
    Note over S,U: Source contains call PluralRules at line 79 - order unresolved
    Note over S,U: Source contains call select at line 79 - order unresolved
    Note over S,U: Source contains call push at line 129 - order unresolved
    Note over S,U: Source contains call push at line 135 - order unresolved
    Note over S,U: Source contains call useI18n at line 282 - order unresolved
    Note over S,U: Source contains call useContext at line 271 - order unresolved
    Note over S,U: Source contains call Error at line 272 - order unresolved
    Note over S,U: Source contains call useI18n at line 278 - order unresolved
    Note over S,U: Source contains call setPreference at line 266 - order unresolved
    Note over S,U: Source contains call String at line 257 - order unresolved
    Note over S,U: Source contains call formatMessage at line 258 - order unresolved
```
