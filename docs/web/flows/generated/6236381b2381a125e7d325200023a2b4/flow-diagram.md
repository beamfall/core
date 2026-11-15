# Manage area shell: route table, nav groups, admin lock — flow-diagram

Source revision: 03e521f81ef085ec14c6d7d5d76533692ecba36c. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:6d8542ad14b6a67b4e935b8d909e9802 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/index\.tsx:209; symbol: AdminUnlocked; span SHA256: 2fbb9c781a0203abe431258f0a69e258cc97a537c32fb8a006ec511fe1ae2a91.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6236381b2381a125e7d325200023a2b4
    Note over S,U: Source contains call useEffect at line 210 - order unresolved
    Note over S,U: Source contains call onChange at line 211 - order unresolved
    Note over S,U: Source contains call onChange at line 212 - order unresolved
    Note over S,U: Source contains call useLocation at line 148 - order unresolved
    Note over S,U: Source contains call useRegister at line 218 - order unresolved
    Note over S,U: Source contains call useLocation at line 219 - order unresolved
    Note over S,U: Source contains call useSession at line 220 - order unresolved
    Note over S,U: Source contains call useT at line 221 - order unresolved
    Note over S,U: Source contains call useAdvancedSettings at line 222 - order unresolved
    Note over S,U: Source contains call useEffect at line 223 - order unresolved
    Note over S,U: Source contains call setRegister at line 223 - order unresolved
    Note over S,U: Source contains call currentRouteLabel at line 224 - order unresolved
    Note over S,U: Source contains call t at line 229 - order unresolved
    Note over S,U: Source contains call t at line 242 - order unresolved
    Note over S,U: Source contains call t at line 244 - order unresolved
    Note over S,U: Source contains call visibleNavGroups at line 248 - order unresolved
    Note over S,U: Source contains call map at line 248 - order unresolved
    Note over S,U: Source contains call map at line 250 - order unresolved
    Note over S,U: Source contains call t at line 256 - order unresolved
    Note over S,U: Source contains call t at line 266 - order unresolved
    Note over S,U: Source contains call useState at line 157 - order unresolved
    Note over S,U: Source contains call useRef at line 158 - order unresolved
    Note over S,U: Source contains call flat at line 114 - order unresolved
    Note over S,U: Source contains call find at line 114 - order unresolved
    Note over S,U: Source contains call startsWith at line 115 - order unresolved
    Note over S,U: Source contains call useCallback at line 161 - order unresolved
    Note over S,U: Source contains call splice at line 162 - order unresolved
    Note over S,U: Source contains call resolve at line 162 - order unresolved
```
