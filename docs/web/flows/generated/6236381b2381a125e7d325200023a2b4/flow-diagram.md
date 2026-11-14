# Manage area shell: route table, nav groups, admin lock — flow-diagram

Source revision: 6742c2361d684c662e9ccb84d344a2200c377582. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:6d8542ad14b6a67b4e935b8d909e9802 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/index\.tsx:206; symbol: AdminUnlocked; span SHA256: 2fbb9c781a0203abe431258f0a69e258cc97a537c32fb8a006ec511fe1ae2a91.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6236381b2381a125e7d325200023a2b4
    Note over S,U: Source contains call useEffect at line 207 - order unresolved
    Note over S,U: Source contains call onChange at line 208 - order unresolved
    Note over S,U: Source contains call onChange at line 209 - order unresolved
    Note over S,U: Source contains call useLocation at line 145 - order unresolved
    Note over S,U: Source contains call useRegister at line 215 - order unresolved
    Note over S,U: Source contains call useLocation at line 216 - order unresolved
    Note over S,U: Source contains call useSession at line 217 - order unresolved
    Note over S,U: Source contains call useT at line 218 - order unresolved
    Note over S,U: Source contains call useAdvancedSettings at line 219 - order unresolved
    Note over S,U: Source contains call useEffect at line 220 - order unresolved
    Note over S,U: Source contains call setRegister at line 220 - order unresolved
    Note over S,U: Source contains call currentRouteLabel at line 221 - order unresolved
    Note over S,U: Source contains call t at line 226 - order unresolved
    Note over S,U: Source contains call t at line 239 - order unresolved
    Note over S,U: Source contains call t at line 241 - order unresolved
    Note over S,U: Source contains call visibleNavGroups at line 245 - order unresolved
    Note over S,U: Source contains call map at line 245 - order unresolved
    Note over S,U: Source contains call map at line 247 - order unresolved
    Note over S,U: Source contains call t at line 253 - order unresolved
    Note over S,U: Source contains call t at line 263 - order unresolved
    Note over S,U: Source contains call useState at line 154 - order unresolved
    Note over S,U: Source contains call useRef at line 155 - order unresolved
    Note over S,U: Source contains call flat at line 111 - order unresolved
    Note over S,U: Source contains call find at line 111 - order unresolved
    Note over S,U: Source contains call startsWith at line 112 - order unresolved
    Note over S,U: Source contains call useCallback at line 158 - order unresolved
    Note over S,U: Source contains call splice at line 159 - order unresolved
    Note over S,U: Source contains call resolve at line 159 - order unresolved
```
