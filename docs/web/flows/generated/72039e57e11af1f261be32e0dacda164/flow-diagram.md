# Viewer notifications inbox — flow-diagram

Source revision: e2ffd915f3bda309ca8792f07724053b048ba15f. Trust: generated. Freshness: pinned source; run docs flows check for a later revision.

<!-- flowdocs:paragraph:022de9e1c3870011e548cee6056eaee1 -->
Static source inventory only; sequence and runtime ordering are unresolved\.

Source: internal/web/app/src/areas/viewer/pages/NotificationsPage\.tsx:12; symbol: NOTIFICATIONS\_CHANGED\_EVENT; span SHA256: 7dde5112c416d78842d0a78b8b573bc6866f08d7ac535b0bcf9c82ce0969e72c.

```mermaid
sequenceDiagram
    participant S as Static source
    participant U as Unresolved runtime
    Note over S,U: Lexical inventory only - no witnessed order
    Note over S: Flow 72039e57e11af1f261be32e0dacda164
    Note over S,U: Source contains call useT at line 126 - order unresolved
    Note over S,U: Source contains call notificationKindLabel at line 136 - order unresolved
    Note over S,U: Source contains call t at line 137 - order unresolved
    Note over S,U: Source contains call formatDateTime at line 139 - order unresolved
    Note over S,U: Source contains call onMarkRead at line 152 - order unresolved
    Note over S,U: Source contains call t at line 154 - order unresolved
    Note over S,U: Source contains call useT at line 19 - order unresolved
    Note over S,U: Source contains call useState at line 21 - order unresolved
    Note over S,U: Source contains call useCallback at line 24 - order unresolved
    Note over S,U: Source contains call async at line 25 - order unresolved
    Note over S,U: Source contains call setError at line 26 - order unresolved
    Note over S,U: Source contains call setLoading at line 27 - order unresolved
    Note over S,U: Source contains call setNotifications at line 29 - order unresolved
    Note over S,U: Source contains call fetchNotifications at line 29 - order unresolved
    Note over S,U: Source contains call setError at line 31 - order unresolved
    Note over S,U: Source contains call t at line 31 - order unresolved
    Note over S,U: Source contains call setLoading at line 33 - order unresolved
    Note over S,U: Source contains call useEffect at line 39 - order unresolved
    Note over S,U: Source contains call refreshNotifications at line 40 - order unresolved
    Note over S,U: Source contains call useEffect at line 43 - order unresolved
    Note over S,U: Source contains call Date at line 175 - order unresolved
    Note over S,U: Source contains call isNaN at line 176 - order unresolved
    Note over S,U: Source contains call getTime at line 176 - order unresolved
    Note over S,U: Source contains call toLocaleString at line 177 - order unresolved
    Note over S,U: Source contains call t at line 164 - order unresolved
    Note over S,U: Source contains call t at line 166 - order unresolved
    Note over S,U: Source contains call t at line 168 - order unresolved
    Note over S,U: Source contains call t at line 170 - order unresolved
    Note over S,U: Source contains call setError at line 57 - order unresolved
    Note over S,U: Source contains call markNotificationRead at line 59 - order unresolved
    Note over S,U: Source contains call setNotifications at line 61 - order unresolved
    Note over S,U: Source contains call map at line 62 - order unresolved
    Note over S,U: Source contains call dispatchEvent at line 64 - order unresolved
    Note over S,U: Source contains call Event at line 64 - order unresolved
    Note over S,U: Source contains call refreshNotifications at line 67 - order unresolved
    Note over S,U: Source contains call setError at line 69 - order unresolved
    Note over S,U: Source contains call t at line 69 - order unresolved
    Note over S,U: Source contains call refreshNotifications at line 46 - order unresolved
```
