# Manage area shell: route table, nav groups, admin lock — flow-diagram

Source revision: 4d9f6ba81afcc8de0179eb7c056488d3e3bd8218. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:6d8542ad14b6a67b4e935b8d909e9802 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/index\.tsx:279; symbol: AdminUnlocked; span SHA256: 2fbb9c781a0203abe431258f0a69e258cc97a537c32fb8a006ec511fe1ae2a91.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6236381b2381a125e7d325200023a2b4
    Note over S,U: Source contains call useEffect at line 280 - order unresolved
    Note over S,U: Source contains call onChange at line 281 - order unresolved
    Note over S,U: Source contains call onChange at line 282 - order unresolved
    Note over S,U: Source contains call useLocation at line 218 - order unresolved
    Note over S,U: Source contains call useRegister at line 288 - order unresolved
    Note over S,U: Source contains call useLocation at line 289 - order unresolved
    Note over S,U: Source contains call useSession at line 290 - order unresolved
    Note over S,U: Source contains call useT at line 291 - order unresolved
    Note over S,U: Source contains call useAdvancedSettings at line 292 - order unresolved
    Note over S,U: Source contains call useEffect at line 293 - order unresolved
    Note over S,U: Source contains call setRegister at line 293 - order unresolved
    Note over S,U: Source contains call currentRoute at line 294 - order unresolved
    Note over S,U: Source contains call t at line 295 - order unresolved
    Note over S,U: Source contains call t at line 300 - order unresolved
    Note over S,U: Source contains call t at line 313 - order unresolved
    Note over S,U: Source contains call visibleNavGroups at line 319 - order unresolved
    Note over S,U: Source contains call map at line 319 - order unresolved
    Note over S,U: Source contains call map at line 321 - order unresolved
    Note over S,U: Source contains call t at line 327 - order unresolved
    Note over S,U: Source contains call t at line 337 - order unresolved
    Note over S,U: Source contains call useState at line 227 - order unresolved
    Note over S,U: Source contains call useRef at line 228 - order unresolved
    Note over S,U: Source contains call flat at line 184 - order unresolved
    Note over S,U: Source contains call find at line 184 - order unresolved
    Note over S,U: Source contains call startsWith at line 185 - order unresolved
    Note over S,U: Source contains call useCallback at line 231 - order unresolved
    Note over S,U: Source contains call splice at line 232 - order unresolved
    Note over S,U: Source contains call resolve at line 232 - order unresolved
```
