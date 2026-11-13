# Manage area shell: route table, nav groups, admin lock — flow-diagram

Source revision: d658ad5b71dcb67da0ae540d6d5ff80ca8efefd5. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:6d8542ad14b6a67b4e935b8d909e9802 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/manage/index\.tsx:190; symbol: AdminUnlocked; span SHA256: 2fbb9c781a0203abe431258f0a69e258cc97a537c32fb8a006ec511fe1ae2a91.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 6236381b2381a125e7d325200023a2b4
    Note over S,U: Source contains call useEffect at line 191 - order unresolved
    Note over S,U: Source contains call onChange at line 192 - order unresolved
    Note over S,U: Source contains call onChange at line 193 - order unresolved
    Note over S,U: Source contains call useLocation at line 129 - order unresolved
    Note over S,U: Source contains call useRegister at line 199 - order unresolved
    Note over S,U: Source contains call useLocation at line 200 - order unresolved
    Note over S,U: Source contains call useSession at line 201 - order unresolved
    Note over S,U: Source contains call useT at line 202 - order unresolved
    Note over S,U: Source contains call useEffect at line 203 - order unresolved
    Note over S,U: Source contains call setRegister at line 203 - order unresolved
    Note over S,U: Source contains call currentRouteLabel at line 204 - order unresolved
    Note over S,U: Source contains call t at line 209 - order unresolved
    Note over S,U: Source contains call t at line 221 - order unresolved
    Note over S,U: Source contains call map at line 222 - order unresolved
    Note over S,U: Source contains call map at line 224 - order unresolved
    Note over S,U: Source contains call t at line 231 - order unresolved
    Note over S,U: Source contains call t at line 240 - order unresolved
    Note over S,U: Source contains call useState at line 138 - order unresolved
    Note over S,U: Source contains call useRef at line 139 - order unresolved
    Note over S,U: Source contains call flat at line 107 - order unresolved
    Note over S,U: Source contains call find at line 107 - order unresolved
    Note over S,U: Source contains call startsWith at line 108 - order unresolved
    Note over S,U: Source contains call useCallback at line 142 - order unresolved
    Note over S,U: Source contains call splice at line 143 - order unresolved
    Note over S,U: Source contains call resolve at line 143 - order unresolved
```
